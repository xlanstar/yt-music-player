package session

import (
	"errors"
	"io/fs"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"
	"yt-music-player/internal/accountcache"
	"yt-music-player/internal/api"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/catalog/catalogtest"
	"yt-music-player/internal/fetchstate"
)

// fakeCache keeps a Saved in memory; the Session calls it off the UI
// thread. A gate, when set, holds its call until closed.
type fakeCache struct {
	mu       sync.Mutex
	saved    *accountcache.Saved
	loadErr  error
	ops      []string
	loadGate chan struct{}
	saveGate chan struct{}
}

func (c *fakeCache) Load() (accountcache.Saved, error) {
	if c.loadGate != nil {
		<-c.loadGate
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ops = append(c.ops, "load")
	switch {
	case c.loadErr != nil:
		return accountcache.Saved{}, c.loadErr
	case c.saved == nil:
		return accountcache.Saved{}, fs.ErrNotExist
	}
	return *c.saved, nil
}

func (c *fakeCache) Save(s *accountcache.Saved) error {
	if c.saveGate != nil {
		<-c.saveGate
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ops = append(c.ops, "save")
	c.saved = s
	return nil
}

func (c *fakeCache) Remove() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ops = append(c.ops, "remove")
	c.saved = nil
	return nil
}

// took returns the calls made so far, which must be want.
func (c *fakeCache) took(t *testing.T, want ...string) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if !slices.Equal(c.ops, want) {
		t.Fatalf("cache calls %q, want %q", c.ops, want)
	}
}

func (c *fakeCache) kept() *accountcache.Saved {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.saved
}

var oldMix = catalog.Playlist{ID: "old-mix", Name: "Old Notes"}

// adaCached is what a previous run kept of Ada's: older than the fetch.
func adaCached() *accountcache.Saved {
	old := catalog.Song{ID: "old-1", Title: "Old Song", Artist: "Nova Static"}
	return &accountcache.Saved{
		Account:    catalog.Account{ID: ada.ID},
		Library:    catalog.Library{Playlists: []catalog.Playlist{oldMix}, Songs: []catalog.Song{old}},
		Liked:      []string{"old-1", "far-1"},
		LikedSongs: []catalog.Song{old, tracks[0]},
	}
}

// cachedFixture is a fixture whose Session uses c, told Ada is signed in:
// it fetches, and reads c.
func cachedFixture(t *testing.T, c *fakeCache) (f *fixture, library, liked catalogtest.Call) {
	t.Helper()
	f = newFixture(t)
	f.s.cache = c
	f.s.ApplyAccount(api.AccountEvent{Account: ada, SignedIn: true})
	library, liked = f.takeFetches(t)
	return f, library, liked
}

func libraryIDs(cat *catalog.Catalog) []string {
	var ids []string
	for _, pl := range cat.LibraryPlaylists() {
		ids = append(ids, pl.ID)
	}
	return ids
}

// Read before the fetch ends, the cache shows at once, loaded; the fetch
// replaces it, and is kept for the next run.
func TestCacheShownAtOnce(t *testing.T) {
	c := &fakeCache{saved: adaCached()}
	f, library, liked := cachedFixture(t, c)
	f.run(t)
	c.took(t, "load")
	for _, res := range []fetchstate.Resource{LibraryRes, LikedRes} {
		wantState(t, f, res, fetchstate.Loaded)
	}
	if !slices.Equal(libraryIDs(f.cat), []string{"old-mix"}) || !f.s.IsLiked("old-1") || f.s.LikedCount() != 2 {
		t.Errorf("cache not shown: library %q, %d liked", libraryIDs(f.cat), f.s.LikedCount())
	}
	if song, ok := f.cat.Song("old-1"); !ok || song.Title != "Old Song" {
		t.Error("the cached songs are not in the catalog")
	}
	if !f.s.IsBusy() {
		t.Error("not busy while fetching")
	}

	library.Reply <- catalogtest.Reply{Library: adaLibrary()}
	f.run(t)
	// One fetched is not saved without the other.
	f.none(t)
	c.took(t, "load")
	liked.Reply <- adaLiked()
	f.run(t)
	if !slices.Equal(libraryIDs(f.cat), []string{"ada-mix"}) || f.s.IsLiked("old-1") || !f.s.IsLiked("far-2") {
		t.Errorf("the fetch did not replace the cache: library %q", libraryIDs(f.cat))
	}
	f.run(t)
	c.took(t, "load", "save")
	lib := f.s.Library()
	want := &accountcache.Saved{
		Account:    ada,
		Library:    catalog.Library{Playlists: []catalog.Playlist{{ID: adaMix.ID, Name: adaMix.Name}}, Artists: lib.Artists, Songs: tracks},
		Liked:      []string{"far-2", "far-1"},
		LikedSongs: []catalog.Song{tracks[1], tracks[0]},
	}
	if got := c.kept(); !reflect.DeepEqual(got, want) {
		t.Errorf("saved %+v, want %+v", got, want)
	}
	if f.s.IsBusy() {
		t.Error("busy once saved")
	}

	// Told again, as on reconnecting, the Session fetches anew and saves
	// once both came, but never reads the cache again.
	f.s.ApplyAccount(api.AccountEvent{Account: ada, SignedIn: true})
	library, liked = f.takeFetches(t)
	library.Reply <- catalogtest.Reply{Library: adaLibrary()}
	f.run(t)
	if f.s.diskJob.IsRunning() {
		t.Error("saved the new library with the old likes")
	}
	liked.Reply <- adaLiked()
	f.run(t)
	f.run(t)
	c.took(t, "load", "save", "save")
	if f.s.IsBusy() {
		t.Error("busy once saved")
	}
}

// Another account's cache shows nothing, and goes: no longer kept than
// needed.
func TestCacheOfAnother(t *testing.T) {
	cached := adaCached()
	cached.Account = catalog.Account{ID: grace.ID}
	c := &fakeCache{saved: cached}
	f, _, _ := cachedFixture(t, c)
	f.run(t)
	wantState(t, f, LibraryRes, fetchstate.Loading)
	wantState(t, f, LikedRes, fetchstate.Loading)
	if len(f.cat.LibraryPlaylists()) != 0 || f.s.IsLiked("old-1") {
		t.Error("Grace's cache shown as Ada's")
	}
	f.run(t)
	c.took(t, "load", "remove")
}

// Another sign-in's cache shows nothing, though both accounts have the
// same name and no handle, as YouTube's often have none; a rename's shows.
func TestCacheOfSameName(t *testing.T) {
	for _, tc := range []struct {
		name    string
		id      string
		isShown bool
	}{
		{"another sign-in", "another-sign-in", false},
		{"renamed", ada.ID, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cached := adaCached()
			cached.Account = catalog.Account{Name: "Ada", ID: ada.ID}
			c := &fakeCache{saved: cached}
			f := newFixture(t)
			f.s.cache = c
			f.s.ApplyAccount(api.AccountEvent{Account: catalog.Account{Name: "Ada", ID: tc.id}, SignedIn: true})
			f.takeFetches(t)
			f.run(t)
			if shown := f.s.IsLiked("old-1"); shown != tc.isShown {
				t.Errorf("cache shown %v, want %v", shown, tc.isShown)
			}
		})
	}
}

// Read once the fetch was saved, another account's cache stays replaced.
func TestCacheOfAnotherAfterSave(t *testing.T) {
	cached := adaCached()
	cached.Account = catalog.Account{ID: grace.ID}
	c := &fakeCache{saved: cached, loadGate: make(chan struct{})}
	f, library, liked := cachedFixture(t, c)
	library.Reply <- catalogtest.Reply{Library: adaLibrary()}
	liked.Reply <- adaLiked()
	f.run(t)
	f.run(t)
	f.run(t) // the save
	close(c.loadGate)
	f.run(t)
	if f.s.IsBusy() {
		t.Error("busy once read")
	}
	c.took(t, "save", "load")
	if c.kept().Account != ada {
		t.Errorf("kept %+v's", c.kept().Account)
	}
}

// Another account signing in removes the last one's cache, then keeps
// its own.
func TestCacheRemovedOnSwitch(t *testing.T) {
	c := &fakeCache{}
	f, library, liked := cachedFixture(t, c)
	f.run(t) // none to read
	library.Reply <- catalogtest.Reply{Library: adaLibrary()}
	liked.Reply <- adaLiked()
	f.run(t)
	f.run(t)
	f.run(t) // the save
	f.s.ApplyAccount(api.AccountEvent{Account: grace, SignedIn: true, Switched: true})
	f.run(t)
	c.took(t, "load", "save", "remove")
	f.fetched(t)
	f.run(t)
	c.took(t, "load", "save", "remove", "save")
	if c.kept().Account != grace {
		t.Errorf("kept %+v's", c.kept().Account)
	}
}

// Read once the fetch ended, the cache changes nothing.
func TestCacheAfterFetch(t *testing.T) {
	c := &fakeCache{saved: adaCached(), loadGate: make(chan struct{})}
	f, library, liked := cachedFixture(t, c)
	library.Reply <- catalogtest.Reply{Library: adaLibrary()}
	liked.Reply <- adaLiked()
	f.run(t)
	f.run(t)
	f.run(t) // the save
	close(c.loadGate)
	f.run(t)
	if !slices.Equal(libraryIDs(f.cat), []string{"ada-mix"}) || f.s.IsLiked("old-1") {
		t.Errorf("the cache replaced the fetch: library %q", libraryIDs(f.cat))
	}
}

// Likes told while fetching go over the fetch's result, the cache shown
// meanwhile or not.
func TestCacheLikesToldMeanwhile(t *testing.T) {
	f, library, liked := cachedFixture(t, &fakeCache{saved: adaCached()})
	f.s.ApplyLikes(api.Likes{Added: []string{"new-1"}, Removed: []string{"far-2"}})
	f.run(t)
	if !f.s.IsLiked("new-1") || !f.s.IsLiked("old-1") {
		t.Error("the cache dropped a like told before it")
	}
	f.s.ApplyLikes(api.Likes{Added: []string{"new-2"}})
	library.Reply <- catalogtest.Reply{Library: adaLibrary()}
	liked.Reply <- adaLiked()
	f.run(t)
	f.run(t)
	if !f.s.IsLiked("new-1") || !f.s.IsLiked("new-2") || f.s.IsLiked("far-2") || !f.s.IsLiked("far-1") {
		t.Error("likes told while fetching lost")
	}
}

// Offline, the cache shows, and a failed fetch keeps it until fetched
// again.
func TestCacheWhenFetchFails(t *testing.T) {
	c := &fakeCache{saved: adaCached(), loadGate: make(chan struct{})}
	f, library, liked := cachedFixture(t, c)
	library.Reply <- catalogtest.Reply{Err: errors.New("offline")}
	liked.Reply <- catalogtest.Reply{Err: errors.New("offline")}
	f.run(t)
	f.run(t)
	wantState(t, f, LibraryRes, fetchstate.Failed)
	close(c.loadGate)
	f.run(t)
	wantState(t, f, LibraryRes, fetchstate.Loaded)
	wantState(t, f, LikedRes, fetchstate.Loaded)
	if !f.s.IsLiked("old-1") {
		t.Error("the cache not shown")
	}
	f.s.RetryFailed()
	f.fetched(t)
	if f.s.IsLiked("old-1") || !f.s.IsLiked("far-2") {
		t.Error("not fetched again")
	}
}

// Signing out removes the cache, after a save under way.
func TestCacheRemovedOnSignOut(t *testing.T) {
	c := &fakeCache{saveGate: make(chan struct{})}
	f, library, liked := cachedFixture(t, c)
	f.run(t) // none to read
	library.Reply <- catalogtest.Reply{Library: adaLibrary()}
	liked.Reply <- adaLiked()
	f.run(t)
	f.run(t)
	f.s.ApplyAccount(api.AccountEvent{})
	if !f.s.IsBusy() {
		t.Error("not busy while saving")
	}
	close(c.saveGate)
	f.run(t)
	f.run(t)
	c.took(t, "load", "save", "remove")
	if c.kept() != nil || f.s.IsBusy() {
		t.Error("the cache stayed")
	}
}

// An unreadable cache, of another version say, shows nothing and goes;
// the fetch goes on.
func TestCacheUnreadable(t *testing.T) {
	c := &fakeCache{loadErr: errors.New("accountcache: version 1")}
	f, _, _ := cachedFixture(t, c)
	f.run(t)
	wantState(t, f, LibraryRes, fetchstate.Loading)
	if !f.s.IsBusy() {
		t.Error("the fetch stopped")
	}
	f.run(t)
	c.took(t, "load", "remove")
}

// The cache keeps the library's playlists without the songs the catalog
// has of those opened: a start would play those IDs, maybe stale, rather
// than fetch the playlist.
func TestCacheKeepsNoPlaylistSongs(t *testing.T) {
	c := &fakeCache{}
	f, library, liked := cachedFixture(t, c)
	f.run(t) // none to read
	library.Reply <- catalogtest.Reply{Library: listedLibrary()}
	liked.Reply <- adaLiked()
	f.run(t)
	f.run(t)
	f.run(t)                                        // the save
	f.cat.Add(nil, nil, []catalog.Playlist{adaMix}) // opened
	f.s.ApplyAccount(api.AccountEvent{Account: ada, SignedIn: true})
	library, liked = f.takeFetches(t)
	library.Reply <- catalogtest.Reply{Library: listedLibrary()}
	liked.Reply <- adaLiked()
	f.run(t)
	f.run(t)
	f.run(t) // the save
	c.took(t, "load", "save", "save")
	for _, pl := range c.kept().Library.Playlists {
		if pl.Songs != nil {
			t.Fatalf("kept playlist %s's songs %q", pl.ID, pl.Songs)
		}
	}

	next := newFixture(t)
	next.s.cache = c
	next.s.ApplyAccount(api.AccountEvent{Account: ada, SignedIn: true})
	next.takeFetches(t)
	next.run(t)
	if pl, ok := next.cat.Playlist(adaMix.ID); !ok || pl.Songs != nil {
		t.Errorf("a start from the cache has %+v, %v; want it without songs, to fetch", pl, ok)
	}
}

// A like told before the cache is read stays, though Liked music's fetch
// failed first, or failed and is fetched again: the cache is older still.
func TestCacheLikesToldAfterFailure(t *testing.T) {
	for _, tc := range []struct {
		name      string
		isRetried bool
	}{{"failed", false}, {"retried", true}} {
		t.Run(tc.name, func(t *testing.T) {
			c := &fakeCache{saved: adaCached(), loadGate: make(chan struct{})}
			f, _, liked := cachedFixture(t, c)
			liked.Reply <- catalogtest.Reply{Err: errors.New("HTTP 500")}
			f.run(t)
			f.s.ApplyLikes(api.Likes{Added: []string{"new-1"}})
			if tc.isRetried {
				f.s.RetryFailed()
				f.src.Next(t, nil, "playlist", catalog.LikedID)
			}
			close(c.loadGate)
			f.run(t)
			if !f.s.IsLiked("new-1") || !f.s.IsLiked("old-1") {
				t.Errorf("likes %q, want new-1 told over the cache's", f.s.likeOrder)
			}
		})
	}
}

// listedLibrary is Ada's library as YouTube lists it: playlists without
// their songs.
func listedLibrary() catalog.Library {
	lib := adaLibrary()
	for i := range lib.Playlists {
		lib.Playlists[i].Songs = nil
	}
	return lib
}

// Wait, as a front-end exits, has the save under way end and the removal
// asked after it run, though the UI thread runs no more results.
func TestWaitForDisk(t *testing.T) {
	c := &fakeCache{saveGate: make(chan struct{})}
	f, library, liked := cachedFixture(t, c)
	f.run(t) // none to read
	library.Reply <- catalogtest.Reply{Library: adaLibrary()}
	liked.Reply <- adaLiked()
	f.run(t)
	f.run(t)
	f.s.ApplyAccount(api.AccountEvent{}) // signed out: a removal waits
	time.AfterFunc(20*time.Millisecond, func() { close(c.saveGate) })
	f.s.Wait(5 * time.Second)
	c.took(t, "load", "save", "remove")
	if c.kept() != nil {
		t.Error("the cache stayed")
	}
}

// Wait gives up on a save that hangs once the time given passed: an exit
// must not hang.
func TestWaitBounded(t *testing.T) {
	c := &fakeCache{saveGate: make(chan struct{})}
	t.Cleanup(func() { close(c.saveGate) })
	f, library, liked := cachedFixture(t, c)
	f.run(t) // none to read
	library.Reply <- catalogtest.Reply{Library: adaLibrary()}
	liked.Reply <- adaLiked()
	f.run(t)
	f.run(t)
	start := time.Now()
	f.s.Wait(50 * time.Millisecond)
	if waited := time.Since(start); waited < 50*time.Millisecond || waited > 2*time.Second {
		t.Errorf("waited %v for a save that hangs, want 50ms", waited)
	}
	c.took(t, "load")
}
