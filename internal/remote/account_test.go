package remote

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"yt-music-player/internal/api"
	"yt-music-player/internal/catalog"
)

// accountFixture is an Account over a Client attached to a fake daemon
// that signs in "SID=x" as Lan, answers "SID=late" never, signs out no
// one, and gives anything else a reply without its result.
type accountFixture struct {
	d *daemon
	f *front
	a *Account
}

func newAccountFixture(t *testing.T) *accountFixture {
	t.Helper()
	d := newDaemon("1.0.0")
	d.answer = func(c *api.Call) *api.Reply {
		switch {
		case c.Method == api.MethodSignOut:
			return &api.Reply{Err: &api.Error{Code: api.CodeSignedOut}}
		case c.Method != api.MethodSignIn:
			return &api.Reply{}
		case c.SignIn.Credentials == "SID=x":
			return &api.Reply{Account: &catalog.Account{Name: "Lan"}}
		case c.SignIn.Credentials == "SID=late":
			return nil
		}
		return &api.Reply{}
	}
	f := newFront()
	attach(t, f, d)
	return &accountFixture{d: d, f: f, a: NewAccount(f.client)}
}

// settle runs the answers' done funcs on the front-end's thread.
func (af *accountFixture) settle() {
	synctest.Wait()
	af.f.ui.run()
}

func TestAccountRequests(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		af := newAccountFixture(t)
		var errs []error
		keep := func(err error) { errs = append(errs, err) }
		song := &catalog.Song{ID: "a", Title: "A"}
		af.a.Refresh(keep)
		af.a.Like("a", true, song, keep)
		af.a.SignOut(keep)
		af.settle()
		if len(errs) != 3 || errs[0] != nil || errs[1] != nil || !errors.Is(errs[2], catalog.ErrSignedOut) {
			t.Errorf("done got %v, want nil, nil, ErrSignedOut", errs)
		}
		calls := af.d.received()
		if len(calls) != 3 {
			t.Fatalf("daemon got %d frames, want 3", len(calls))
		}
		if c := calls[0].Call; c.Method != api.MethodRefresh {
			t.Errorf("Refresh sent %+v", c)
		}
		if c := calls[1].Call; c.Method != api.MethodLike || c.Like.ID != "a" || !c.Like.Liked || c.Like.Song.Title != "A" {
			t.Errorf("Like sent %+v", c)
		}
		if c := calls[2].Call; c.Method != api.MethodSignOut {
			t.Errorf("SignOut sent %+v", c)
		}
	})
}

func TestAccountSignIn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		af := newAccountFixture(t)
		var accounts []catalog.Account
		var errs []error
		keep := func(acct catalog.Account, err error) {
			accounts, errs = append(accounts, acct), append(errs, err)
		}
		af.a.SignIn("SID=x", keep)
		af.settle()
		af.a.SignIn("nothing", keep)
		af.settle()
		if len(errs) != 2 || errs[0] != nil || accounts[0].Name != "Lan" {
			t.Fatalf("SignIn got %+v, %v; want Lan", accounts, errs)
		}
		// A reply without the account it should hold.
		if !errors.Is(errs[1], api.ErrInvalid) || accounts[1] != (catalog.Account{}) {
			t.Errorf("SignIn without an account got %+v, %v", accounts[1], errs[1])
		}
	})
}

// A sign-in canceled, or replaced by another, is canceled in the daemon
// too.
func TestAccountCancelSignIn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		af := newAccountFixture(t)
		var errs []error
		keep := func(_ catalog.Account, err error) { errs = append(errs, err) }
		af.a.SignIn("SID=late", keep)
		synctest.Wait()
		af.a.SignIn("SID=late", keep)
		synctest.Wait()
		af.a.CancelSignIn()
		af.a.CancelSignIn()
		af.settle()
		if len(errs) != 2 || !errors.Is(errs[0], context.Canceled) || !errors.Is(errs[1], context.Canceled) {
			t.Errorf("done got %v, want two context.Canceled", errs)
		}
		var canceled []uint32
		for _, fr := range af.d.received() {
			if fr.Cancel {
				canceled = append(canceled, fr.ID)
			}
		}
		if len(canceled) != 2 || canceled[0] != 1 || canceled[1] != 2 {
			t.Errorf("daemon canceled calls %v, want [1 2]", canceled)
		}
	})
}
