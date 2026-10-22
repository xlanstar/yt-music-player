package session

import (
	"context"
	"errors"
	"io/fs"
	"slices"
	"time"
	"yt-music-player/internal/accountcache"
	"yt-music-player/internal/fetchstate"

	"github.com/rs/zerolog/log"
)

// Cache keeps the library and likes between runs (accountcache.File): the
// Session shows them while fetching them anew, keeps each fetch's result,
// and removes them once they are no longer the signed-in account's. It is
// called off the UI thread, one call at a time.
type Cache interface {
	Load() (accountcache.Saved, error)
	Save(s *accountcache.Saved) error
	Remove() error
}

// readCache reads the cache once a run, as the first account is told:
// after that, the Session has fetched what it keeps, or another account's.
func (s *Session) readCache() {
	if s.cache == nil || s.isCacheRead {
		return
	}
	s.isCacheRead = true
	c := s.cache
	s.cacheJob.Run(s.post,
		func(context.Context) (accountcache.Saved, error) { return c.Load() },
		s.cacheRead)
}

// cacheRead shows what the cache kept of the account signed in, for the
// resources not fetched yet; one that failed keeps its Err, to fetch again
// with what failed (RetryFailed).
func (s *Session) cacheRead(saved accountcache.Saved, err error) {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return
	case err != nil:
		// Of another version, say: of no use.
		log.Debug().Err(err).Msg("reading the account's cache failed")
		s.dropCache()
		return
	case !s.isSignedIn:
		return
	case !sameAccount(saved.Account, s.account):
		s.dropCache()
		return
	}
	if !s.isLibraryFresh {
		s.setLibrary(saved.Library)
		s.fetches.Set(LibraryRes, fetchstate.Fetch{State: fetchstate.Loaded, Err: s.fetches.Get(LibraryRes).Err})
	}
	if !s.isLikesFresh {
		s.cat.Add(saved.LikedSongs, nil, nil)
		s.seedLikes(saved.Liked)
		s.fetches.Set(LikedRes, fetchstate.Fetch{State: fetchstate.Loaded, Err: s.fetches.Get(LikedRes).Err})
	}
}

// dropCache removes a cache read of no use, unless both fetches ended:
// their save replaced it.
func (s *Session) dropCache() {
	if !s.isLibraryFresh || !s.isLikesFresh {
		s.removeCache()
	}
}

// saveCache keeps the library and likes once both fetches ended, never
// one's result with the other's older one.
func (s *Session) saveCache() {
	if s.cache == nil || !s.isLibraryFresh || !s.isLikesFresh || s.libraryJob.IsRunning() || s.likesJob.IsRunning() {
		return
	}
	lib := s.library
	// Without the songs the catalog has of those opened: a start would play
	// those IDs, maybe stale, rather than fetch them.
	lib.Playlists = slices.Clone(s.cat.LibraryPlaylists())
	for i := range lib.Playlists {
		lib.Playlists[i].Songs = nil
	}
	liked := slices.AppendSeq(make([]string, 0, len(s.likeOrder)), s.likedIDs())
	saved := &accountcache.Saved{Account: s.account, Library: lib, Liked: liked, LikedSongs: s.LikedSongs()}
	c := s.cache
	s.disk(func() {
		if err := c.Save(saved); err != nil {
			log.Debug().Err(err).Msg("saving the account's cache failed")
		}
	})
}

// removeCache empties the cache: it is no one's, or another account's.
func (s *Session) removeCache() {
	if s.cache == nil {
		return
	}
	c := s.cache
	s.disk(func() {
		if err := c.Remove(); err != nil {
			log.Debug().Err(err).Msg("removing the account's cache failed")
		}
	})
}

// disk runs op off the UI thread once the one under way ended, so that the
// last asked is the last done; a newer op replaces one waiting.
func (s *Session) disk(op func()) {
	if s.diskJob.IsRunning() {
		s.nextDisk = op
		return
	}
	ended := make(chan struct{})
	s.diskEnded = ended
	s.diskJob.Run(s.post,
		func(context.Context) (struct{}, error) {
			defer close(ended)
			op()
			return struct{}{}, nil
		},
		s.diskDone)
}

func (s *Session) diskDone(struct{}, error) {
	if op := s.nextDisk; op != nil {
		s.nextDisk = nil
		s.disk(op)
	}
}

// Wait waits, at most timeout, for the cache's save or removal under way
// and the one asked after it, as the front-end exits: cut short, a save
// leaves a temporary file of personal data, and a sign-out's removal is lost.
func (s *Session) Wait(timeout time.Duration) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for s.diskJob.IsRunning() {
		select {
		case <-s.diskEnded:
		case <-timer.C:
			return
		}
		// Its result, posted to this thread, would never run.
		s.diskJob.Stop()
		s.diskDone(struct{}{}, nil)
	}
}
