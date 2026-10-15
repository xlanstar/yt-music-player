package catalog

import (
	"slices"
	"testing"
)

func TestPlaceholderReferencesResolve(t *testing.T) {
	c := Placeholder()
	check := func(what, id string) {
		t.Helper()
		if _, ok := c.Song(id); !ok {
			t.Errorf("%s refers to unknown song %q", what, id)
		}
	}
	for _, playlist := range c.Playlists {
		for _, id := range playlist.Songs {
			check(playlist.ID, id)
		}
	}
	for _, id := range placeholderTrending() {
		check("trending", id)
	}
	for _, id := range PlaceholderLikes() {
		check("likes", id)
	}
	for _, s := range c.Songs {
		if _, ok := c.Album(s.AlbumID); s.AlbumID != "" && !ok {
			t.Errorf("song %q is on unknown album %q", s.ID, s.AlbumID)
		}
	}
	seen := map[string]bool{}
	for _, id := range c.SongIDs() {
		if seen[id] {
			t.Errorf("duplicate song ID %q", id)
		}
		seen[id] = true
	}
}

func TestAlbumTracks(t *testing.T) {
	c := Placeholder()
	var titles []string
	for _, s := range c.AlbumTracks("paper-moons") {
		titles = append(titles, s.Title)
	}
	if want := []string{"Paper Moons", "Slow Burn Summer"}; !slices.Equal(titles, want) {
		t.Errorf("tracks %q, want %q", titles, want)
	}
	if got := c.AlbumTracks("nope"); len(got) != 0 {
		t.Errorf("unknown album has tracks %v", got)
	}
}

func TestAlbumArtists(t *testing.T) {
	c := Placeholder()
	if artists := AlbumArtists(c.Albums); len(artists) != len(c.Albums) {
		// Every placeholder album has its own artist.
		t.Fatalf("%d artists for %d albums", len(artists), len(c.Albums))
	}
	albums := []Album{
		{ID: "a", Artist: "X", Art: "1"},
		{ID: "b", Artist: "Y", Art: "2"},
		{ID: "c", Artist: "X", Art: "3"},
	}
	want := []Artist{{Name: "X", Art: "1"}, {Name: "Y", Art: "2"}}
	if got := AlbumArtists(albums); !slices.Equal(got, want) {
		t.Errorf("artists %v, want %v", got, want)
	}
}

func TestSongsByIDSkipsUnknown(t *testing.T) {
	c := Placeholder()
	got := c.SongsByID([]string{"rewind", "nope", "ember"})
	if len(got) != 2 || got[0].ID != "rewind" || got[1].ID != "ember" {
		t.Errorf("got %v", got)
	}
	if TotalSeconds(got) != 204+252 {
		t.Errorf("total %d", TotalSeconds(got))
	}
}

func TestAddMergesByID(t *testing.T) {
	c := Placeholder()
	before := len(c.Songs)
	renamed := c.Songs[0]
	renamed.Title = "Renamed"
	fresh := Song{ID: "fresh", Title: "Fresh", Artist: "New Artist", AlbumID: "fresh-album"}
	c.AddAlbum(Album{ID: "fresh-album", Title: "Fresh Album", Artist: "New Artist"}, []Song{fresh, renamed})

	if len(c.Songs) != before+1 {
		t.Fatalf("songs = %d, want %d", len(c.Songs), before+1)
	}
	if s, _ := c.Song(renamed.ID); s.Title != "Renamed" {
		t.Errorf("known song not replaced: %+v", s)
	}
	if album, ok := c.Album("fresh-album"); !ok || album.Title != "Fresh Album" {
		t.Errorf("added album missing: %+v", album)
	}
	var ids []string
	for _, s := range c.AlbumTracks("fresh-album") {
		ids = append(ids, s.ID)
	}
	if want := []string{"fresh", renamed.ID}; !slices.Equal(ids, want) {
		t.Errorf("album tracks = %v, want %v", ids, want)
	}
}

func TestAddKeepsKnownPlaylistSongs(t *testing.T) {
	c := Placeholder()
	known := c.Playlists[0]
	c.Add(nil, nil, []Playlist{{ID: known.ID, Name: "From a feed"}})
	got, _ := c.Playlist(known.ID)
	if got.Name != "From a feed" || !slices.Equal(got.Songs, known.Songs) {
		t.Errorf("playlist = %+v, want the new name and the known songs", got)
	}
}

func TestLibraryIsSavedPlaylistsOnly(t *testing.T) {
	c := Placeholder()
	var ids []string
	for _, playlist := range c.LibraryPlaylists() {
		ids = append(ids, playlist.ID)
	}
	if len(ids) != len(placeholderPlaylists()) || slices.Contains(ids, "supermix") {
		t.Fatalf("library %q, want the saved playlists without mixes", ids)
	}
	if _, ok := c.Playlist("supermix"); !ok {
		t.Error("mixes are not in the catalog")
	}
	// Feed playlists stay out of the library; a saved one renamed by a
	// fetch is renamed in it too.
	c.Add(nil, nil, []Playlist{{ID: "feed-only", Name: "Feed"}, {ID: ids[0], Name: "Renamed"}})
	got := c.LibraryPlaylists()
	if len(got) != len(ids) || got[0].Name != "Renamed" {
		t.Errorf("library after Add %+v", got)
	}
}
