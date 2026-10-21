// Package session keeps the signed-in account as the pages read it: who,
// as ytmd's account tells it (api events), and the library and liked
// songs, which it fetches through ytmd whenever ytmd says who is signed
// in. SetLiked shows a like at once and undoes it if the daemon says it
// failed; results come out as notices.
//
// A Session asks ytmd through a Daemon (remote.Account) and tracks what
// is not answered yet, relying on an answer coming before the events its
// request caused. It belongs to the UI thread, as do the Daemon's answers,
// the Apply methods' events and its fetches' results, posted there.
package session

import (
	"context"
	"errors"
	"iter"
	"slices"
	"yt-music-player/internal/accountcache"
	"yt-music-player/internal/api"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/fetchstate"
	"yt-music-player/internal/job"
	"yt-music-player/internal/loader"
	"yt-music-player/internal/notice"
)

// The account's resources, fetched whatever page shows: who is signed in
// by ytmd, the rest by the Session.
var (
	AccountRes = fetchstate.Resource{Kind: fetchstate.Account}
	LibraryRes = fetchstate.Resource{Kind: fetchstate.Library}
	LikedRes   = fetchstate.Resource{Kind: fetchstate.Liked}
)

// Resources are the account's resources.
var Resources = [...]fetchstate.Resource{AccountRes, LibraryRes, LikedRes}

// Daemon is what a Session asks of ytmd; tests fake it. Its done funcs
// run on the Session's thread, each before the events its request caused.
type Daemon interface {
	// Refresh has who is signed in fetched anew, whose result comes as an
	// event; done gets why the request failed, or nil.
	Refresh(done func(error))
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

// Source is where the library and Liked music come from: ytmd, through
// the front-end's remote.Client. Its methods run off the UI thread.
type Source interface {
	Library(ctx context.Context) (catalog.Library, error)
	Playlist(ctx context.Context, id string) (catalog.Playlist, []catalog.Song, error)
}

// Options make a Session.
type Options struct {
	// Daemon is ytmd's account: remote.Account.
	Daemon Daemon
	// Source fetches the library and Liked music off the UI thread: the
	// remote Client, through ytmd.
	Source Source
	// Post runs a func on the UI thread, where fetches' results come.
	Post    func(func())
	Fetches *fetchstate.Registry
	// Loader refetches Home when the account changes.
	Loader *loader.Loader
	// Catalog gets the music fetched.
	Catalog *catalog.Catalog
	// AccountCache is where the library and likes are kept between runs
	// (cache.go); "" keeps none.
	AccountCache string
}

// Session is the signed-in account, or none, and what it has.
type Session struct {
	daemon  Daemon
	src     Source
	post    func(func())
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

	// libraryJob and likesJob fetch the library and Liked music; told are
	// the likes that went through since, in order, newer than the list
	// fetched, or than the cache, which shows until a fetch went through.
	libraryJob, likesJob job.Job
	told                 []toldLike
	// isLibraryFresh and isLikesFresh: what shows was fetched, not cached.
	isLibraryFresh, isLikesFresh bool

	// cache keeps the library and likes between runs, nil none (cache.go).
	cache             Cache
	cacheJob, diskJob job.Job
	isCacheRead       bool
	// diskEnded closes as diskJob's op returns; nextDisk waits for it.
	diskEnded chan struct{}
	nextDisk  func()

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

type toldLike struct {
	id    string
	liked bool
}

// New returns a Session, its Resources loading until ytmd tells who is
// signed in; the account's events must go to its Apply methods
// (remote.Client.SetAccountEvents).
func New(opts Options) *Session {
	for _, res := range Resources {
		opts.Fetches.Set(res, fetchstate.Fetch{State: fetchstate.Loading})
	}
	s := &Session{
		daemon:  opts.Daemon,
		src:     opts.Source,
		post:    opts.Post,
		fetches: opts.Fetches,
		loader:  opts.Loader,
		cat:     opts.Catalog,
		liked:   map[string]bool{},
		pending: map[string]pendingLike{},
	}
	if opts.AccountCache != "" {
		s.cache = accountcache.File(opts.AccountCache)
	}
	return s
}

// Retry fetches res again, and who is signed in too if that failed, since
// the account has no Retry of its own in the UI.
func (s *Session) Retry(res fetchstate.Resource) {
	if res == AccountRes || s.fetches.Get(AccountRes).State == fetchstate.Failed {
		s.refresh()
	}
	switch {
	case res == AccountRes:
		// Refreshed above: its event fetches the rest.
	case !s.isSignedIn:
		// Who is signed in decides whose: its event fetches res.
		if s.fetches.Get(res).State == fetchstate.Failed {
			s.fetches.Set(res, fetchstate.Fetch{State: fetchstate.Loading})
		}
	case res == LibraryRes:
		s.fetchLibrary()
	case res == LikedRes:
		s.fetchLikes()
	}
}

// RetryFailed fetches again the Resources that failed, and those whose
// fetch failed while the last one's result showed (Loaded with an Err).
func (s *Session) RetryFailed() {
	failed := func(res fetchstate.Resource) bool { return s.fetches.Get(res).State == fetchstate.Failed }
	switch {
	case failed(AccountRes):
		s.Retry(AccountRes)
		// The account now Loading, these Retries ask ytmd nothing more.
		for _, res := range [...]fetchstate.Resource{LibraryRes, LikedRes} {
			if failed(res) {
				s.Retry(res)
			}
		}
	case !s.isSignedIn:
	default:
		if s.fetches.Get(LibraryRes).Err != nil {
			s.fetchLibrary()
		}
		if s.fetches.Get(LikedRes).Err != nil {
			s.fetchLikes()
		}
	}
}

// refresh has ytmd fetch who is signed in anew.
func (s *Session) refresh() {
	s.fetches.Set(AccountRes, fetchstate.Fetch{State: fetchstate.Loading})
	s.daemon.Refresh(s.refreshed)
}

// refreshed fails the fetch of who is signed in whose request failed,
// unless an event came since.
func (s *Session) refreshed(err error) {
	if err != nil && s.fetches.Get(AccountRes).State == fetchstate.Loading {
		s.accountFailed(err)
	}
}

// accountFailed fails who is signed in, and, not knowing who, the
// resources waiting on it.
func (s *Session) accountFailed(err error) {
	s.fetches.Set(AccountRes, fetchstate.Fetch{State: fetchstate.Failed, Err: err})
	if s.isSignedIn {
		return
	}
	for _, res := range [...]fetchstate.Resource{LibraryRes, LikedRes} {
		if s.fetches.Get(res).State == fetchstate.Loading {
			s.fetches.Set(res, fetchstate.Fetch{State: fetchstate.Failed, Err: err})
		}
	}
}

// ApplyAccount keeps who is signed in (remote.AccountEvents) and fetches
// what they have anew, told again too: a fetch in flight may fail with an
// old link after this.
func (s *Session) ApplyAccount(status api.AccountEvent) {
	switch {
	case !status.SignedIn:
		s.forget()
		return
	case status.Switched || s.isSignedIn && !sameAccount(status.Account, s.account):
		s.clearLibrary()
		s.removeCache()
		s.loader.Forget(loader.HomeRes)
	case s.fetches.Get(AccountRes).State == fetchstate.SignedOut:
		// Home was a guest's.
		s.loader.Forget(loader.HomeRes)
	}
	s.setAccount(status.Account)
	s.fetches.Set(AccountRes, fetchstate.Fetch{State: fetchstate.Loaded})
	s.libraryJob.Stop()
	s.likesJob.Stop()
	s.fetchLibrary()
	s.fetchLikes()
	s.readCache()
}

// ApplyFailed keeps a fetch of who is signed in that failed
// (remote.AccountEvents); the pages show its error with Retry.
func (s *Session) ApplyFailed(f api.FailedEvent) {
	err := f.Err.Err()
	if err == nil {
		err = api.ErrInternal
	}
	s.accountFailed(err)
}

// fetchLibrary fetches the library, unless a fetch is in flight; what
// shows stays meanwhile.
func (s *Session) fetchLibrary() {
	if s.libraryJob.IsRunning() {
		return
	}
	s.loading(LibraryRes)
	s.libraryJob.Run(s.post, s.src.Library, s.finishLibrary)
}

func (s *Session) finishLibrary(lib catalog.Library, err error) {
	if err != nil {
		s.failFetch(LibraryRes, err)
		return
	}
	s.isLibraryFresh = true
	s.setLibrary(lib)
	s.fetches.Set(LibraryRes, fetchstate.Fetch{State: fetchstate.Loaded})
	s.saveCache()
}

// likedMusic is Liked music fetched: its songs' IDs, newest first, and
// what the source knows of them.
type likedMusic struct {
	ids   []string
	songs []catalog.Song
}

// fetchLikes fetches Liked music, unless a fetch is in flight; the likes
// stay meanwhile, and those told go over the list it brings.
func (s *Session) fetchLikes() {
	if s.likesJob.IsRunning() {
		return
	}
	s.loading(LikedRes)
	if s.isLikesFresh {
		s.told = s.told[:0]
	}
	src := s.src
	s.likesJob.Run(s.post,
		func(ctx context.Context) (likedMusic, error) { return fetchLikes(ctx, src) },
		s.finishLikes)
}

// fetchLikes runs off the UI thread.
func fetchLikes(ctx context.Context, src Source) (likedMusic, error) {
	playlist, songs, err := src.Playlist(ctx, catalog.LikedID)
	switch {
	// An account that never liked a song may have no such playlist.
	case errors.Is(err, catalog.ErrNotFound):
		return likedMusic{}, nil
	case err != nil:
		return likedMusic{}, err
	}
	ids := playlist.Songs
	if len(ids) == 0 {
		ids = make([]string, len(songs))
		for i, song := range songs {
			ids[i] = song.ID
		}
	}
	// Decoded for this fetch, so ours to share: the catalog keeps them.
	catalog.ShareStrings(songs)
	return likedMusic{ids: ids, songs: songs}, nil
}

func (s *Session) finishLikes(likes likedMusic, err error) {
	if err != nil {
		s.failFetch(LikedRes, err)
		return
	}
	s.isLikesFresh = true
	s.cat.Add(likes.songs, nil, nil)
	s.seedLikes(likes.ids)
	s.told = s.told[:0]
	s.fetches.Set(LikedRes, fetchstate.Fetch{State: fetchstate.Loaded})
	s.saveCache()
}

// loading marks res as loading, unless what it showed stays until the
// fetch ends.
func (s *Session) loading(res fetchstate.Resource) {
	if s.fetches.Get(res).State != fetchstate.Loaded {
		s.fetches.Set(res, fetchstate.Fetch{State: fetchstate.Loading})
	}
}

// failFetch records res's fetch failed for err; what res showed stays,
// Loaded with err, to fetch again with what failed.
func (s *Session) failFetch(res fetchstate.Resource, err error) {
	switch {
	case errors.Is(err, catalog.ErrSignedOut):
		// ytmd signed out, which its Account event tells next.
		s.fetches.Set(res, fetchstate.Fetch{State: fetchstate.SignedOut})
	case s.fetches.Get(res).State == fetchstate.Loaded:
		s.fetches.Set(res, fetchstate.Fetch{State: fetchstate.Loaded, Err: err})
	default:
		s.fetches.Set(res, fetchstate.Fetch{State: fetchstate.Failed, Err: err})
	}
}

// ApplyLikes keeps the likes that went through (remote.AccountEvents),
// adding the songs they describe to the catalog.
func (s *Session) ApplyLikes(likes api.Likes) {
	if len(likes.Songs) > 0 {
		s.cat.Add(likes.Songs, nil, nil)
	}
	isNewer := s.likesJob.IsRunning() || !s.isLikesFresh
	for _, id := range likes.Added {
		s.keepLike(id, true)
		if isNewer {
			s.told = append(s.told, toldLike{id, true})
		}
	}
	for _, id := range likes.Removed {
		s.keepLike(id, false)
		if isNewer {
			s.told = append(s.told, toldLike{id, false})
		}
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

// sameAccount reports whether a and b are one account: YouTube serves its
// photo under changing URLs.
func sameAccount(a, b catalog.Account) bool { return a.Name == b.Name && a.Handle == b.Handle }

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

// seedLikes makes ids, newest first and edited in place, the liked songs,
// once each. The likes told meanwhile, then the pending ones, are
// reapplied: they are newer.
func (s *Session) seedLikes(ids []string) {
	// Made to size when empty, as at the first seed; else reused, as every
	// refetch seeds as many. None lets a large one go.
	if len(s.liked) == 0 || len(ids) == 0 {
		s.liked = make(map[string]bool, len(ids))
	} else {
		clear(s.liked)
	}
	s.likeOrder = slices.DeleteFunc(ids, func(id string) bool {
		if s.liked[id] {
			return true
		}
		s.liked[id] = true
		return false
	})
	s.likesGen++
	for _, like := range s.told {
		s.keepLike(like.id, like.liked)
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

// likedIDs yields the liked songs' IDs, newest first: likeOrder keeps
// unliked ones too.
func (s *Session) likedIDs() iter.Seq[string] {
	return func(yield func(string) bool) {
		for _, id := range s.likeOrder {
			if s.IsLiked(id) && !yield(id) {
				return
			}
		}
	}
}

// LikedSongs returns the liked songs the catalog has, newest first.
func (s *Session) LikedSongs() []catalog.Song {
	songs := make([]catalog.Song, 0, len(s.likeOrder))
	for id := range s.likedIDs() {
		if song, ok := s.cat.Song(id); ok {
			songs = append(songs, song)
		}
	}
	return songs
}

// LikedCount returns how many songs are liked.
func (s *Session) LikedCount() int {
	count := 0
	for range s.likedIDs() {
		count++
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
	s.isSignedIn, s.account, s.displayName = false, catalog.Account{}, ""
	s.clearLibrary()
	s.removeCache()
	for _, res := range Resources {
		s.fetches.Set(res, fetchstate.Fetch{State: fetchstate.SignedOut})
	}
	if wasSignedIn {
		s.loader.Forget(loader.HomeRes)
	}
}

// clearLibrary drops the library and likes, and their fetches and the
// cache's read in flight.
func (s *Session) clearLibrary() {
	s.libraryJob.Stop()
	s.likesJob.Stop()
	s.cacheJob.Stop()
	s.isLibraryFresh, s.isLikesFresh = false, false
	s.library = catalog.Library{}
	s.cat.SetLibrary(nil)
	clear(s.pending)
	s.told = s.told[:0]
	s.seedLikes(nil)
	for _, res := range [...]fetchstate.Resource{LibraryRes, LikedRes} {
		s.fetches.Set(res, fetchstate.Fetch{State: fetchstate.Loading})
	}
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

// IsBusy reports whether a fetch, a like, a sign-in or sign-out, or the
// cache's read or write is in flight.
func (s *Session) IsBusy() bool {
	isFetching := s.libraryJob.IsRunning() || s.likesJob.IsRunning()
	isCaching := s.cacheJob.IsRunning() || s.diskJob.IsRunning()
	if len(s.pending) > 0 || s.isSigningIn || s.isSigningOut || isFetching || isCaching {
		return true
	}
	for _, res := range Resources {
		if s.fetches.Get(res).State == fetchstate.Loading {
			return true
		}
	}
	return false
}
