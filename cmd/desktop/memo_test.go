package main

import (
	"testing"
	"yt-music-player/internal/catalog"
)

// Lines under titles leave out what is unknown, never a dangling dot.
func TestBylines(t *testing.T) {
	album := func(artist string, year int, kind catalog.AlbumKind) catalog.Album {
		return catalog.Album{Artist: artist, Year: year, Kind: kind}
	}
	for _, tc := range []struct {
		name, got, want string
	}{
		{"dotJoin/all", dotJoin("Lumen Drive", "Velvet Static"), "Lumen Drive · Velvet Static"},
		{"dotJoin/first", dotJoin("Lumen Drive", ""), "Lumen Drive"},
		{"dotJoin/last", dotJoin("", "Velvet Static"), "Velvet Static"},
		{"dotJoin/none", dotJoin("", ""), ""},
		{"dotJoin/middle", dotJoin("a", "", "b", ""), "a · b"},
		{"albumByline/year", albumByline(new(album("Lumen Drive", 2021, ""))), "Lumen Drive · 2021"},
		{"albumByline/no_year", albumByline(new(album("Lumen Drive", 0, ""))), "Lumen Drive"},
		{"albumByline/no_artist", albumByline(new(album("", 2021, ""))), "2021"},
		{"albumMeta/year", albumMeta(2021, "· 3 songs · 9 min"), "· 2021 · 3 songs · 9 min"},
		{"albumMeta/no_year", albumMeta(0, "· 3 songs · 9 min"), "· 3 songs · 9 min"},
		{
			"cardSub/kind", cardSub(catalog.Item{Kind: catalog.ItemAlbum, Album: album("Lumen Drive", 0, catalog.KindEP)}),
			"EP · Lumen Drive",
		},
		{"cardSub/no_kind", cardSub(catalog.Item{Kind: catalog.ItemAlbum, Album: album("Lumen Drive", 0, "")}), "Lumen Drive"},
		{"cardSub/no_artist", cardSub(catalog.Item{Kind: catalog.ItemAlbum, Album: album("", 0, catalog.KindEP)}), "EP"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}

func TestRowTextWithoutAlbum(t *testing.T) {
	a := idleApp(t)
	s := catalog.Song{ID: "single", Title: "Alone", Artist: "Lumen Drive"}
	if got := a.rowText(&s).sub; got != "Lumen Drive" {
		t.Errorf("sub %q, want the artist alone", got)
	}
}
