package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// The sheet's Sign in is disabled until a header is pasted.
func TestSignInButton(t *testing.T) {
	src, a, _, tt := signedOutApp(t)
	click(t, tt, "Sign in")
	for range 3 {
		tt.Key(0, ui.KeyTab)
		if tt.Focused("Sign in") {
			tt.Key(0, ui.KeySpace)
			break
		}
	}
	src.None(t, tt.Frame, 50*time.Millisecond)
	if !a.sheet.isOpen {
		t.Fatal("the sheet closed")
	}

	click(t, tt, "Cookie header")
	tt.Type("SID=good")
	tt.Frame()
	// The top bar's Sign in comes first by name; the sheet's follows
	// Cancel's label, its padding and the gap.
	cancel, ok := tt.Find("Cancel")
	if !ok {
		t.Fatalf("no Cancel, texts %q", tt.Texts())
	}
	tt.ClickAt(cancel.X+cancel.W+th.Space(5)+th.Space(2.5)+th.Space(2), cancel.Y+cancel.H/2)
	src.Next(t, tt.Frame, "signin", "SID=good")
}
