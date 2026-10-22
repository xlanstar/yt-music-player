package ytmusic

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"yt-music-player/internal/catalog"
)

func TestSearch(t *testing.T) {
	results, err := fixtureClient(t).Search(t.Context(), " "+searchQuery+" ")
	if err != nil {
		t.Fatal(err)
	}
	if len(results.Songs) != 20 || len(results.Albums) != 20 {
		t.Fatalf("found %d songs and %d albums, want 20 and 20", len(results.Songs), len(results.Albums))
	}
	want := catalog.Song{
		ID: "uSFXOBaqiR8", Title: "Veridis Quo (Edit)", Artist: "Daft Punk",
		Album: "Veridis Quo", AlbumID: "MPREb_ACdoDdB5hLk", Seconds: 165,
	}
	if got := withoutArt(results.Songs[0]); got != want {
		t.Errorf("first song = %+v, want %+v", got, want)
	}
	if got := results.Songs[10].Artist; got != "Daft Punk, Pharrell Williams, Nile Rodgers" {
		t.Errorf("artists of %q = %q", results.Songs[10].Title, got)
	}
	for _, s := range results.Songs {
		if s.ID == "" || s.Title == "" || s.Artist == "" || s.Seconds == 0 || s.Art == "" ||
			!strings.HasPrefix(s.AlbumID, "MPREb_") {
			t.Errorf("incomplete song %+v", s)
		}
	}

	wantAlbum := catalog.Album{
		ID:     "MPREb_7ltM34kr0mH",
		Title:  "Discovery",
		Artist: "Daft Punk",
		Year:   2001,
		Kind:   catalog.ReleaseAlbum,
	}
	if got := results.Albums[1]; withoutAlbumArt(got) != wantAlbum || !strings.Contains(string(got.Art), "w544-h544") {
		t.Errorf("second album = %+v, want %+v with the 544px art", got, wantAlbum)
	}
	got := results.Albums[6]
	if got.Title != "Robot Rock (Edit)" || got.Kind != catalog.ReleaseSingle || got.Year != 2005 {
		t.Errorf("seventh album = %+v, want the single Robot Rock (Edit) of 2005", got)
	}
	for _, al := range results.Albums {
		if !strings.HasPrefix(al.ID, "MPREb_") || al.Title == "" || al.Artist == "" || al.Year == 0 || al.Art == "" {
			t.Errorf("incomplete album %+v", al)
		}
	}
}

func TestSearchBlank(t *testing.T) {
	c := serverClient(t, func(http.ResponseWriter, *http.Request) { t.Error("blank search sent a request") })
	if r, err := c.Search(t.Context(), "  "); err != nil || !r.IsEmpty() {
		t.Errorf("Search(blank) = %+v, %v", r, err)
	}
}

func TestAlbum(t *testing.T) {
	al, tracks, err := fixtureClient(t).Album(t.Context(), albumID)
	if err != nil {
		t.Fatal(err)
	}
	want := catalog.Album{ID: albumID, Title: "Discovery", Artist: "Daft Punk", Year: 2001, Kind: catalog.ReleaseAlbum}
	if withoutAlbumArt(al) != want || !strings.HasSuffix(string(al.Art), "=w544-h544-l90-rj") {
		t.Errorf("album = %+v, want %+v with the 544px art", al, want)
	}
	titles := []string{
		"One More Time", "Aerodynamic", "Digital Love", "Harder, Better, Faster, Stronger", "Crescendolls",
		"Nightvision", "Superheroes", "High Life", "Something About Us", "Voyager", "Veridis Quo",
		"Short Circuit", "Face to Face", "Too Long",
	}
	if len(tracks) != len(titles) {
		t.Fatalf("got %d tracks, want %d", len(tracks), len(titles))
	}
	for i, s := range tracks {
		if s.Title != titles[i] || s.Artist != "Daft Punk" || s.Album != "Discovery" || s.AlbumID != albumID ||
			s.Art != al.Art || s.Seconds == 0 || s.ID == "" {
			t.Errorf("track %d = %+v", i+1, s)
		}
	}
	if tracks[0].ID != "FGBhQbmPwH8" || tracks[0].Seconds != 5*60+21 || tracks[13].Seconds != 10*60+1 {
		t.Errorf("tracks 1 and 14 = %+v, %+v", tracks[0], tracks[13])
	}
}

func TestPlaylist(t *testing.T) {
	pl, songs, err := fixtureClient(t).Playlist(t.Context(), playlistID)
	if err != nil {
		t.Fatal(err)
	}
	if pl.ID != playlistID || pl.Name != "All Daft Punk Songs In Order" || pl.Owner != "IffyAlex" ||
		!strings.HasSuffix(string(pl.Art), "=s1200") {
		t.Errorf("playlist = %+v", pl)
	}
	// 151 tracks over two pages, one of them greyed out.
	if len(songs) != 150 || len(pl.Songs) != 150 {
		t.Fatalf("got %d songs and %d IDs, want 150", len(songs), len(pl.Songs))
	}
	for i, s := range songs {
		if pl.Songs[i] != s.ID || s.ID == "" || s.Title == "" || s.Artist == "" || s.Seconds == 0 || s.Art == "" {
			t.Errorf("song %d = %+v (ID %q)", i, s, pl.Songs[i])
		}
	}
	first := catalog.Song{ID: "D2i0skatDaE", Title: "DAFT PUNK -THE NEW WAVE", Artist: "Tockyn", Seconds: 438}
	if got := withoutArt(songs[0]); got != first {
		t.Errorf("first song = %+v, want %+v", got, first)
	}
	album := catalog.Song{
		ID: "F6dMKqiDhGk", Title: "Ouverture", Artist: "Daft Punk", Album: "Daft Club",
		AlbumID: songs[37].AlbumID, Seconds: 161,
	}
	if got := withoutArt(songs[37]); got != album || !strings.HasPrefix(got.AlbumID, "MPREb_") {
		t.Errorf("song 37 = %+v, want %+v", got, album)
	}
	// The continuation's songs follow the first page's.
	if s := songs[100]; s.ID != "-2C_P2N0LA8" || s.Title != "End of Line" {
		t.Errorf("song 100 = %+v, want End of Line", s)
	}
	if s := songs[149]; s.ID != "DuDX6wNfjqc" || s.Title != "Daft Punk - Epilogue" || s.Seconds != 478 {
		t.Errorf("last song = %+v, want Epilogue", s)
	}
}

func TestPlaylistAcceptsVLPrefix(t *testing.T) {
	pl, _, err := fixtureClient(t).Playlist(t.Context(), "VL"+playlistID)
	if err != nil || pl.ID != playlistID {
		t.Errorf("Playlist(VL…) = %q, %v", pl.ID, err)
	}
}

func TestNotFound(t *testing.T) {
	c := fixtureClient(t)
	if _, _, err := c.Playlist(t.Context(), missingPlaylistID); !errors.Is(err, catalog.ErrNotFound) {
		t.Errorf("missing playlist: err = %v, want ErrNotFound", err)
	}
	// Invalid IDs fail before any request, which fixtureClient would reject.
	for _, id := range []string{"", "Discovery", "VLPLx", "UCRr1xG_2WIDs18a6cIiCxeA"} {
		if _, _, err := c.Album(t.Context(), id); !errors.Is(err, catalog.ErrNotFound) {
			t.Errorf("Album(%q): err = %v, want ErrNotFound", id, err)
		}
	}
	if _, _, err := c.Playlist(t.Context(), "VL"); !errors.Is(err, catalog.ErrNotFound) {
		t.Errorf("Playlist(VL): err = %v, want ErrNotFound", err)
	}
}

func TestNotFoundStatus(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusBadRequest} {
		c := serverClient(t, func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, `{"error":{"message":"Requested entity was not found."}}`, status)
		})
		if _, _, err := c.Album(t.Context(), "MPREb_gone"); !errors.Is(err, catalog.ErrNotFound) {
			t.Errorf("%d: err = %v, want ErrNotFound", status, err)
		}
	}
}

func TestOtherErrorsAreNotNotFound(t *testing.T) {
	c := serverClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	if _, _, err := c.Playlist(t.Context(), "PLx"); err == nil || errors.Is(err, catalog.ErrNotFound) {
		t.Errorf("403: err = %v, want an error other than ErrNotFound", err)
	}
	if _, err := c.Search(t.Context(), "x"); err == nil {
		t.Error("403: Search succeeded")
	}
}

func TestUnavailableAlert(t *testing.T) {
	c := serverClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"alerts":[{"alertRenderer":{"type":"ERROR",` +
			`"text":{"runs":[{"text":"This playlist is not available."}]}}}]}`))
	})
	_, _, err := c.Playlist(t.Context(), "PLprivate")
	if !errors.Is(err, catalog.ErrNotFound) || !strings.Contains(err.Error(), "not available") {
		t.Errorf("err = %v, want ErrNotFound with the alert", err)
	}
}

func withoutArt(s catalog.Song) catalog.Song {
	s.Art = ""
	return s
}

func withoutAlbumArt(al catalog.Album) catalog.Album {
	al.Art = ""
	return al
}

// Errors name the package once, at the method the caller called; inner
// layers add only what they asked for.
func TestErrorsNameThePackageOnce(t *testing.T) {
	signedOut, _ := accountClient(t, nil)
	_, err := signedOut.Account(t.Context())
	if want := "ytmusic: account: account/account_menu: catalog: signed out"; err == nil || err.Error() != want {
		t.Errorf("Account: err %v, want %q", err, want)
	}
	if !errors.Is(err, catalog.ErrSignedOut) {
		t.Errorf("Account: err %v, want ErrSignedOut", err)
	}

	broken := serverClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("{")) })
	_, _, albumErr := broken.Album(t.Context(), albumID)
	_, searchErr := broken.Search(t.Context(), "q")
	_, signInErr := broken.SignIn(t.Context(), "Cookie: "+testCookie)
	for name, err := range map[string]error{"Album": albumErr, "Search": searchErr, "SignIn": signInErr} {
		if err == nil {
			t.Errorf("%s: no error", name)
			continue
		}
		for line := range strings.Lines(err.Error()) {
			if n := strings.Count(line, "ytmusic:"); n != 1 {
				t.Errorf("%s: %q names the package %d times", name, line, n)
			}
		}
	}
}
