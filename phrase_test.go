package main

import (
	"testing"
	"yt-music-player/locales"
)

func TestPhrase(t *testing.T) {
	for _, lang := range []string{"en", "zh-Hant"} {
		inLanguage(t, lang)
		for _, tc := range []struct{ got, want string }{
			{songByArtist.say2("Paper Moons", "Ivy Calder"), locales.SongByArtist("Paper Moons", "Ivy Calder")},
			{seekValue.say2("1:24", "3:47"), locales.SeekValue("1:24", "3:47")},
			{quoted.say1("lumen"), locales.Quoted("lumen")},
			// An argument holding a marker is filled in as it is.
			{searchNone.say1(markers[1]), locales.SearchNone(markers[1])},
		} {
			if tc.got != tc.want {
				t.Errorf("%s: %q, want %q", lang, tc.got, tc.want)
			}
		}
	}
	// A message that swaps its arguments keeps each in its place.
	swapped := phrase2(func(a, b string) string { return b + " / " + a })
	if got := swapped.say2("x", "y"); got != "y / x" || !swapped.exact {
		t.Errorf("swapped %q, exact %v", got, swapped.exact)
	}
	// One that repeats an argument is formatted each time.
	twice := phrase1(func(a string) string { return a + a })
	if got := twice.say1("ab"); got != "abab" || twice.exact {
		t.Errorf("repeated %q, exact %v", got, twice.exact)
	}
}
