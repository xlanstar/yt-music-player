package catalog

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"
)

func TestPlaceholderSourceAlbum(t *testing.T) {
	src := NewPlaceholderSource(0)
	album, tracks, err := src.Album(t.Context(), "velvet-static")
	if err != nil || album.Title != "Velvet Static" {
		t.Fatalf("album %+v, err %v", album, err)
	}
	want := Placeholder().AlbumTracks("velvet-static")
	if len(tracks) == 0 || !slices.Equal(tracks, want) {
		t.Errorf("tracks %v, want %v", tracks, want)
	}
}

func TestPlaceholderSourcePlaylist(t *testing.T) {
	src := NewPlaceholderSource(0)
	playlist, songs, err := src.Playlist(t.Context(), "late-night-drive")
	if err != nil || playlist.Name != "Late Night Drive" {
		t.Fatalf("playlist %+v, err %v", playlist, err)
	}
	if len(songs) != len(playlist.Songs) || songs[0].ID != playlist.Songs[0] {
		t.Errorf("songs %v for IDs %v", songs, playlist.Songs)
	}
	// Changes by a caller must not reach the source.
	playlist.Songs[0] = "changed"
	again, _, _ := src.Playlist(t.Context(), "late-night-drive")
	if again.Songs[0] == "changed" {
		t.Error("the source shares its playlist's songs with callers")
	}
}

func TestPlaceholderSourceSearch(t *testing.T) {
	src := NewPlaceholderSource(0)
	results, err := src.Search(t.Context(), "  ORBIT ")
	if err != nil {
		t.Fatal(err)
	}
	var songs, albums []string
	for _, song := range results.Songs {
		songs = append(songs, song.ID)
	}
	for _, album := range results.Albums {
		albums = append(albums, album.ID)
	}
	if want := []string{"gravity-well", "weightless"}; !slices.Equal(songs, want) {
		t.Errorf("songs %q, want %q", songs, want)
	}
	if want := []string{"low-orbit"}; !slices.Equal(albums, want) {
		t.Errorf("albums %q, want %q", albums, want)
	}
	for _, query := range []string{"   ", "zzz"} {
		if results, err := src.Search(t.Context(), query); err != nil || !results.IsEmpty() {
			t.Errorf("%q found %+v, err %v", query, results, err)
		}
	}
}

func TestPlaceholderSourceHome(t *testing.T) {
	src := NewPlaceholderSource(0)
	home, err := src.Home(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, shelf := range home.Shelves {
		titles = append(titles, shelf.Title)
	}
	want := []string{"Quick picks", "Albums for you", "Mixed for you", "Artists you love"}
	if !slices.Equal(titles, want) {
		t.Fatalf("shelves %q, want %q", titles, want)
	}
	for i, kind := range []ItemKind{ItemSong, ItemAlbum, ItemPlaylist, ItemArtist} {
		shelf := home.Shelves[i]
		if len(shelf.Items) == 0 {
			t.Errorf("%s is empty", shelf.Title)
		}
		for _, item := range shelf.Items {
			if item.Kind != kind {
				t.Errorf("%s has an item of kind %d, want %d", shelf.Title, item.Kind, kind)
			}
		}
	}
	// Like YouTube Music's feed, mixes come without songs but open as playlists.
	for _, item := range home.Shelves[2].Items {
		mix := item.Playlist
		if mix.Songs != nil {
			t.Errorf("mix %q lists its songs", mix.ID)
		}
		opened, songs, err := src.Playlist(t.Context(), mix.ID)
		if err != nil || opened.Name != mix.Name || len(songs) == 0 || len(songs) != len(opened.Songs) {
			t.Errorf("mix %q opens as %+v with %d songs, err %v", mix.ID, opened, len(songs), err)
		}
	}
}

func TestPlaceholderSourceExplore(t *testing.T) {
	src := NewPlaceholderSource(0)
	explore, err := src.Explore(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	isEmpty := len(explore.NewReleases) == 0 || len(explore.Moods) == 0
	if isEmpty || len(explore.Trending) != len(placeholderTrending()) {
		t.Fatalf("explore %+v", explore)
	}
	seen := map[string]bool{}
	for _, mood := range explore.Moods {
		if mood.ID == "" || seen[mood.ID] {
			t.Errorf("mood %q has a missing or repeated ID %q", mood.Name, mood.ID)
		}
		seen[mood.ID] = true
	}
	if explore.Trending[0].ID != placeholderTrending()[0] {
		t.Errorf("trending starts with %q", explore.Trending[0].ID)
	}
	// Changes by a caller must not reach the source.
	explore.NewReleases[0].Title = "changed"
	again, _ := src.Explore(t.Context())
	if again.NewReleases[0].Title == "changed" {
		t.Error("the source shares its albums with callers")
	}
}

func TestPlaceholderSourceNotFound(t *testing.T) {
	src := NewPlaceholderSource(0)
	if _, _, err := src.Album(t.Context(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("album err %v, want ErrNotFound", err)
	}
	if _, _, err := src.Playlist(t.Context(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("playlist err %v, want ErrNotFound", err)
	}
}

func TestPlaceholderSourceDelayHonorsContext(t *testing.T) {
	src := NewPlaceholderSource(time.Hour)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := src.Search(ctx, "orbit"); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err %v, want the context's", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("returned after %v, not when the context ended", elapsed)
	}

	// A canceled request fails even without a delay.
	ctx, cancel = context.WithCancel(t.Context())
	cancel()
	if _, _, err := NewPlaceholderSource(0).Album(ctx, "velvet-static"); !errors.Is(err, context.Canceled) {
		t.Errorf("err %v, want context.Canceled", err)
	}
	if _, err := NewPlaceholderSource(0).Home(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("home err %v, want context.Canceled", err)
	}
	if _, err := NewPlaceholderSource(0).Explore(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("explore err %v, want context.Canceled", err)
	}

	results, err := NewPlaceholderSource(time.Millisecond).Search(t.Context(), "orbit")
	if err != nil || results.IsEmpty() {
		t.Errorf("delayed search %+v, err %v", results, err)
	}
}

// Meaningful only with -race.
func TestPlaceholderSourceConcurrent(t *testing.T) {
	src := NewPlaceholderSource(0)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 50 {
				if _, err := src.Search(t.Context(), "lumen"); err != nil {
					t.Error(err)
				}
				if _, _, err := src.Album(t.Context(), "low-orbit"); err != nil {
					t.Error(err)
				}
				if _, _, err := src.Playlist(t.Context(), "deep-focus"); err != nil {
					t.Error(err)
				}
				if _, err := src.Home(t.Context()); err != nil {
					t.Error(err)
				}
				if _, err := src.Explore(t.Context()); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
}

func TestPlaceholderSourceAccount(t *testing.T) {
	account, err := NewPlaceholderSource(0).Account(t.Context())
	if err != nil || account.Name != "Lan" {
		t.Fatalf("account %+v, err %v", account, err)
	}
	if _, ok := Source(NewPlaceholderSource(0)).(Accounts); ok {
		t.Error("the placeholder offers to sign in, though it is never signed out")
	}
}

func TestPlaceholderSourceLibrary(t *testing.T) {
	src := NewPlaceholderSource(0)
	library, err := src.Library(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	c := Placeholder()
	if len(library.Playlists) != len(c.LibraryPlaylists()) || library.Playlists[0].ID != "late-night-drive" {
		t.Errorf("playlists %v", library.Playlists)
	}
	for _, playlist := range library.Playlists {
		if slices.ContainsFunc(placeholderMixes(), func(mix Playlist) bool { return mix.ID == playlist.ID }) {
			t.Errorf("mix %q in the library", playlist.ID)
		}
	}
	hasSongs := len(library.Songs) == len(c.Songs)
	hasAlbums := len(library.Albums) == len(c.Albums)
	hasArtists := len(library.Artists) == len(AlbumArtists(c.Albums))
	if !hasSongs || !hasAlbums || !hasArtists {
		t.Errorf("%d songs, %d albums, %d artists", len(library.Songs), len(library.Albums), len(library.Artists))
	}
	// Changes by a caller must not reach the source.
	library.Playlists[0].Songs[0] = "changed"
	library.Artists[0].Name = "changed"
	again, _ := src.Library(t.Context())
	if again.Playlists[0].Songs[0] == "changed" || again.Artists[0].Name == "changed" {
		t.Error("the source shares its library with callers")
	}
}

func TestPlaceholderSourceLikes(t *testing.T) {
	src := NewPlaceholderSource(0)
	liked := func() []string {
		t.Helper()
		playlist, songs, err := src.Playlist(t.Context(), LikedID)
		if err != nil || playlist.ID != LikedID || len(songs) != len(playlist.Songs) {
			t.Fatalf("liked %+v with %d songs, err %v", playlist, len(songs), err)
		}
		return playlist.Songs
	}
	if got := liked(); !slices.Equal(got, PlaceholderLikes()) {
		t.Fatalf("likes %q, want %q", got, PlaceholderLikes())
	}
	if err := src.Like(t.Context(), "paper-moons", true); err != nil {
		t.Fatal(err)
	}
	if err := src.Like(t.Context(), "midnight-arcade", false); err != nil {
		t.Fatal(err)
	}
	want := []string{"paper-moons", "gravity-well", "radio-sunset", "3-am-thoughts"}
	if got := liked(); !slices.Equal(got, want) {
		t.Errorf("likes %q, want %q", got, want)
	}
	// Changes by a caller must not reach the source.
	liked()[0] = "changed"
	if liked()[0] != "paper-moons" {
		t.Error("the source shares its likes with callers")
	}
	if err := src.Like(t.Context(), "nope", true); !errors.Is(err, ErrNotFound) {
		t.Errorf("liking an unknown song: %v", err)
	}
	err := NewPlaceholderSource(time.Hour).Like(canceled(t), "paper-moons", true)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("a canceled like: %v", err)
	}
}

func TestPlaceholderSourceConcurrentLikes(t *testing.T) {
	src := NewPlaceholderSource(0)
	songs := Placeholder().SongIDs()
	var wg sync.WaitGroup
	for _, id := range songs {
		wg.Go(func() {
			if err := src.Like(t.Context(), id, true); err != nil {
				t.Error(err)
			}
		})
		wg.Go(func() {
			if _, _, err := src.Playlist(t.Context(), LikedID); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	liked, _, _ := src.Playlist(t.Context(), LikedID)
	if len(liked.Songs) != len(songs) {
		t.Errorf("%d liked, want every one of %d", len(liked.Songs), len(songs))
	}
}

func canceled(t *testing.T) context.Context {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	return ctx
}
