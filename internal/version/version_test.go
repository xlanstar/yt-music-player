package version

import "testing"

func TestIsDev(t *testing.T) {
	for v, want := range map[string]bool{"dev": true, "": true, "0.1.0": false, "development": false} {
		if got := IsDev(v); got != want {
			t.Errorf("IsDev(%q) = %v, want %v", v, got, want)
		}
	}
	if !IsDev(Version) {
		t.Errorf("Version = %q without -ldflags, want dev", Version)
	}
}

func TestOlder(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"0.1.0", "0.2.0", true},
		{"0.2.0", "0.1.0", false},
		{"0.1.0", "0.1.0", false},
		{"0.9.9", "0.10.0", true}, // by number, not text
		{"1.2.3", "1.2.10", true},
		{"0.10.0", "1.0.0", true},
		// Development builds, pre-releases and garbage never compare.
		{"dev", "0.1.0", false},
		{"0.1.0", "dev", false},
		{"", "0.1.0", false},
		{"0.1", "0.2.0", false},
		{"0.1.0", "1.x.0", false},
		{"1.0.0-rc.1", "2.0.0", false},
		{"v0.1.0", "0.2.0", false},
		{"1..0", "2.0.0", false},
		{"1.0.0.0", "2.0.0", false},
		{"-1.0.0", "2.0.0", false},
		{"99999999999999999999.0.0", "2.0.0", false},
	} {
		if got := Older(tc.a, tc.b); got != tc.want {
			t.Errorf("Older(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
