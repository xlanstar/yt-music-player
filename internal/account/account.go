// Package account signs in and out through catalog.Accounts, fetches who
// is signed in, and sends likes, reporting those that went through. It
// holds no UI state, so that a process without a window can own it;
// package session keeps the library and likes, which front-ends fetch.
//
// Any call answering catalog.ErrSignedOut signs out, and so does any
// other request with the same credentials (Answered): the fetch stops,
// the likes in flight change nothing, and a Status says no one is signed
// in.
//
// An Account belongs to the thread post runs funcs on. Its goroutines call
// only the Source; results come back through post, and what changed is
// reported on that thread as a Change. It keeps the latest Status and
// failure for front-ends that connect later.
package account

import (
	"context"
	"errors"
	"yt-music-player/internal/api"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/job"

	"github.com/rs/zerolog/log"
)

// Change is what an Account reports: exactly one field is set.
type Change struct {
	Status *Status
	Likes  *Likes
	// Failed is why who is signed in could not be fetched, for another
	// reason than being signed out.
	Failed error
}

// Status is who is signed in, if anyone.
type Status struct {
	Account  catalog.Account
	SignedIn bool
	// Switched reports a sign-in: what was fetched with the previous
	// credentials is stale.
	Switched bool
}

// Likes are likes that went through: Songs describe those of Added whose
// Like described them.
type Likes struct {
	Songs          []catalog.Song
	Added, Removed []string
}

// Account is the credentials the Source holds, and who they sign in.
type Account struct {
	src      catalog.Source
	accounts catalog.Accounts // src as catalog.Accounts, or nil
	post     func(func())
	report   func(Change)

	fetchJob job.Job
	// liking maps songs to their latest likeSeq; stale replies change nothing.
	// Credential changes clear it so old likes cannot affect the new account.
	liking      map[string]uint64
	likeSeq     uint64
	likeCtx     context.Context
	cancelLikes context.CancelFunc
	// gen counts the credentials' changes (Gen).
	gen uint64

	// signInCtx identifies the sign-in in flight.
	signInCtx  context.Context
	stopSignIn context.CancelFunc
	signOutJob job.Job

	// status is the latest reported, hasStatus whether there is one;
	// failed is the latest fetch's failure, nil once one went through.
	status    Status
	failed    error
	hasStatus bool
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

// Fetch fetches who is signed in off the thread, dropping a fetch in
// flight, and reports it or its failure.
func (a *Account) Fetch() {
	src := a.src
	a.fetchJob.Run(a.post, src.Account, a.finishFetch)
}

func (a *Account) finishFetch(account catalog.Account, err error) {
	switch {
	case errors.Is(err, catalog.ErrSignedOut):
		log.Debug().Err(err).Msg("signed out")
		a.signedOut()
	case err != nil:
		log.Debug().Err(err).Msg("fetching the account failed")
		a.failed = err
		a.report(Change{Failed: err})
	default:
		a.tell(Status{Account: account, SignedIn: true})
	}
}

// IsFetching reports whether a fetch of who is signed in is in flight.
func (a *Account) IsFetching() bool { return a.fetchJob.IsRunning() }

// Gen changes whenever the credentials do: a request's answer counts
// (Answered) only with the credentials it was sent with.
func (a *Account) Gen() uint64 { return a.gen }

// Answered signs out if err, the Source's answer to a request sent while
// Gen was gen, is catalog.ErrSignedOut, as the Account's own requests do:
// a front-end's fetch of the library or likes, say.
func (a *Account) Answered(gen uint64, err error) {
	if gen != a.gen || !errors.Is(err, catalog.ErrSignedOut) || a.hasStatus && !a.status.SignedIn {
		return
	}
	log.Debug().Err(err).Msg("signed out")
	a.signedOut()
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
		a.report(Change{Likes: &Likes{Added: []string{id}, Songs: []catalog.Song{*song}}})
	case err == nil && liked:
		a.report(Change{Likes: &Likes{Added: []string{id}}})
	case err == nil:
		a.report(Change{Likes: &Likes{Removed: []string{id}}})
	}
}

// switchCredentials drops what the credentials in use had in flight.
func (a *Account) switchCredentials() {
	a.gen++
	a.fetchJob.Stop()
	clear(a.liking)
}

func (a *Account) signedOut() {
	a.switchCredentials()
	a.tell(Status{})
}

// tell keeps st as the latest, without Switched, then reports it.
func (a *Account) tell(st Status) {
	a.status, a.hasStatus, a.failed = st, true, nil
	a.status.Switched = false
	a.report(Change{Status: &st})
}

// Status returns the latest Status reported, without Switched, if any.
func (a *Account) Status() (Status, bool) { return a.status, a.hasStatus }

// Failed returns why the latest fetch of who is signed in failed, nil if
// it went through.
func (a *Account) Failed() error { return a.failed }

// CanSignIn reports whether the source signs in and out at all.
func (a *Account) CanSignIn() bool { return a.accounts != nil }

// SignIn sends credentials, as pasted, off the thread, canceling one in
// flight; the caller should forget them. done gets the account, after its
// Status, or why not; a canceled sign-in never fails, though one whose
// credentials were stored anyway still signs in.
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
		post(func() { a.finishSignIn(ctx == a.signInCtx, account, err, done) })
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
	// The sign-in fetched who it signs in.
	a.switchCredentials()
	a.tell(Status{Account: account, SignedIn: true, Switched: true})
	done(account, nil)
}

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
	a.signOutJob.Run(a.post,
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

// Stop cancels all work in flight, whose results then never come.
func (a *Account) Stop() {
	a.fetchJob.Stop()
	a.signOutJob.Stop()
	a.CancelSignIn()
	a.cancelLikes()
	clear(a.liking)
}
