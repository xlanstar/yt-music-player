package api

import (
	"strings"
	"testing"
)

func TestCleanCredentials(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"SID=a; HSID=b", "SID=a; HSID=b"},
		{"Cookie: SID=a;\n  HSID=b\n", "SID=a; HSID=b"},
		{"cookie:SID=a", "SID=a"},
		{" \n\t", ""},
		{"Cookie:", ""},
	} {
		if got := CleanCredentials(tc.in); got != tc.want {
			t.Errorf("CleanCredentials(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// FuzzCollapseSpace holds collapseSpace to what it stands for.
func FuzzCollapseSpace(f *testing.F) {
	for _, s := range []string{
		"", " ", "a", "a b", " a  b ", "a\tb", "a\nb\n", "a \u00a0b", "\u2003a\u2003", "a\xffb \xff", "a\u0085b", "Cookie: SID=a;\n  HSID=b\n",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if got, want := collapseSpace(s), strings.Join(strings.Fields(s), " "); got != want {
			t.Errorf("collapseSpace(%q) = %q, want %q", s, got, want)
		}
	})
}
