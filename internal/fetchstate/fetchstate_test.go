package fetchstate

import (
	"errors"
	"testing"
)

func TestSetAndForget(t *testing.T) {
	var r Registry
	acct := Resource{Kind: Account}
	if got := r.State(acct); got.State != Idle {
		t.Fatalf("an empty registry has %+v", got)
	}
	r.Set(acct, Fetch{State: SignedOut})
	if got := r.State(acct); got.State != SignedOut {
		t.Fatalf("state %+v", got)
	}
	err := errors.New("network is down")
	album := Resource{Kind: Album, ID: "remote"}
	r.Set(album, Fetch{State: Failed, Err: err})
	if got := r.State(album); got.State != Failed || !errors.Is(got.Err, err) {
		t.Fatalf("state %+v", got)
	}
	if got := r.State(Resource{Kind: Playlist, ID: "remote"}); got.State != Idle {
		t.Errorf("another kind has %+v", got)
	}
	r.Set(acct, Fetch{})
	r.Set(album, Fetch{})
	if len(r.fetches) != 0 || r.State(acct).State != Idle {
		t.Errorf("Idle kept %d resources", len(r.fetches))
	}
}

func TestKindString(t *testing.T) {
	for k, want := range map[Kind]string{0: "none", Album: "album", Liked: "liked", 42: "kind(42)"} {
		if got := k.String(); got != want {
			t.Errorf("Kind(%d) is %q, want %q", k, got, want)
		}
	}
}
