// Package session keeps what ytmd's account reports (api events) as the
// pages read it: fetch states and music in the catalog. SetLiked shows a
// like at once and undoes it if the daemon says it failed; results come
// out as notices.
//
// A Session asks ytmd through a Daemon (remote.Account) and tracks what
// is not answered yet, relying on an answer coming before the events its
// request caused. It belongs to the UI thread, as do the Daemon's answers
// and the Apply methods' events.
package session

import (
	"errors"
	"slices"
	"yt-music-player/internal/api"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/fetchstate"
	"yt-music-player/internal/loader"
	"yt-music-player/internal/notice"
)

// The account's resources, fetched by ytmd whatever page shows.
var (
	AccountRes = fetchstate.Resource{Kind: fetchstate.Account}
	LibraryRes = fetchstate.Resource{Kind: fetchstate.Library}
	LikedRes   = fetchstate.Resource{Kind: fetchstate.Liked}
)

// Resources are the account's resources: Resources[p-1] is api.Part p's.
var Resources = [...]fetchstate.Resource{
	api.PartAccount - 1: AccountRes,
	api.PartLibrary - 1: LibraryRes,
	api.PartLiked - 1:   LikedRes,
}

// parts are the api.Parts, in Resources' order.
var parts = [...]api.Part{api.PartAccount, api.PartLibrary, api.PartLiked}

// Daemon is what a Session asks of ytmd; tests fake it. Its done funcs
// run on the Session's thread, each before the events its request caused.
type Daemon interface {
	// Refresh has part fetched anew, whose result comes as an event; done
	// gets why the request failed, or nil.
	Refresh(part api.Part, done func(error))
	// Like likes or unlikes song id. song, when liked, is what the Session
	// knows of it, or nil, for the other front-ends' Liked music.
	Like(id string, liked bool, song *catalog.Song, done func(error))
	// SignIn sends credentials as pasted, canceling a sign-in in flight,
	// whose failure may never come.
	SignIn(credentials string, done func(catalog.Account, error))
	// CancelSignIn gives up the sign-in in flight; credentials stored
	// anyway still sign in, which only the Account event tells.
	CancelSignIn()
	// SignOut clears the stored credentials; done comes after the Account
	// event that no one is signed in.
	SignOut(done func(error))
}

// Session is the signed-in account, or none, and what it has.
type Session struct {
	daemon  Daemon
	fetches *fetchstate.Registry
	loader  *loader.Loader
	cat     *catalog.Catalog

	account catalog.Account
	// displayName is made once rather than every build.
	displayName string
	// library lacks its playlists, which the catalog keeps.
	library catalog.Library
	// likeOrder keeps newest-first order, including unliked songs; liked filters it,
	// so reverting an unlike restores the song's place.
	likeOrder []string
	liked     map[string]bool
	likesGen  uint64
	// pending keeps each song's latest unanswered like across crossing fetches.
	// Account changes clear it; likeSeq identifies the latest request.
	pending map[string]pendingLike
	likeSeq uint64

	signInSeq uint64
	notices   []notice.Notice

	isSignedIn bool
	// isSigningIn lasts until the latest signInSeq answers or CancelSignIn.
	isSigningIn  bool
	isSigningOut bool
}

type pendingLike struct {
	liked bool
	seq   uint64
}

// New returns a Session that records each fetch in fetches, has load refetch
// Home when the account changes, and adds music to cat. The account's
// events must go to its Apply methods.
func New(d Daemon, fetches *fetchstate.Registry, load *loader.Loader, cat *catalog.Catalog) *Session {
	return &Session{
		daemon:  d,
		fetches: fetches,
		loader:  load,
		cat:     cat,
		liked:   map[string]bool{},
		pending: map[string]pendingLike{},
	}
}

// Retry fetches res again, and the account too if it failed, since the
// account has no Retry of its own in the UI.
func (s *Session) Retry(res fetchstate.Resource) {
	for i, r := range Resources {
		if r == res {
			s.fetch(parts[i])
		}
	}
	if s.fetches.Get(AccountRes).State == fetchstate.Failed {
		s.fetch(api.PartAccount)
	}
}

// RetryFailed fetches again the Resources that failed.
func (s *Session) RetryFailed() {
	for i, res := range Resources {
		if s.fetches.Get(res).State == fetchstate.Failed {
			s.fetch(parts[i])
		}
	}
}

func (s *Session) fetch(p api.Part) {
	s.fetches.Set(Resources[p-1], fetchstate.Fetch{State: fetchstate.Loading})
	s.daemon.Refresh(p, func(err error) { s.refreshed(p, err) })
}

// refreshed fails p's fetch whose request failed, unless an event came
// since.
func (s *Session) refreshed(p api.Part, err error) {
	if err != nil && s.fetches.Get(Resources[p-1]).State == fetchstate.Loading {
		s.fail(p, err)
	}
}

func (s *Session) fail(p api.Part, err error) {
	s.fetches.Set(Resources[p-1], fetchstate.Fetch{State: fetchstate.Failed, Err: err})
}

// ApplyAccount keeps who is signed in (remote.AccountEvents).
func (s *Session) ApplyAccount(status api.AccountEvent) {
	switch {
	case !status.SignedIn:
		s.forget()
	case status.Switched:
		s.setAccount(status.Account)
		s.clearLibrary()
		for _, res := range Resources {
			s.fetches.Set(res, fetchstate.Fetch{State: fetchstate.Loading})
		}
		s.loader.Forget(loader.HomeFeed)
	default:
		s.setAccount(status.Account)
		s.fetches.Set(AccountRes, fetchstate.Fetch{State: fetchstate.Loaded})
	}
}

// ApplyLibrary keeps the library fetched (remote.AccountEvents).
func (s *Session) ApplyLibrary(lib catalog.Library) {
	s.setLibrary(lib)
	s.fetches.Set(LibraryRes, fetchstate.Fetch{State: fetchstate.Loaded})
}

// ApplyFailed keeps a fetch that failed (remote.AccountEvents); the
// pages show its error with Retry.
func (s *Session) ApplyFailed(f api.FailedEvent) {
	if !f.Part.IsValid() {
		return
	}
	err := f.Err.Err()
	if err == nil {
		err = api.ErrInternal
	}
	s.fail(f.Part, err)
}

// ApplyLikes keeps the likes fetched, or those that went through
// (remote.AccountEvents), adding the songs they describe to the catalog.
func (s *Session) ApplyLikes(likes api.Likes) {
	if len(likes.Songs) > 0 && !likes.Full {
		s.cat.Add(likes.Songs, nil, nil)
	}
	if likes.Full {
		s.cat.Add(likes.Songs, nil, nil)
		s.seedLikes(loader.Unique(likes.IDs, func(id string) string { return id }))
		s.fetches.Set(LikedRes, fetchstate.Fetch{State: fetchstate.Loaded})
		return
	}
	for _, id := range likes.Added {
		s.keepLike(id, true)
	}
	for _, id := range likes.Removed {
		s.keepLike(id, false)
	}
}

// keepLike applies a like that went through, unless the user's own,
// newer, is pending.
func (s *Session) keepLike(id string, liked bool) {
	if _, ok := s.pending[id]; ok || s.IsLiked(id) == liked {
		return
	}
	s.applyLike(id, liked)
}

// KeepAccount stores user and lib as if fetched.
func (s *Session) KeepAccount(user catalog.Account, lib catalog.Library) {
	s.setAccount(user)
	s.setLibrary(lib)
	s.fetches.Set(AccountRes, fetchstate.Fetch{State: fetchstate.Loaded})
	s.fetches.Set(LibraryRes, fetchstate.Fetch{State: fetchstate.Loaded})
}

// KeepLikes stores ids, newest first, as the liked songs as if fetched;
// the catalog must have the songs already.
func (s *Session) KeepLikes(ids []string) {
	s.seedLikes(ids)
	s.fetches.Set(LikedRes, fetchstate.Fetch{State: fetchstate.Loaded})
}

func (s *Session) setAccount(user catalog.Account) {
	s.isSignedIn, s.account, s.displayName = true, user, user.Name
	if user.Handle != "" {
		s.displayName = user.Name + " (" + user.Handle + ")"
	}
}

// setLibrary adds lib's music to the catalog and makes its playlists the
// catalog's library. Duplicates are dropped as the UI keys entries by ID.
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

// IsLiked reports whether the user likes song id.
func (s *Session) IsLiked(id string) bool { return s.liked[id] }

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
		if !s.IsLiked(id) {
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
		if s.IsLiked(id) {
			count++
		}
	}
	return count
}

// SetLiked likes or unlikes song id at once, then has the account send it;
// on failure it is undone and a notice tells why.
func (s *Session) SetLiked(id string, liked bool) {
	if id == "" || s.IsLiked(id) == liked {
		return
	}
	if s.fetches.Get(AccountRes).State == fetchstate.SignedOut {
		s.notify(signInToLike())
		return
	}
	s.applyLike(id, liked)
	s.likeSeq++
	seq := s.likeSeq
	s.pending[id] = pendingLike{liked: liked, seq: seq}
	var song *catalog.Song
	if liked {
		if known, ok := s.cat.Song(id); ok {
			song = &known
		}
	}
	s.daemon.Like(id, liked, song, func(err error) { s.finishLike(id, liked, seq, err) })
}

func (s *Session) finishLike(id string, liked bool, seq uint64, err error) {
	// The account changed, or a later click on the same song decides.
	if like, ok := s.pending[id]; !ok || like.seq != seq {
		return
	}
	delete(s.pending, id)
	switch {
	case errors.Is(err, catalog.ErrSignedOut):
		// The Account event of the sign-out comes next.
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
		return failed(notice.KindFailure, notice.LikeFailedSong, err)
	case !ok:
		return failed(notice.KindFailure, notice.UnlikeFailedSong, err)
	case liked:
		return failed(notice.KindFailure, notice.LikeFailed, err, song.Title)
	}
	return failed(notice.KindFailure, notice.UnlikeFailed, err, song.Title)
}

// failed is a notice of title, given its args, whose detail is err's text.
func failed(kind notice.Kind, title notice.Code, err error, args ...string) notice.Notice {
	return notice.Notice{Kind: kind, Title: title, Detail: notice.Reason, Args: append(args, err.Error())}
}

func (s *Session) notify(n notice.Notice) { s.notices = append(s.notices, n) }

func signInToLike() notice.Notice {
	return notice.Notice{Kind: notice.KindNeedsAccount, Title: notice.SignInToLike, Topic: notice.TopicSignIn}
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
		s.fetches.Set(res, fetchstate.Fetch{State: fetchstate.SignedOut})
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

// SignIn has the daemon sign in with credentials, as pasted, canceling a
// sign-in in flight; the caller should forget them. The result comes as a
// SignedIn or SignInError notice.
func (s *Session) SignIn(credentials string) {
	if api.CleanCredentials(credentials) == "" {
		return
	}
	s.signInSeq++
	seq := s.signInSeq
	s.isSigningIn = true
	s.daemon.SignIn(credentials, func(user catalog.Account, err error) { s.finishSignIn(seq, user, err) })
}

// finishSignIn tells a sign-in's success, as its account is the one
// signed in, but only the latest one's failure.
func (s *Session) finishSignIn(seq uint64, user catalog.Account, err error) {
	isLatest := s.isSigningIn && seq == s.signInSeq
	if isLatest {
		s.isSigningIn = false
	}
	switch {
	case err != nil && isLatest:
		s.notify(failed(notice.KindSignInError, notice.SignInFailed, err))
	case err == nil:
		s.notify(notice.Notice{Kind: notice.KindSignedIn, Title: notice.SignedInAs, Args: []string{user.Name}})
	}
}

// IsSigningIn reports whether a sign-in is in flight.
func (s *Session) IsSigningIn() bool { return s.isSigningIn }

// CancelSignIn gives up the sign-in in flight; its failure is never told.
func (s *Session) CancelSignIn() {
	if !s.isSigningIn {
		return
	}
	s.isSigningIn = false
	s.daemon.CancelSignIn()
}

// SignOut has the daemon clear the stored credentials, unless a sign-out
// is in flight; the Account event that follows forgets the account.
func (s *Session) SignOut() {
	if s.isSigningOut {
		return
	}
	s.isSigningOut = true
	s.daemon.SignOut(s.finishSignOut)
}

func (s *Session) finishSignOut(err error) {
	s.isSigningOut = false
	if err != nil {
		s.notify(failed(notice.KindFailure, notice.SignOutFailed, err))
		return
	}
	// The daemon dropped the sign-in in flight, which never answers.
	s.CancelSignIn()
	s.notify(notice.Notice{Kind: notice.KindInfo, Title: notice.SignedOut})
}

// IsSigningOut reports whether a sign-out is in flight.
func (s *Session) IsSigningOut() bool { return s.isSigningOut }

// IsBusy reports whether a fetch, a like, or a sign-in or sign-out is in
// flight.
func (s *Session) IsBusy() bool {
	if len(s.pending) > 0 || s.isSigningIn || s.isSigningOut {
		return true
	}
	for _, res := range Resources {
		if s.fetches.Get(res).State == fetchstate.Loading {
			return true
		}
	}
	return false
}
