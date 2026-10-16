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
// reported on that thread as a Change.
package account

import (
	"context"
	"errors"
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
// otherwise Added and Removed are likes that went through since.
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
		a.report(Change{Failed: &Failure{Part: p, Err: err}})
	default:
		a.report(c)
	}
}

// Like likes or unlikes song id off the thread; done gets the Source's
// answer. If it is the latest like sent for the song and went through,
// Likes reports it after done.
func (a *Account) Like(id string, liked bool, done func(error)) {
	a.likeSeq++
	seq := a.likeSeq
	a.liking[id] = seq
	ctx, src, post := a.likeCtx, a.src, a.post
	go func() {
		err := src.Like(ctx, id, liked)
		if ctx.Err() != nil {
			return
		}
		post(func() { a.finishLike(id, liked, seq, err, done) })
	}()
}

func (a *Account) finishLike(id string, liked bool, seq uint64, err error, done func(error)) {
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
	case err == nil && liked:
		a.report(Change{Likes: &Likes{Added: []string{id}}})
	case err == nil:
		a.report(Change{Likes: &Likes{Removed: []string{id}}})
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
	a.report(Change{Status: &Status{}})
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
	creds := CleanCredentials(credentials)
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
	a.report(Change{Status: &Status{Account: account, SignedIn: true, Switched: true}})
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
