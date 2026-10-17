// Package daemon is ytmd's Service: it owns the player, the playback and
// its engine (with the system's media controls), the account and the
// saved state on one loop goroutine (package loop), and serves front-ends
// the api protocol over connections (package ipc).
//
// Every request, engine event and fetch result runs on the loop; after each
// batch the Service sends its subscribers what changed: the State when
// anything but the moving position changed, or the position jumped (a
// seek), never on a timer; the queue when it changed, with the songs new
// to it; the playback's notices. The account's changes go out as they
// come, after the reply of the request that caused them (a Like's reply
// precedes its Likes; SignIn's and SignOut's follow their Account, as
// package account orders them). A subscriber first gets a snapshot: the
// State, the queue and all its songs, then the account's status, library
// and likes as far as fetched (a kind not yet fetched is left out; its
// event follows), then a Failed event per Part whose last fetch failed.
//
// Catalog requests run on goroutines of their own with a context that a
// Cancel frame or the connection's end cancels; their replies come back
// through the loop. A Cancel or the connection's end cancels a SignIn too,
// unless another replaced it. The loop never waits on a socket: a peer
// that reads too slowly loses its connection (ipc.Conn).
//
// The daemon keeps no catalog but its queue's songs, which callers send
// with the IDs they play or enqueue (api.IDsArgs.Songs) and daemon.json
// keeps, and the account's liked songs: those of Liked music fetched, and
// those liked since whose Like described them (api.LikeArgs.Song), which
// the Likes event of the like carries to the other front-ends. A sign-in that switches accounts empties the queue: its songs
// were the previous account's. Play plays one song as Desktop's rows do
// (resumes or pauses it when current, jumps to it when queued, else
// queues it after the current song); PlayAll makes the IDs the queue,
// from Start. The queue holds each song once (package player).
//
// A peer of another Proto may only Ping, Handoff and Shutdown; it gets no
// events but a HandoffEvent.
package daemon

import (
	"context"
	"errors"
	"net"
	"os"
	"sync"
	"time"
	"yt-music-player/internal/account"
	"yt-music-player/internal/audio"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/ipc"
	"yt-music-player/internal/loop"
	"yt-music-player/internal/playback"
	"yt-music-player/internal/player"
	"yt-music-player/internal/stream"

	"github.com/rs/zerolog/log"
)

const (
	// defaultSaveDelay is how long the state waits after a change to the
	// queue or a setting before it is saved, so a burst saves once.
	defaultSaveDelay = 2 * time.Second
	// resumeSlack is how long past a hand-off's Wait the new daemon still
	// resumes playing, for a slow start.
	resumeSlack = 30 * time.Second
	// flushTimeout bounds how long a stopping daemon waits for its peers
	// to read what it sent last.
	flushTimeout = time.Second
)

// Options make a Service.
type Options struct {
	// Source is where music comes from; one that is also catalog.Accounts
	// signs in and out.
	Source catalog.Source
	// Resolver finds songs' streams and NewEngine makes the engine as the
	// first song plays, as playback.Playback's. Without a Resolver nothing
	// plays: the player acts as a mock.
	Resolver  stream.Resolver
	NewEngine func(events func(audio.Event)) (audio.Engine, error)
	// State is the path of daemon.json; empty keeps nothing.
	State string
	// Seed is the queue without saved state, such as the placeholder's
	// songs.
	Seed []catalog.Song
	// Now is the clock; nil is time.Now.
	Now func() time.Time
	// SaveDelay is how long after a change the state is saved; 0 is 2 s.
	SaveDelay time.Duration
}

// Service is the daemon. Its methods are safe from any goroutine.
type Service struct {
	loop *loop.Loop
	// post is loop.Post, made once: a method value allocates.
	post func(func())
	pid  int
	now  func() time.Time

	// The rest but lis, lock, mu's and done are the loop's.
	src    catalog.Source
	cat    *catalog.Catalog // the queue's songs only
	player *player.Player
	pb     *playback.Playback
	acct   *account.Account
	// ctx is every catalog request's parent, canceled as the Service stops.
	ctx    context.Context
	cancel context.CancelFunc

	conns map[*conn]struct{}
	sent  sentState
	// signIn is the SignIn in flight; signOuts wait for the SignOut in
	// flight.
	signIn   *pendingSignIn
	signOuts []pendingReply

	store *store // nil keeps nothing
	quiet quietGC
	// resume plays at Start: a hand-off's resumePlaying.
	resume   bool
	stopping bool

	lis  net.Listener
	lock *ipc.Lock

	mu     sync.Mutex
	live   map[*ipc.Conn]struct{} // every connection served
	closed bool                   // no more connections
	done   chan struct{}
}

// New returns a Service, its state loaded from opts.State, and starts its
// loop. Nothing plays, fetches or makes an engine until Start: on macOS,
// the engine may only be made once the main thread runs its run loop
// (RunMain).
func New(opts Options) *Service {
	s := newService(opts)
	go s.loop.Run()
	return s
}

// newService is New without its loop running: benchmarks run the loop's
// work on their own goroutine.
func newService(opts Options) *Service {
	s := &Service{
		pid:   os.Getpid(),
		now:   opts.Now,
		src:   opts.Source,
		conns: map[*conn]struct{}{},
		live:  map[*ipc.Conn]struct{}{},
		done:  make(chan struct{}),
	}
	if s.now == nil {
		s.now = time.Now
	}
	s.loop = loop.New(s.settle)
	s.post = s.loop.Post
	s.ctx, s.cancel = context.WithCancel(context.Background())

	saved, ok := loadState(opts.State)
	s.restore(saved, ok, opts.Seed)
	if opts.State != "" {
		delay := opts.SaveDelay
		if delay <= 0 {
			delay = defaultSaveDelay
		}
		s.store = newStore(opts.State, delay)
	}

	s.pb = playback.New(s.cat, s.player, s.post)
	s.pb.Resolver, s.pb.NewEngine = opts.Resolver, opts.NewEngine
	s.acct = account.New(s.src, s.post, s.accountChanged)
	s.sent.reset(s)
	return s
}

// Open starts ytmd's way (ipc): it prepares the directories of paths,
// takes the lock (ipc.ErrLocked: a daemon runs already), loads the state
// from paths.State, and listens on paths.Socket, removing a stale one. The
// state is loaded before anyone can connect, so the first snapshot is
// whole. Start accepts the connections; stopping releases the lock.
func Open(paths ipc.Paths, opts Options) (*Service, error) {
	if err := paths.Prepare(); err != nil {
		return nil, err
	}
	lock, err := ipc.TryLock(paths.Lock)
	if err != nil {
		return nil, err
	}
	opts.State = paths.State
	s := New(opts)
	lis, err := ipc.Listen(paths.Socket)
	if err != nil {
		s.Shutdown()
		_ = lock.Unlock()
		return nil, err
	}
	s.lis, s.lock = lis, lock
	return s, nil
}

// Start resumes playing after a hand-off, fetches the account, and accepts
// connections on Open's socket.
func (s *Service) Start() {
	s.post(s.start)
	if s.lis != nil {
		go s.accept(s.lis)
	}
}

func (s *Service) start() {
	if s.resume {
		s.resume = false
		s.player.Resume()
		// Saved anew without resumePlaying: read once.
		s.save()
	}
	for p := range account.Liked + 1 {
		s.acct.Fetch(p)
	}
	s.pb.Follow()
}

func (s *Service) accept(lis net.Listener) {
	for {
		nc, err := lis.Accept()
		if err != nil {
			if s.isClosed() || errors.Is(err, net.ErrClosed) {
				return
			}
			log.Debug().Err(err).Msg("accept")
			// Out of files, say: give the others time to close theirs.
			time.Sleep(100 * time.Millisecond)
			continue
		}
		go s.Serve(nc)
	}
}

func (s *Service) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// Shutdown stops the Service as ytmd stops (Shutdown, SIGTERM, SIGINT):
// it pauses, saves the state, closes the engine and its media controls,
// stops listening (removing the socket), flushes and closes the
// connections and releases the lock, then returns. It returns at once
// once the Service stopped.
func (s *Service) Shutdown() {
	s.post(func() { s.stop(time.Time{}) })
	<-s.done
}

// Done is closed once the Service stopped: after Shutdown, a Shutdown
// request or a hand-off.
func (s *Service) Done() <-chan struct{} { return s.done }

// stop pauses, saves, closes everything and stops the loop; a hand-off
// has the next daemon resume until resumeBefore. The connections are
// flushed and closed off the loop.
func (s *Service) stop(resumeBefore time.Time) {
	if s.stopping {
		return
	}
	s.stopping = true
	wasPlaying := s.player.Playing
	s.player.Pause()
	s.pb.Follow()
	if s.store != nil {
		st := s.saved()
		if !resumeBefore.IsZero() {
			st.ResumePlaying, st.ResumeBefore = wasPlaying, resumeBefore
		}
		s.store.save(st)
		s.store.wait()
	}
	s.pb.Stop()
	s.acct.Stop()
	s.cancel()
	for c := range s.conns {
		c.cancelAll()
	}
	s.quiet.stop()
	if s.lis != nil {
		// The socket goes with it.
		_ = s.lis.Close()
	}
	s.loop.Stop()
	go s.finish()
}

// finish flushes and closes every connection, releases the lock and marks
// the Service done.
func (s *Service) finish() {
	s.mu.Lock()
	s.closed = true
	live := make([]*ipc.Conn, 0, len(s.live))
	for c := range s.live {
		live = append(live, c)
	}
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), flushTimeout)
	var wg sync.WaitGroup
	for _, c := range live {
		wg.Go(func() { _ = c.Shutdown(ctx) })
	}
	wg.Wait()
	cancel()
	if s.lock != nil {
		if err := s.lock.Unlock(); err != nil {
			log.Debug().Err(err).Msg("unlock")
		}
	}
	close(s.done)
}
