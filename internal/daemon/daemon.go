// Package daemon is ytmd's Service: the player, the playback and its
// engine, the account and the saved state live on one loop goroutine
// (package loop), which serves front-ends the api protocol (package ipc)
// and never waits on a socket. After each batch on the loop, settle sends
// subscribers what changed; catalog requests run off the loop.
// docs/daemon.md describes the protocol, the events and daemon.json.
//
// The daemon keeps no catalog but its queue's songs, which callers send
// with the IDs (api.IDsArgs.Songs): front-ends fetch the library and likes
// through it, and keep them.
package daemon

import (
	"context"
	"errors"
	"net"
	"os"
	"time"
	"yt-music-player/internal/account"
	"yt-music-player/internal/audio"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/ipc"
	"yt-music-player/internal/loop"
	"yt-music-player/internal/playback"
	"yt-music-player/internal/player"
	"yt-music-player/internal/quiet"
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
	// first song plays (playback.Options). Without a Resolver nothing
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

	// src through quiet belong to the loop, as do shouldResume and
	// isStopping below.
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
	saves saveTracker
	quiet quiet.GC

	lis  net.Listener
	lock *ipc.Lock

	// live is shared with Serve's goroutines; done closes once finish
	// drained it.
	live liveConns
	done chan struct{}

	// shouldResume continues a hand-off's playback at Start.
	shouldResume bool
	isStopping   bool
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

	s.pb = playback.New(
		s.cat,
		s.player,
		s.post,
		playback.Options{
			Resolver:  opts.Resolver,
			NewEngine: opts.NewEngine,
			// Paused, the loop allocates too little for quiet to free on
			// its own: the released stream's ~2 MB would stay.
			Released: s.quiet.Dropped,
		},
	)
	s.acct = account.New(s.src, s.post, s.accountChanged)
	s.sent.reset(s)
	s.saves.reset(s.mark())
	return s
}

// Open takes the lock (ipc.ErrLocked: a daemon runs already), loads the
// state, then listens on paths.Socket, so the first snapshot is whole.
// Start accepts the connections; stopping releases the lock.
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

// Start resumes playing after a hand-off, fetches who is signed in, and
// accepts connections on Open's socket.
func (s *Service) Start() {
	s.post(s.start)
	if s.lis != nil {
		go s.accept(s.lis)
	}
}

func (s *Service) start() {
	s.quiet.Start()
	if s.shouldResume {
		s.shouldResume = false
		s.player.Resume()
		// Saved anew without resumePlaying: read once.
		s.save()
	}
	s.acct.Fetch()
}

func (s *Service) accept(lis net.Listener) {
	for {
		nc, err := lis.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
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

// Shutdown pauses, saves and stops the Service, and returns once it is
// done.
func (s *Service) Shutdown() {
	s.post(func() { s.stop(time.Time{}) })
	<-s.done
}

// Done is closed once the Service stopped: after Shutdown, a Shutdown
// request or a hand-off.
func (s *Service) Done() <-chan struct{} { return s.done }

// stop pauses, saves and stops the loop; a hand-off has the next daemon
// resume until resumeBefore. The connections close off the loop.
func (s *Service) stop(resumeBefore time.Time) {
	if s.isStopping {
		return
	}
	s.isStopping = true
	wasPlaying := s.player.IsPlaying()
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
	s.quiet.Stop()
	if s.lis != nil {
		_ = s.lis.Close() // removes the socket
	}
	s.loop.Stop()
	go s.finish()
}

func (s *Service) finish() {
	// Saved and unlistened already: a hand-off's next ytmd, started once the
	// asker's connection ends, must not find the lock held while others flush.
	if s.lock != nil {
		if err := s.lock.Unlock(); err != nil {
			log.Debug().Err(err).Msg("unlock")
		}
	}
	s.live.drain(flushTimeout)
	close(s.done)
}
