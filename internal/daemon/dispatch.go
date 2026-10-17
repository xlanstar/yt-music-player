package daemon

import (
	"context"
	"errors"
	"fmt"
	"time"
	"yt-music-player/internal/account"
	"yt-music-player/internal/api"
	"yt-music-player/internal/catalog"

	"github.com/rs/zerolog/log"
)

var (
	errNoSongs     = fmt.Errorf("%w: no songs", api.ErrInvalid)
	errStart       = fmt.Errorf("%w: start out of range", api.ErrInvalid)
	errNoAccounts  = errors.New("daemon: the source signs in no one")
	errNoCookie    = fmt.Errorf("%w: nothing pasted", catalog.ErrInvalidCredentials)
	errSuperseded  = errors.New("daemon: another sign-in followed")
	errNoArguments = fmt.Errorf("%w: no arguments", api.ErrInvalid)
	errLikeSong    = fmt.Errorf("%w: a like describing another song", api.ErrInvalid)
)

// dispatch answers a request. A stopping Service answers none: after a
// hand-off, its peers wait for the next daemon.
func (s *Service) dispatch(c *conn, id uint32, cl *api.Call) {
	if s.stopping {
		return
	}
	if !c.compatible {
		s.bootstrap(c, id, cl)
		return
	}
	if err := cl.Validate(); err != nil {
		log.Debug().Stringer("method", cl.Method).Err(err).Msg("invalid call")
		c.fail(id, err)
		return
	}
	switch cl.Method {
	case api.MethodPing, api.MethodHandoff, api.MethodShutdown:
		s.bootstrap(c, id, cl)
	case api.MethodStatus:
		s.pb.Follow()
		st := s.status()
		c.reply(id, &api.Reply{Status: &st})
	case api.MethodSearch, api.MethodAlbum, api.MethodPlaylist, api.MethodHome,
		api.MethodExplore, api.MethodAccount, api.MethodLibrary:
		s.fetch(c, id, cl)
	case api.MethodLike:
		like := cl.Like
		if like.Song != nil && like.Song.ID != like.ID {
			c.fail(id, errLikeSong)
			return
		}
		s.acct.Like(like.ID, like.Liked, like.Song, func(err error) { c.fail(id, err) })
	case api.MethodSignIn:
		s.signInFor(c, id, cl.SignIn.Credentials)
	case api.MethodSignOut:
		s.signOutFor(c, id)
	case api.MethodRefresh:
		s.acct.Fetch(account.Part(cl.Part.Part - 1))
		c.reply(id, &api.Reply{})
	default:
		if err := s.control(cl); err != nil {
			log.Debug().Stringer("method", cl.Method).Err(err).Msg("invalid call")
			c.fail(id, err)
			return
		}
		c.reply(id, &api.Reply{})
	}
}

// bootstrap answers the methods of every Proto, and only those.
func (s *Service) bootstrap(c *conn, id uint32, cl *api.Call) {
	switch cl.Method {
	case api.MethodPing:
		c.reply(id, &api.Reply{})
	case api.MethodShutdown:
		c.reply(id, &api.Reply{})
		s.stop(time.Time{})
	case api.MethodHandoff:
		if cl.Hand == nil {
			c.fail(id, errNoArguments)
			return
		}
		s.handoff(c, id, cl.Hand.Wait)
	default:
		c.reply(id, &api.Reply{Err: errProto})
	}
}

// handoff tells the other connections, answers, then stops, saving with
// resumePlaying.
func (s *Service) handoff(c *conn, id uint32, wait time.Duration) {
	ev := &api.Event{Handoff: &api.HandoffEvent{By: c.hello.PID, Wait: wait}}
	for o := range s.conns {
		if o != c {
			o.send(api.Frame{Event: ev})
		}
	}
	c.reply(id, &api.Reply{})
	s.stop(s.now().Add(wait + resumeSlack))
}

// control changes the player; the engine follows as the batch settles.
func (s *Service) control(cl *api.Call) error {
	p := s.player
	// Prev and the rest act on where the song is now.
	s.pb.Follow()
	switch cl.Method {
	case api.MethodPlay, api.MethodPlayAll, api.MethodEnqueue:
		ids := cl.IDs.IDs
		if len(ids) == 0 {
			return errNoSongs
		}
		start := cl.IDs.Start
		if start < 0 || start >= len(ids) {
			return errStart
		}
		s.cat.Add(cl.IDs.Songs, nil, nil)
		switch cl.Method {
		case api.MethodPlay:
			p.Play(ids[start])
		case api.MethodPlayAll:
			p.PlayAll(ids, start)
		default:
			p.Enqueue(ids, cl.IDs.Next)
		}
	case api.MethodToggle:
		p.TogglePlay()
	case api.MethodPause:
		p.Pause()
	case api.MethodResume:
		p.Resume()
	case api.MethodNext:
		p.Next()
	case api.MethodPrev:
		p.Prev()
	case api.MethodSeek:
		p.Seek(cl.Seek.Seconds)
	case api.MethodSetVolume:
		p.SetVolume(cl.Volume.Volume)
	case api.MethodToggleMute:
		p.ToggleMute()
	case api.MethodCycleRepeat:
		p.CycleRepeat()
	case api.MethodToggleShuffle:
		p.ToggleShuffle()
	case api.MethodRemove:
		p.Remove(cl.Index.Index)
	case api.MethodMove:
		p.Move(cl.Move.From, cl.Move.To)
	case api.MethodClearUpNext:
		p.ClearUpNext()
	}
	return nil
}

// fetch asks the source off the loop; a request canceled meanwhile gets
// no reply.
func (s *Service) fetch(c *conn, id uint32, cl *api.Call) {
	s.cancelCall(c, id)
	ctx, cancel := context.WithCancel(s.ctx)
	c.calls[id] = cancel
	src := s.src
	go func() {
		r := ask(ctx, src, cl)
		if ctx.Err() != nil {
			return
		}
		s.post(func() {
			if ctx.Err() != nil {
				return
			}
			delete(c.calls, id)
			cancel()
			c.reply(id, r)
		})
	}()
}

// ask runs a catalog request off the loop: it touches nothing but src.
func ask(ctx context.Context, src catalog.Source, cl *api.Call) *api.Reply {
	var (
		r   api.Reply
		err error
	)
	switch cl.Method {
	case api.MethodSearch:
		var results catalog.Results
		results, err = src.Search(ctx, cl.Query.Query)
		r.Results = &results
	case api.MethodAlbum:
		var a api.AlbumReply
		a.Album, a.Songs, err = src.Album(ctx, cl.Ref.ID)
		r.Album = &a
	case api.MethodPlaylist:
		var p api.PlaylistReply
		p.Playlist, p.Songs, err = src.Playlist(ctx, cl.Ref.ID)
		r.Playlist = &p
	case api.MethodHome:
		var home catalog.Home
		home, err = src.Home(ctx)
		r.Home = &home
	case api.MethodExplore:
		var explore catalog.Explore
		explore, err = src.Explore(ctx)
		r.Explore = &explore
	case api.MethodAccount:
		var a catalog.Account
		a, err = src.Account(ctx)
		r.Account = &a
	case api.MethodLibrary:
		var lib catalog.Library
		lib, err = src.Library(ctx)
		r.Library = &lib
	}
	if err != nil {
		log.Debug().Stringer("method", cl.Method).Err(err).Msg("catalog request failed")
		return &api.Reply{Err: api.Encode(err)}
	}
	return &r
}

// pendingReply is a request answered later.
type pendingReply struct {
	c  *conn
	id uint32
}

// pendingSignIn is a SignIn in flight, answered once.
type pendingSignIn struct {
	pendingReply
	isAnswered bool
}

// settle marks p answered and drops it from its connection's calls; false
// if it was already.
func (p *pendingSignIn) settle() bool {
	if p.isAnswered {
		return false
	}
	p.isAnswered = true
	delete(p.c.calls, p.id)
	return true
}

func (p *pendingSignIn) fail(err error) {
	if p.settle() {
		p.c.fail(p.id, err)
	}
}

// signInFor signs in for c. A sign-in replaces the one in flight, whose
// caller hears so now, as the account never answers a sign-in canceled.
// A Cancel of the request, or the end of its connection, cancels the
// account's sign-in unless another replaced it; it gets no reply.
func (s *Service) signInFor(c *conn, id uint32, credentials string) {
	switch {
	case !s.acct.CanSignIn():
		c.fail(id, errNoAccounts)
		return
	case api.CleanCredentials(credentials) == "":
		c.fail(id, errNoCookie)
		return
	}
	if s.signIn != nil {
		s.signIn.fail(errSuperseded)
	}
	s.cancelCall(c, id)
	p := &pendingSignIn{pendingReply: pendingReply{c, id}}
	s.signIn = p
	c.calls[id] = func() { s.cancelSignIn(p) }
	s.acct.SignIn(credentials, func(user catalog.Account, err error) {
		if s.signIn == p {
			s.signIn = nil
		}
		if err != nil {
			p.fail(err)
			return
		}
		if p.settle() {
			c.reply(id, &api.Reply{Account: &user})
		}
	})
}

// cancelSignIn drops p's reply and, if p is still in flight, cancels the
// account's sign-in.
func (s *Service) cancelSignIn(p *pendingSignIn) {
	p.isAnswered = true
	if s.signIn != p {
		return
	}
	s.signIn = nil
	s.acct.CancelSignIn()
}

// signOutFor signs out for c; a SignOut in flight answers it too.
func (s *Service) signOutFor(c *conn, id uint32) {
	if !s.acct.CanSignIn() {
		c.fail(id, errNoAccounts)
		return
	}
	s.signOuts = append(s.signOuts, pendingReply{c, id})
	if s.acct.IsSigningOut() {
		return
	}
	s.acct.SignOut(func(err error) {
		waiting := s.signOuts
		s.signOuts = nil
		for _, p := range waiting {
			p.c.fail(p.id, err)
		}
	})
}
