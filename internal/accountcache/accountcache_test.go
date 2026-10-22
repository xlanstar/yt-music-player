package accountcache

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"unsafe"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/catalog/catalogtest"
)

func testFile(t *testing.T) File { return File(filepath.Join(t.TempDir(), "cache", "account.json")) }

// adaSaved is a library and likes as a session saves them.
func adaSaved() *Saved {
	songs := catalogtest.Decoded(20)
	return &Saved{
		Account: catalog.Account{ID: "ada-sign-in"},
		Library: catalog.Library{
			Playlists: []catalog.Playlist{{ID: "ada-mix", Name: "Engine Notes", Owner: "Ada Lovelace", Songs: []string{"x"}}},
			Albums:    []catalog.Album{{ID: "MPREb_1", Title: "Far Signals", Artist: "Nova Static", Year: 2024, Kind: catalog.ReleaseAlbum}},
			Artists:   []catalog.Artist{{Name: "Nova Static", ID: "UCnova", Art: "https://example.com/a.jpg"}},
			Songs:     songs[:5],
		},
		Liked:      []string{songs[9].ID, songs[8].ID, "undescribed"},
		LikedSongs: []catalog.Song{songs[9], songs[8]},
	}
}

func TestSaveLoad(t *testing.T) {
	f := testFile(t)
	want := adaSaved()
	if err := f.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := f.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(&got, want) {
		t.Errorf("loaded %+v, want %+v", got, want)
	}
	// Only the user reads it, and no temporary file stays.
	info, err := os.Stat(string(f))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600", info.Mode().Perm())
	}
	if entries, _ := os.ReadDir(filepath.Dir(string(f))); len(entries) != 1 {
		t.Errorf("%d files beside it", len(entries)-1)
	}
}

// Only the sign-in's ID is kept of the account: it tells whose they are,
// as names and handles do not, and a rename keeps them.
func TestSaveKeepsOnlyID(t *testing.T) {
	f := testFile(t)
	saved := adaSaved()
	saved.Account = catalog.Account{Name: "Ada Lovelace", Handle: "@ada", Photo: "https://example.com/ada.jpg", ID: "ada-sign-in"}
	if err := f.Save(saved); err != nil {
		t.Fatal(err)
	}
	got, err := f.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Account != (catalog.Account{ID: "ada-sign-in"}) {
		t.Errorf("account %+v", got.Account)
	}
}

func TestLoadNone(t *testing.T) {
	got, err := testFile(t).Load()
	if !errors.Is(err, fs.ErrNotExist) || !reflect.DeepEqual(got, Saved{}) {
		t.Errorf("loaded %+v, %v; want nothing, fs.ErrNotExist", got, err)
	}
	if err := File("").Save(adaSaved()); err != nil {
		t.Errorf("an empty File saved: %v", err)
	}
	if _, err := File("").Load(); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("an empty File loaded: %v", err)
	}
}

// A file of another version, or broken, loads as nothing.
func TestLoadUnreadable(t *testing.T) {
	for name, data := range map[string]string{
		"version":  `{"v":99,"account":{"Name":"Ada Lovelace"}}`,
		"by names": `{"v":1,"account":{"Name":"Ada Lovelace","Handle":""},"liked":["x"]}`,
		"broken":   `{"v":2,"account":`,
	} {
		t.Run(name, func(t *testing.T) {
			f := testFile(t)
			if err := os.MkdirAll(filepath.Dir(string(f)), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(string(f), []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := f.Load()
			if err == nil || !reflect.DeepEqual(got, Saved{}) {
				t.Errorf("loaded %+v, %v", got, err)
			}
		})
	}
}

func TestRemove(t *testing.T) {
	f := testFile(t)
	if err := f.Remove(); err != nil {
		t.Errorf("removing none: %v", err)
	}
	if err := f.Save(adaSaved()); err != nil {
		t.Fatal(err)
	}
	if err := f.Remove(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Load(); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("loaded %v after Remove", err)
	}
	// A crash's temporary file holds the account's data too.
	leftover := filepath.Join(filepath.Dir(string(f)), "."+filepath.Base(string(f))+"-123")
	if err := os.WriteFile(leftover, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.Remove(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(leftover); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the leftover stayed: %v", err)
	}
}

// Decoded, each song's strings are copies of their own; loaded, the
// songs share those they repeat, as fetched ones do.
func TestLoadShares(t *testing.T) {
	f := testFile(t)
	songs := catalogtest.Decoded(100)
	ids := make([]string, len(songs))
	for i, song := range songs {
		ids[i] = song.ID
	}
	if err := f.Save(&Saved{Account: catalog.Account{ID: "ada-sign-in"}, Library: catalog.Library{Songs: songs}, Liked: ids, LikedSongs: songs}); err != nil {
		t.Fatal(err)
	}
	got, err := f.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, list := range [][]catalog.Song{got.Library.Songs, got.LikedSongs} {
		byArtist := map[string]*byte{}
		for _, song := range list {
			p := unsafe.StringData(song.Artist)
			if q, ok := byArtist[song.Artist]; ok && q != p {
				t.Fatalf("artist %q held twice", song.Artist)
			}
			byArtist[song.Artist] = p
		}
	}
}

// Front-ends save at once: the last rename wins, whole.
func TestSaveTogether(t *testing.T) {
	f := testFile(t)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			saved := adaSaved()
			saved.Liked = saved.Liked[:i%3]
			if err := f.Save(saved); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if got, err := f.Load(); err != nil || got.Account.ID != "ada-sign-in" {
		t.Errorf("loaded %+v, %v", got.Account, err)
	}
	if entries, _ := os.ReadDir(filepath.Dir(string(f))); len(entries) != 1 {
		t.Errorf("%d files beside it", len(entries)-1)
	}
}
