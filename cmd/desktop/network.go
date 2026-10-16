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

// reconnected retries the shown page, the search and the account's
// resources that failed; what loaded stays.
func (a *app) reconnected() {
	a.loader.RetryFailed()
	a.session.RetryFailed()
}
