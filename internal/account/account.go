// Package account signs in and out through catalog.Accounts, fetches who
// is signed in, their library and their liked songs, and sends likes. It
// holds no UI state, so that a process without a window can own it;
// package session keeps what it reports for the pages.
//
// Any call answering catalog.ErrSignedOut signs out: the fetches stop, the
// likes in flight change nothing, and a Status says no one is signed in.
//
// An Account belongs to the thread post runs funcs on. Its goroutines call
// only the Source; results come back through post, and what changed is
// reported on that thread as a Change. It keeps the latest of each, for a
// daemon to tell the front-ends that connect later (Latest).
package account

import (
	"context"
	"errors"
	"slices"
	"yt-music-player/internal/api"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/job"

	"github.com/rs/zerolog/log"
)

// Part is one thing an Account fetches.
type Part uint8

// The Parts, in the order a sign-in fetches them.
const (
	Profile Part = iota // who is signed in
	Library
	Liked // the songs in Liked music
)

var partNames = [...]string{Profile: "profile", Library: "library", Liked: "liked"}

func (p Part) String() string { return partNames[p] }

// Change is what an Account reports: exactly one field is set.
type Change struct {
	Status  *Status
	Library *catalog.Library
	Likes   *Likes
	Failed  *Failure
}

// Status is who is signed in, if anyone.
type Status struct {
	Account  catalog.Account
	SignedIn bool
	// Switched reports a sign-in: what was fetched with the previous
	// credentials is stale, and every Part is being fetched anew.
	Switched bool
}

// Likes changes the liked songs. With Full, IDs are all of them, newest
// first and maybe repeated, and Songs those Liked music described;
// otherwise Added and Removed are likes that went through since, and Songs
// describe those of Added whose Like described them.
type Likes struct {
	Full           bool
	IDs            []string
	Songs          []catalog.Song
	Added, Removed []string
}

// Failure is a fetch that failed for another reason than being signed out.
type Failure struct {
	Part Part
	Err  error
}

// Account is the credentials the Source holds, and what they fetch.
type Account struct {
	src catalog.Source
	// accounts is src as catalog.Accounts, or nil. CanSignIn runs every
	// build, and asserting an interface there allocated now and then (the
	// runtime building its assertion cache).
	accounts catalog.Accounts
	post     func(func())
	report   func(Change)

	fetches [Liked + 1]job.Job
	// liking holds the latest like sent for each song until it is
	// answered; seq tells it from earlier ones, whose answers report no
	// change: the latest decides. A sign-in or sign-out clears it, so the
	// previous credentials' likes change nothing.
	liking      map[string]uint64
	likeSeq     uint64
	likeCtx     context.Context
	cancelLikes context.CancelFunc

	// signInCtx identifies the sign-in in flight.
	signInCtx  context.Context
	stopSignIn context.CancelFunc
	signOutJob job.Job

	// The latest reported: status once hasStatus, library once fetched,
	// likes once hasLikes (likedSongs replaced whole, never edited), and
	// the failure of each Part whose last fetch failed.
	status     Status
	hasStatus  bool
	library    *catalog.Library
	likedIDs   []string
	likedSongs []catalog.Song
	hasLikes   bool
	failed     [Liked + 1]*Failure
}

// Latest is the latest an Account reported of each Part, as a full
// Change would carry it; nil while not known. Library and Likes.Songs are
// the Account's: read them, never modify them.
type Latest struct {
	// Status never has Switched.
	Status  *Status
	Library *catalog.Library
	// Likes are Full, the liked songs since the last fetch included; Songs
	// describe those of the fetch, and those liked since whose Like
	// described them.
	Likes *Likes
	// Failed are the Parts whose last fetch failed.
	Failed []Failure
}

// New returns an Account over src. post runs a func on the Account's
// thread; report gets each change there. post is first called after Fetch,
// Like, SignIn or SignOut.
func New(src catalog.Source, post func(func()), report func(Change)) *Account {
	ctx, cancel := context.WithCancel(context.Background())
	accounts, _ := src.(catalog.Accounts)
	return &Account{
		src:         src,
		accounts:    accounts,
		post:        post,
		report:      report,
		liking:      map[string]uint64{},
		likeCtx:     ctx,
		cancelLikes: cancel,
	}
}

// Fetch fetches p off the thread, dropping a fetch of p in flight, and
// reports it or its failure.
func (a *Account) Fetch(p Part) {
	src := a.src
	job.Run(&a.fetches[p], a.post,
		func(ctx context.Context) (Change, error) { return fetch(ctx, src, p) },
		func(c Change, err error) { a.finishFetch(p, c, err) })
}

func fetch(ctx context.Context, src catalog.Source, p Part) (Change, error) {
	switch p {
	case Profile:
		account, err := src.Account(ctx)
		return Change{Status: &Status{Account: account, SignedIn: true}}, err
	case Library:
		lib, err := src.Library(ctx)
		return Change{Library: &lib}, err
	default:
		likes, err := fetchLikes(ctx, src)
		return Change{Likes: &likes}, err
	}
}

func fetchLikes(ctx context.Context, src catalog.Source) (Likes, error) {
	playlist, songs, err := src.Playlist(ctx, catalog.LikedID)
	switch {
	// An account that never liked a song may have no such playlist.
	case errors.Is(err, catalog.ErrNotFound):
		return Likes{Full: true}, nil
	case err != nil:
		return Likes{}, err
	}
	ids := playlist.Songs
	if len(ids) == 0 {
		ids = make([]string, len(songs))
		for i, song := range songs {
			ids[i] = song.ID
		}
	}
	return Likes{Full: true, IDs: ids, Songs: songs}, nil
}

func (a *Account) finishFetch(p Part, c Change, err error) {
	switch {
	case errors.Is(err, catalog.ErrSignedOut):
		log.Debug().Stringer("part", p).Err(err).Msg("signed out")
		a.signedOut()
	case err != nil:
		log.Debug().Stringer("part", p).Err(err).Msg("fetch failed")
		a.tell(Change{Failed: &Failure{Part: p, Err: err}})
	default:
		a.tell(c)
	}
}

// Like likes or unlikes song id off the thread; done gets the Source's
// answer. If it is the latest like sent for the song and went through,
// Likes reports it after done, with song when liked: what the caller knows
// of it, or nil.
func (a *Account) Like(id string, liked bool, song *catalog.Song, done func(error)) {
	a.likeSeq++
	seq := a.likeSeq
	a.liking[id] = seq
	ctx, src, post := a.likeCtx, a.src, a.post
	go func() {
		err := src.Like(ctx, id, liked)
		if ctx.Err() != nil {
			return
		}
		post(func() { a.finishLike(id, liked, song, seq, err, done) })
	}()
}

func (a *Account) finishLike(id string, liked bool, song *catalog.Song, seq uint64, err error, done func(error)) {
	isLatest := a.liking[id] == seq
	if isLatest {
		delete(a.liking, id)
	}
	if err != nil {
		log.Debug().Str("song", id).Bool("liked", liked).Bool("latest", isLatest).Err(err).Msg("like failed")
	}
	// Before the change it causes, so the caller still knows the like.
	done(err)
	if !isLatest {
		return
	}
	switch {
	case errors.Is(err, catalog.ErrSignedOut):
		a.signedOut()
	case err == nil && liked && song != nil:
		a.tell(Change{Likes: &Likes{Added: []string{id}, Songs: []catalog.Song{*song}}})
	case err == nil && liked:
		a.tell(Change{Likes: &Likes{Added: []string{id}}})
	case err == nil:
		a.tell(Change{Likes: &Likes{Removed: []string{id}}})
	}
}

// switchCredentials drops what the credentials in use had in flight.
func (a *Account) switchCredentials() {
	for i := range a.fetches {
		a.fetches[i].Stop()
	}
	clear(a.liking)
}

func (a *Account) signedOut() {
	a.switchCredentials()
	a.tell(Change{Status: &Status{}})
}

// tell keeps c as the latest, then reports it.
func (a *Account) tell(c Change) {
	a.keep(c)
	a.report(c)
}

func (a *Account) keep(c Change) {
	switch {
	case c.Status != nil:
		a.status, a.hasStatus = *c.Status, true
		a.status.Switched = false
		if !c.Status.SignedIn || c.Status.Switched {
			// Another account's, or no one's: what it had is gone.
			a.library, a.likedIDs, a.likedSongs, a.hasLikes = nil, nil, nil, false
			a.failed = [len(a.failed)]*Failure{}
		}
		a.failed[Profile] = nil
	case c.Library != nil:
		a.library, a.failed[Library] = c.Library, nil
	case c.Likes != nil && c.Likes.Full:
		// The Change's IDs go on to others; the Account edits its own.
		a.likedIDs, a.likedSongs, a.hasLikes = slices.Clone(c.Likes.IDs), c.Likes.Songs, true
		a.failed[Liked] = nil
	case c.Likes != nil && a.hasLikes:
		for _, id := range c.Likes.Added {
			a.likedIDs = slices.Insert(slices.DeleteFunc(a.likedIDs, isID(id)), 0, id)
		}
		for _, id := range c.Likes.Removed {
			a.likedIDs = slices.DeleteFunc(a.likedIDs, isID(id))
		}
		if len(c.Likes.Songs) > 0 {
			a.likedSongs = described(c.Likes.Songs, a.likedSongs)
		}
	case c.Failed != nil:
		a.failed[c.Failed.Part] = c.Failed
	}
}

func isID(id string) func(string) bool { return func(s string) bool { return s == id } }

// described returns songs, then those of old they do not describe anew, in
// a new slice: Latest handed old out.
func described(songs, old []catalog.Song) []catalog.Song {
	out := make([]catalog.Song, 0, len(songs)+len(old))
	out = append(out, songs...)
	for _, song := range old {
		if !slices.ContainsFunc(songs, func(s catalog.Song) bool { return s.ID == song.ID }) {
			out = append(out, song)
		}
	}
	return out
}

// Latest returns the latest reported of each Part.
func (a *Account) Latest() Latest {
	var l Latest
	if a.hasStatus {
		status := a.status
		l.Status = &status
	}
	l.Library = a.library
	if a.hasLikes {
		l.Likes = &Likes{Full: true, IDs: slices.Clone(a.likedIDs), Songs: a.likedSongs}
	}
	for _, f := range a.failed {
		if f != nil {
			l.Failed = append(l.Failed, *f)
		}
	}
	return l
}

// CanSignIn reports whether the source signs in and out at all.
func (a *Account) CanSignIn() bool { return a.accounts != nil }

// SignIn sends credentials, as pasted, off the thread, canceling a sign-in
// in flight; the caller should forget them. done gets the account signed
// in or why not, though never the failure of a sign-in canceled: one whose
// credentials were stored anyway still signs in. A Status reports the
// sign-in before done.
func (a *Account) SignIn(credentials string, done func(catalog.Account, error)) {
	accounts := a.accounts
	creds := api.CleanCredentials(credentials)
	if accounts == nil || creds == "" {
		return
	}
	a.CancelSignIn()
	ctx, cancel := context.WithCancel(context.Background())
	a.signInCtx, a.stopSignIn = ctx, cancel
	post := a.post
	go func() {
		account, err := accounts.SignIn(ctx, creds)
		if ctx.Err() != nil && err != nil {
			return
		}
		post(func() { a.finishSignIn(ctx == a.signInCtx, account, err, done) }) //nolint:contextcheck // runs on the Account's thread, past this context, and may start work of its own
	}()
}

// finishSignIn keeps a sign-in's result; isCurrent reports whether it is
// the latest sign-in.
func (a *Account) finishSignIn(isCurrent bool, account catalog.Account, err error, done func(catalog.Account, error)) {
	if isCurrent {
		a.CancelSignIn()
	}
	if err != nil {
		// Sources keep credentials out of their errors (see auth).
		log.Debug().Err(err).Msg("sign-in failed")
		done(catalog.Account{}, err)
		return
	}
	a.switchCredentials()
	a.tell(Change{Status: &Status{Account: account, SignedIn: true, Switched: true}})
	for p := range a.fetches {
		a.Fetch(Part(p))
	}
	done(account, nil)
}

// IsSigningIn reports whether a sign-in is in flight.
func (a *Account) IsSigningIn() bool { return a.signInCtx != nil }

// CancelSignIn gives up the sign-in in flight; its failure never comes.
func (a *Account) CancelSignIn() {
	if a.stopSignIn == nil {
		return
	}
	a.stopSignIn()
	a.signInCtx, a.stopSignIn = nil, nil
}

// SignOut clears the stored credentials off the thread, unless a sign-out
// is in flight; done gets why it failed, or nil after a Status reported
// that no one is signed in.
func (a *Account) SignOut(done func(error)) {
	accounts := a.accounts
	if accounts == nil || a.signOutJob.IsRunning() {
		return
	}
	job.Run(&a.signOutJob, a.post,
		func(context.Context) (struct{}, error) { return struct{}{}, accounts.SignOut() },
		func(_ struct{}, err error) { a.finishSignOut(err, done) })
}

func (a *Account) finishSignOut(err error, done func(error)) {
	if err != nil {
		log.Debug().Err(err).Msg("sign-out failed")
		done(err)
		return
	}
	a.CancelSignIn()
	a.signedOut()
	done(nil)
}

// IsSigningOut reports whether a sign-out is in flight.
func (a *Account) IsSigningOut() bool { return a.signOutJob.IsRunning() }

// IsBusy reports whether a fetch, a like, or a sign-in or sign-out is in
// flight.
func (a *Account) IsBusy() bool {
	for i := range a.fetches {
		if a.fetches[i].IsRunning() {
			return true
		}
	}
	return len(a.liking) > 0 || a.IsSigningIn() || a.signOutJob.IsRunning()
}

// Stop cancels all work in flight, whose results then never come.
func (a *Account) Stop() {
	for i := range a.fetches {
		a.fetches[i].Stop()
	}
	a.signOutJob.Stop()
	a.CancelSignIn()
	a.cancelLikes()
	clear(a.liking)
}
