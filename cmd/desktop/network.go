package main

import "github.com/egoist/mygo"

// followNetwork retries what failed once the computer is online again, so
// a page that failed while offline loads without a click on Retry. It
// returns the func that stops following.
func (a *app) followNetwork() (stop func()) {
	return mygo.Network.OnChanged(func(status mygo.NetworkStatus) {
		if status.Online {
			a.post(a.reconnected)
		}
	})
}

// reconnected connects to ytmd at once if it is away, and retries what
// failed.
func (a *app) reconnected() {
	if a.sup != nil {
		a.sup.Retry()
	}
	a.retryFailed()
}

// retryFailed retries the shown page, the search and the account's
// resources that failed; what loaded stays.
func (a *app) retryFailed() {
	a.loader.RetryFailed()
	a.session.RetryFailed()
}
