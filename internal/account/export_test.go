package account

// Only tests ask these: front-ends read the session's, which it keeps from
// the Changes.

// IsSigningIn reports whether a sign-in is in flight.
func (a *Account) IsSigningIn() bool { return a.signInCtx != nil }

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
