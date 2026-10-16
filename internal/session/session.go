// Package session keeps what package account reports (who is signed in,
// their library and likes) as the pages read it: fetch states and music in
// the catalog. Every like goes through SetLiked, which shows it at once and
// undoes it if the account says it failed. Results for the user come out
// as notices, coded rather than worded.
//
// A Session belongs to the UI thread, as does the Account it asks; it
// calls no Source and starts no goroutine.
package session

import (
	"errors"
	"slices"
	"yt-music-player/internal/account"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/fetchstate"
	"yt-music-player/internal/loader"
	"yt-music-player/internal/notice"
)

// Unlike pages' resources, these are fetched at start whatever page shows,
// and again on sign-in.
var (
	AccountRes = fetchstate.Resource{Kind: fetchstate.Account}
	LibraryRes = fetchstate.Resource{Kind: fetchstate.Library}
	LikedRes   = fetchstate.Resource{Kind: fetchstate.Liked}
)

// Resources are the account's resources, in the order Start fetches them,
// by the account.Part that fills each.
var Resources = [...]fetchstate.Resource{
	account.Profile: AccountRes,
	account.Library: LibraryRes,
	account.Liked:   LikedRes,
}

// Session is the signed-in account, or none, and what it has.
type Session struct {
	acct   *account.Account
	states *fetchstate.Registry
	loader *loader.Loader
	cat    *catalog.Catalog

	isSignedIn bool
	account    catalog.Account
	// displayName is made once rather than every build.
	displayName string
	// library lacks its playlists, which the catalog keeps (see
	// Catalog.SetLibrary).
	library catalog.Library
	// likeOrder is Liked music's order, newest first, including songs
	// unliked since; liked says which are liked, so a reverted unlike
	// keeps its place.
	likeOrder []string
	liked     map[string]bool
	likesGen  uint64
	// pending holds unanswered likes by song, so they survive a Liked
	// music fetch that crossed them; seq picks a song's latest. An account
	// change clears them so one account's likes never change the next's.
	pending map[string]pendingLike
	likeSeq uint64

	notices []notice.Notice
}

type pendingLike struct {
	liked bool
	seq   uint64
}

// New returns a Session that asks acct for the account's data and records
// its fetches in states, has load refetch Home when the account changes,
// and adds music to cat. acct's changes must go to Apply.
func New(acct *account.Account, states *fetchstate.Registry, load *loader.Loader, cat *catalog.Catalog) *Session {
	return &Session{
		acct:    acct,
		states:  states,
		loader:  load,
		cat:     cat,
		liked:   map[string]bool{},
		pending: map[string]pendingLike{},
	}
}

// Start fetches the Resources never fetched yet, once the app can take
// results. Later calls cost nothing.
func (s *Session) Start() {
	for p, res := range Resources {
		if s.states.State(res).State == fetchstate.Idle {
			s.fetch(account.Part(p))
		}
	}
}

// Retry fetches res again, and the account too if it failed, since the
// account has no Retry of its own in the UI.
func (s *Session) Retry(res fetchstate.Resource) {
	for p, r := range Resources {
		if r == res {
			s.fetch(account.Part(p))
		}
	}
	if s.states.State(AccountRes).State == fetchstate.Failed {
		s.fetch(account.Profile)
	}
}

// RetryFailed fetches again the Resources that failed, as when the
// computer is back online.
func (s *Session) RetryFailed() {
	for p, res := range Resources {
		if s.states.State(res).State == fetchstate.Failed {
			s.fetch(account.Part(p))
		}
	}
}

func (s *Session) fetch(p account.Part) {
	s.states.Set(Resources[p], fetchstate.Fetch{State: fetchstate.Loading})
	s.acct.Fetch(p)
}

// Apply keeps a change the account reports.
func (s *Session) Apply(c account.Change) {
	switch {
	case c.Status != nil:
		s.applyStatus(*c.Status)
	case c.Library != nil:
		s.setLibrary(*c.Library)
		s.states.Set(LibraryRes, fetchstate.Fetch{State: fetchstate.Loaded})
	case c.Likes != nil:
		s.applyLikes(c.Likes)
	case c.Failed != nil:
		s.states.Set(Resources[c.Failed.Part], fetchstate.Fetch{State: fetchstate.Failed, Err: c.Failed.Err})
	}
}

func (s *Session) applyStatus(status account.Status) {
	switch {
	case !status.SignedIn:
		s.forget()
	case status.Switched:
		s.setAccount(status.Account)
		s.clearLibrary()
		for _, res := range Resources {
			s.states.Set(res, fetchstate.Fetch{State: fetchstate.Loading})
		}
		s.loader.Forget(loader.HomeFeed)
	default:
		s.setAccount(status.Account)
		s.states.Set(AccountRes, fetchstate.Fetch{State: fetchstate.Loaded})
	}
}

func (s *Session) applyLikes(likes *account.Likes) {
	if likes.Full {
		s.cat.Add(likes.Songs, nil, nil)
		s.seedLikes(loader.Unique(likes.IDs, func(id string) string { return id }))
		s.states.Set(LikedRes, fetchstate.Fetch{State: fetchstate.Loaded})
		return
	}
	for _, id := range likes.Added {
		s.keepLike(id, true)
	}
	for _, id := range likes.Removed {
		s.keepLike(id, false)
	}
}

// keepLike applies a like that went through, unless the user's own like
// of the song is pending: it is newer.
func (s *Session) keepLike(id string, liked bool) {
	if _, ok := s.pending[id]; ok || s.Liked(id) == liked {
		return
	}
	s.applyLike(id, liked)
}

// KeepAccount stores user and lib as if fetched.
func (s *Session) KeepAccount(user catalog.Account, lib catalog.Library) {
	s.setAccount(user)
	s.setLibrary(lib)
	s.states.Set(AccountRes, fetchstate.Fetch{State: fetchstate.Loaded})
	s.states.Set(LibraryRes, fetchstate.Fetch{State: fetchstate.Loaded})
}

// KeepLikes stores ids, newest first, as the liked songs as if fetched;
// the catalog must have the songs already.
func (s *Session) KeepLikes(ids []string) {
	s.seedLikes(ids)
	s.states.Set(LikedRes, fetchstate.Fetch{State: fetchstate.Loaded})
}

func (s *Session) setAccount(user catalog.Account) {
	s.isSignedIn, s.account, s.displayName = true, user, user.Name
	if user.Handle != "" {
		s.displayName = user.Name + " (" + user.Handle + ")"
	}
}

// setLibrary adds lib's music to the catalog, so it plays and opens like
// the rest, and makes its playlists the catalog's library. Duplicates are
// dropped because the UI keys entries by ID.
func (s *Session) setLibrary(lib catalog.Library) {
	lib.Playlists = loader.Unique(lib.Playlists, func(pl catalog.Playlist) string { return pl.ID })
	lib.Albums = loader.Unique(lib.Albums, func(al catalog.Album) string { return al.ID })
	lib.Artists = loader.Unique(lib.Artists, catalog.Artist.Key)
	lib.Songs = loader.Unique(lib.Songs, func(song catalog.Song) string { return song.ID })
	s.cat.Add(lib.Songs, lib.Albums, lib.Playlists)
	ids := make([]string, len(lib.Playlists))
	for i, pl := range lib.Playlists {
		ids[i] = pl.ID
	}
	s.cat.SetLibrary(ids)
	lib.Playlists = nil
	s.library = lib
}

// seedLikes makes ids, newest first, the liked songs. Pending likes are
// reapplied: they are newer than the fetch.
func (s *Session) seedLikes(ids []string) {
	s.likeOrder = ids
	s.likesGen++
	s.liked = make(map[string]bool, len(ids))
	for _, id := range ids {
		s.liked[id] = true
	}
	for id, like := range s.pending {
		s.applyLike(id, like.liked)
	}
}

// applyLike puts a newly liked song first in Liked music.
func (s *Session) applyLike(id string, liked bool) {
	s.likesGen++
	if !liked {
		delete(s.liked, id)
		return
	}
	s.liked[id] = true
	if !slices.Contains(s.likeOrder, id) {
		s.likeOrder = slices.Insert(s.likeOrder, 0, id)
	}
}

// Liked reports whether the user likes song id.
func (s *Session) Liked(id string) bool { return s.liked[id] }

// IsSignedIn reports whether someone is signed in, as far as the app knows.
func (s *Session) IsSignedIn() bool { return s.isSignedIn }

// Account returns the signed-in account, or the zero Account.
func (s *Session) Account() catalog.Account { return s.account }

// DisplayName returns the signed-in account's name with its handle.
func (s *Session) DisplayName() string { return s.displayName }

// Library returns the library without its playlists, which are the
// catalog's LibraryPlaylists.
func (s *Session) Library() *catalog.Library { return &s.library }

// LikesGen changes whenever the likes do, so derived data is remade only then.
func (s *Session) LikesGen() uint64 { return s.likesGen }

// LikedSongs returns the liked songs the catalog has, newest first.
func (s *Session) LikedSongs() []catalog.Song {
	songs := make([]catalog.Song, 0, len(s.likeOrder))
	for _, id := range s.likeOrder {
		if !s.Liked(id) {
			continue
		}
		if song, ok := s.cat.Song(id); ok {
			songs = append(songs, song)
		}
	}
	return songs
}

// LikedCount returns how many songs are liked.
func (s *Session) LikedCount() int {
	count := 0
	for _, id := range s.likeOrder {
		if s.Liked(id) {
			count++
		}
	}
	return count
}

// SetLiked likes or unlikes song id at once, then has the account send it;
// on failure it is undone and a notice tells why.
func (s *Session) SetLiked(id string, liked bool) {
	if id == "" || s.Liked(id) == liked {
		return
	}
	if s.states.State(AccountRes).State == fetchstate.SignedOut {
		s.notify(signInToLike())
		return
	}
	s.applyLike(id, liked)
	s.likeSeq++
	seq := s.likeSeq
	s.pending[id] = pendingLike{liked: liked, seq: seq}
	s.acct.Like(id, liked, func(err error) { s.finishLike(id, liked, seq, err) })
}

func (s *Session) finishLike(id string, liked bool, seq uint64, err error) {
	// The account changed, or a later click on the same song decides.
	if like, ok := s.pending[id]; !ok || like.seq != seq {
		return
	}
	delete(s.pending, id)
	switch {
	case errors.Is(err, catalog.ErrSignedOut):
		// The account reports the sign-out next.
		s.notify(signInToLike())
	case err != nil:
		s.applyLike(id, !liked)
		s.notify(s.likeFailed(id, liked, err))
	}
}

func (s *Session) likeFailed(id string, liked bool, err error) notice.Notice {
	song, ok := s.cat.Song(id)
	switch {
	case !ok && liked:
		return failed(notice.Failure, notice.LikeFailedSong, err)
	case !ok:
		return failed(notice.Failure, notice.UnlikeFailedSong, err)
	case liked:
		return failed(notice.Failure, notice.LikeFailed, err, song.Title)
	}
	return failed(notice.Failure, notice.UnlikeFailed, err, song.Title)
}

// failed is a notice of title, given its args, whose detail is err's text.
func failed(kind notice.Kind, title notice.Code, err error, args ...string) notice.Notice {
	return notice.Notice{Kind: kind, Title: title, Detail: notice.Reason, Args: append(args, err.Error())}
}

func (s *Session) notify(n notice.Notice) { s.notices = append(s.notices, n) }

func signInToLike() notice.Notice {
	return notice.Notice{Kind: notice.NeedsAccount, Title: notice.SignInToLike, Topic: notice.SignIn}
}

// TakeNotices returns and clears the notices since the last call.
func (s *Session) TakeNotices() []notice.Notice {
	taken := s.notices
	s.notices = nil
	return taken
}

// forget signs out locally: no library, no likes, and Home refetched if
// it was someone's.
func (s *Session) forget() {
	wasSignedIn := s.isSignedIn
	for _, res := range Resources {
		s.states.Set(res, fetchstate.Fetch{State: fetchstate.SignedOut})
	}
	s.isSignedIn, s.account, s.displayName = false, catalog.Account{}, ""
	s.clearLibrary()
	if wasSignedIn {
		s.loader.Forget(loader.HomeFeed)
	}
}

func (s *Session) clearLibrary() {
	s.library = catalog.Library{}
	s.cat.SetLibrary(nil)
	clear(s.pending)
	s.seedLikes(nil)
}

// CanSignIn reports whether the source signs in and out at all.
func (s *Session) CanSignIn() bool { return s.acct.CanSignIn() }

// SignIn has the account send credentials, as pasted, canceling a sign-in
// in flight; the caller should forget them. The result comes as a
// SignedIn or SignInError notice.
func (s *Session) SignIn(credentials string) { s.acct.SignIn(credentials, s.finishSignIn) }

func (s *Session) finishSignIn(user catalog.Account, err error) {
	if err != nil {
		s.notify(failed(notice.SignInError, notice.SignInFailed, err))
		return
	}
	s.notify(notice.Notice{Kind: notice.SignedIn, Title: notice.SignedInAs, Args: []string{user.Name}})
}

// IsSigningIn reports whether a sign-in is in flight.
func (s *Session) IsSigningIn() bool { return s.acct.IsSigningIn() }

// CancelSignIn gives up the sign-in in flight; its failure is never told.
func (s *Session) CancelSignIn() { s.acct.CancelSignIn() }

// SignOut has the account clear the stored credentials, then forgets the
// account.
func (s *Session) SignOut() { s.acct.SignOut(s.finishSignOut) }

func (s *Session) finishSignOut(err error) {
	if err != nil {
		s.notify(failed(notice.Failure, notice.SignOutFailed, err))
		return
	}
	s.notify(notice.Notice{Kind: notice.Info, Title: notice.SignedOut})
}

// IsSigningOut reports whether a sign-out is in flight.
func (s *Session) IsSigningOut() bool { return s.acct.IsSigningOut() }

// IsBusy reports whether a fetch, a like, or a sign-in or sign-out is in flight.
func (s *Session) IsBusy() bool { return len(s.pending) > 0 || s.acct.IsBusy() }
