// Package session keeps the signed-in account, its library and its likes,
// and signs in and out through catalog.Accounts. Any call answering
// catalog.ErrSignedOut signs out everywhere. Every like goes through
// SetLiked, which shows it at once and undoes it if the source refuses.
// Results for the user come out as notices.
//
// A Session belongs to the UI thread. Its goroutines call only the Source;
// results are kept on the UI thread through post, and dropped once the
// account changed.
package session

import (
	"context"
	"errors"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/fetchstate"
	"yt-music-player/internal/job"
	"yt-music-player/internal/loader"
	"yt-music-player/internal/notice"

	"github.com/rs/zerolog/log"
)

// Unlike pages' resources, these are fetched at start whatever page shows,
// and again on sign-in.
var (
	AccountRes = fetchstate.Resource{Kind: fetchstate.Account}
	LibraryRes = fetchstate.Resource{Kind: fetchstate.Library}
	LikedRes   = fetchstate.Resource{Kind: fetchstate.Liked}
)

// Resources are the account's resources, in the order Start fetches them.
var Resources = [...]fetchstate.Resource{AccountRes, LibraryRes, LikedRes}

// Session is the signed-in account, or none, and what it has.
type Session struct {
	src catalog.Source
	// accounts is src as catalog.Accounts, or nil. CanSignIn runs every
	// build, and asserting an interface there allocated now and then (the
	// runtime building its assertion cache).
	accounts catalog.Accounts
	states   *fetchstate.Registry
	loader   *loader.Loader
	cat      *catalog.Catalog
	post     func(func())

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
	// fetchJobs is indexed like Resources.
	fetchJobs [len(Resources)]job.Job
	// pending holds unanswered likes by song, so they survive a Liked
	// music fetch that crossed them; seq picks a song's latest. An account
	// change clears them so one account's likes never change the next's.
	pending     map[string]pendingLike
	likeSeq     uint64
	likeCtx     context.Context
	cancelLikes context.CancelFunc

	// signInCtx identifies the sign-in in flight: only its failure is told.
	signInCtx  context.Context
	stopSignIn context.CancelFunc
	signOutJob job.Job

	notices []notice.Notice
}

type pendingLike struct {
	liked bool
	seq   uint64
}

// New returns a Session over src that records fetches in states, has load
// refetch Home when the account changes, and adds music to cat. post runs
// a func on the UI thread; it is first called after Start, SetLiked,
// SignIn or SignOut.
func New(
	src catalog.Source,
	states *fetchstate.Registry,
	load *loader.Loader,
	cat *catalog.Catalog,
	post func(func()),
) *Session {
	ctx, cancel := context.WithCancel(context.Background())
	accounts, _ := src.(catalog.Accounts)
	return &Session{
		src:         src,
		accounts:    accounts,
		states:      states,
		loader:      load,
		cat:         cat,
		post:        post,
		liked:       map[string]bool{},
		pending:     map[string]pendingLike{},
		likeCtx:     ctx,
		cancelLikes: cancel,
	}
}

// Start fetches the Resources never fetched yet, once the app can take
// results. Later calls cost nothing.
func (s *Session) Start() {
	for _, res := range Resources {
		if s.states.State(res).State == fetchstate.Idle {
			s.fetch(res)
		}
	}
}

// Retry fetches res again, and the account too if it failed, since the
// account has no Retry of its own in the UI.
func (s *Session) Retry(res fetchstate.Resource) {
	s.fetch(res)
	if s.states.State(AccountRes).State == fetchstate.Failed {
		s.fetch(AccountRes)
	}
}

func (s *Session) fetch(res fetchstate.Resource) {
	s.states.Set(res, fetchstate.Fetch{State: fetchstate.Loading})
	src := s.src
	job.Run(&s.fetchJobs[slices.Index(Resources[:], res)], s.post,
		func(ctx context.Context) (func(*Session), error) { return fetchResource(ctx, src, res) },
		func(keep func(*Session), err error) { s.finish(res, keep, err) })
}

// fetchResource runs off the UI thread; keep stores the result on it.
func fetchResource(
	ctx context.Context,
	src catalog.Source,
	res fetchstate.Resource,
) (keep func(*Session), err error) {
	switch res.Kind {
	case fetchstate.Account:
		account, err := src.Account(ctx)
		return func(s *Session) { s.setAccount(account) }, err
	case fetchstate.Library:
		lib, err := src.Library(ctx)
		return func(s *Session) { s.setLibrary(lib) }, err
	default:
		return fetchLiked(ctx, src)
	}
}

func fetchLiked(ctx context.Context, src catalog.Source) (keep func(*Session), err error) {
	playlist, songs, err := src.Playlist(ctx, catalog.LikedID)
	// An account that never liked a song may have no such playlist.
	if errors.Is(err, catalog.ErrNotFound) {
		playlist, songs, err = catalog.Playlist{}, nil, nil
	}
	ids := playlist.Songs
	if len(ids) == 0 {
		ids = make([]string, len(songs))
		for i, song := range songs {
			ids[i] = song.ID
		}
	}
	ids = loader.Unique(ids, func(id string) string { return id })
	return func(s *Session) {
		s.cat.Add(songs, nil, nil)
		s.seedLikes(ids)
	}, err
}

func (s *Session) finish(res fetchstate.Resource, keep func(*Session), err error) {
	switch {
	case errors.Is(err, catalog.ErrSignedOut):
		log.Debug().Stringer("kind", res.Kind).Err(err).Msg("signed out")
		s.forget()
	case err != nil:
		log.Debug().Stringer("kind", res.Kind).Err(err).Msg("fetch failed")
		s.states.Set(res, fetchstate.Fetch{State: fetchstate.Failed, Err: err})
	default:
		keep(s)
		s.states.Set(res, fetchstate.Fetch{State: fetchstate.Loaded})
	}
}

// KeepAccount stores account and lib as if fetched.
func (s *Session) KeepAccount(account catalog.Account, lib catalog.Library) {
	s.setAccount(account)
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

func (s *Session) setAccount(account catalog.Account) {
	s.isSignedIn, s.account, s.displayName = true, account, account.Name
	if account.Handle != "" {
		s.displayName = account.Name + " (" + account.Handle + ")"
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

// SetLiked likes or unlikes song id at once, then sends it off the UI
// thread; on failure it is undone and a notice tells why.
func (s *Session) SetLiked(id string, liked bool) {
	if id == "" || s.Liked(id) == liked {
		return
	}
	if s.states.State(AccountRes).State == fetchstate.SignedOut {
		s.notify(signInToLike)
		return
	}
	s.applyLike(id, liked)
	s.likeSeq++
	seq := s.likeSeq
	s.pending[id] = pendingLike{liked: liked, seq: seq}
	ctx, src, post := s.likeCtx, s.src, s.post
	go func() {
		err := src.Like(ctx, id, liked)
		if ctx.Err() != nil {
			return
		}
		post(func() { s.finishLike(id, liked, seq, err) }) //nolint:contextcheck // runs on the UI thread, past this context, and may start work of its own
	}()
}

func (s *Session) finishLike(id string, liked bool, seq uint64, err error) {
	// The account changed, or a later click on the same song decides.
	if like, ok := s.pending[id]; !ok || like.seq != seq {
		return
	}
	delete(s.pending, id)
	if err == nil {
		return
	}
	log.Debug().Str("song", id).Bool("liked", liked).Err(err).Msg("like failed")
	if errors.Is(err, catalog.ErrSignedOut) {
		s.forget()
		s.notify(signInToLike)
		return
	}
	s.applyLike(id, !liked)
	s.notify(notice.Notice{Kind: notice.Failure, Title: s.likeFailedTitle(id, liked), Detail: err.Error()})
}

func (s *Session) likeFailedTitle(id string, liked bool) string {
	title := "Couldn’t like "
	if !liked {
		title = "Couldn’t unlike "
	}
	song, ok := s.cat.Song(id)
	if !ok {
		return title + "the song"
	}
	return title + "“" + song.Title + "”"
}

func (s *Session) notify(n notice.Notice) { s.notices = append(s.notices, n) }

var signInToLike = notice.Notice{Kind: notice.NeedsAccount, Title: "Sign in to like songs", Topic: notice.SignIn}

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
	for i, res := range Resources {
		s.fetchJobs[i].Stop()
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
func (s *Session) CanSignIn() bool { return s.accounts != nil }

// SignIn sends credentials, as pasted, off the UI thread, canceling a
// sign-in in flight; the caller should forget them. The result comes as a
// SignedIn or SignInFailed notice.
func (s *Session) SignIn(credentials string) {
	accounts := s.accounts
	creds := CleanCredentials(credentials)
	if accounts == nil || creds == "" {
		return
	}
	s.CancelSignIn()
	ctx, cancel := context.WithCancel(context.Background())
	s.signInCtx, s.stopSignIn = ctx, cancel
	post := s.post
	go func() {
		account, err := accounts.SignIn(ctx, creds)
		// Credentials stored despite a cancellation still sign in.
		if ctx.Err() != nil && err != nil {
			return
		}
		post(func() { s.finishSignIn(ctx == s.signInCtx, account, err) }) //nolint:contextcheck // runs on the UI thread, past this context, and may start work of its own
	}()
}

// CleanCredentials normalizes a pasted Cookie header, with or without its
// name, on one line or several.
func CleanCredentials(pasted string) string {
	const name = "cookie:"
	cleaned := collapseSpace(pasted)
	if len(cleaned) >= len(name) && strings.EqualFold(cleaned[:len(name)], name) {
		cleaned = strings.TrimSpace(cleaned[len(name):])
	}
	return cleaned
}

// collapseSpace is strings.Join(strings.Fields(s), " ") without its 2
// allocations of the pasted size (BenchmarkCleanCredentials): a header
// with no space to collapse comes back as a part of s.
func collapseSpace(s string) string {
	s = strings.TrimSpace(s)
	i := extraSpace(s)
	if i < 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	// s is trimmed, so what comes before its first extra space is a field
	// and maybe the one ' ' after it.
	b.WriteString(strings.TrimSuffix(s[:i], " "))
	start := -1
	for i < len(s) {
		c, size := s[i], 1
		isSpace := c < utf8.RuneSelf && asciiSpace[c]
		if c >= utf8.RuneSelf {
			isSpace, size = wideSpace(s[i:])
		}
		switch {
		case !isSpace:
			if start < 0 {
				start = i
			}
		case start >= 0:
			b.WriteByte(' ')
			b.WriteString(s[start:i])
			start = -1
		}
		i += size
	}
	if start >= 0 {
		b.WriteByte(' ')
		b.WriteString(s[start:])
	}
	return b.String()
}

// extraSpace is the index of the first space in s other than one ' '
// after a field, -1 if none.
func extraSpace(s string) int {
	isAfterSpace := false
	for i := 0; i < len(s); {
		c, size := s[i], 1
		isSpace := c < utf8.RuneSelf && asciiSpace[c]
		if c >= utf8.RuneSelf {
			isSpace, size = wideSpace(s[i:])
		}
		if isSpace && (c != ' ' || isAfterSpace) {
			return i
		}
		isAfterSpace = isSpace
		i += size
	}
	return -1
}

var asciiSpace = [utf8.RuneSelf]bool{'\t': true, '\n': true, '\v': true, '\f': true, '\r': true, ' ': true}

// wideSpace reports whether s starts with a non-ASCII space, as
// strings.Fields tells, and the rune's size. Kept out of the loops, whose
// ASCII test, nearly all of a header, then stays inline.
func wideSpace(s string) (isSpace bool, size int) {
	r, size := utf8.DecodeRuneInString(s)
	return unicode.IsSpace(r), size
}

// finishSignIn keeps a sign-in's result; isCurrent reports whether it is the
// latest sign-in.
func (s *Session) finishSignIn(isCurrent bool, account catalog.Account, err error) {
	if isCurrent {
		s.CancelSignIn()
	}
	if err != nil {
		// Sources keep credentials out of their errors (see auth).
		log.Debug().Bool("current", isCurrent).Err(err).Msg("sign-in failed")
		if isCurrent {
			s.notify(notice.Notice{Kind: notice.SignInFailed, Title: "Couldn’t sign in", Detail: err.Error()})
		}
		return
	}
	s.setAccount(account)
	s.clearLibrary()
	for _, res := range Resources {
		s.fetch(res)
	}
	s.loader.Forget(loader.HomeFeed)
	s.notify(notice.Notice{Kind: notice.SignedIn, Title: "Signed in as " + account.Name})
}

// IsSigningIn reports whether a sign-in is in flight.
func (s *Session) IsSigningIn() bool { return s.signInCtx != nil }

// CancelSignIn gives up the sign-in in flight; its failure is never told.
func (s *Session) CancelSignIn() {
	if s.stopSignIn == nil {
		return
	}
	s.stopSignIn()
	s.signInCtx, s.stopSignIn = nil, nil
}

// SignOut clears the stored credentials off the UI thread, then forgets
// the account.
func (s *Session) SignOut() {
	accounts := s.accounts
	if accounts == nil || s.signOutJob.IsRunning() {
		return
	}
	job.Run(&s.signOutJob, s.post,
		func(context.Context) (struct{}, error) { return struct{}{}, accounts.SignOut() },
		func(_ struct{}, err error) { s.finishSignOut(err) })
}

func (s *Session) finishSignOut(err error) {
	if err != nil {
		log.Debug().Err(err).Msg("sign-out failed")
		s.notify(notice.Notice{Kind: notice.Failure, Title: "Couldn’t sign out", Detail: err.Error()})
		return
	}
	s.CancelSignIn()
	s.forget()
	s.notify(notice.Notice{Kind: notice.Info, Title: "Signed out"})
}

// IsSigningOut reports whether a sign-out is in flight.
func (s *Session) IsSigningOut() bool { return s.signOutJob.IsRunning() }

// IsBusy reports whether a fetch, a like, or a sign-in or sign-out is in flight.
func (s *Session) IsBusy() bool {
	for i := range s.fetchJobs {
		if s.fetchJobs[i].IsRunning() {
			return true
		}
	}
	return len(s.pending) > 0 || s.IsSigningIn() || s.signOutJob.IsRunning()
}

// Stop cancels all work in flight, for when the window closes.
func (s *Session) Stop() {
	for i := range s.fetchJobs {
		s.fetchJobs[i].Stop()
	}
	s.signOutJob.Stop()
	s.CancelSignIn()
	s.cancelLikes()
}
