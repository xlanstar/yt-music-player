package loader

import (
	"strconv"
	"testing"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/fetchstate"
)

// Sinks keep the compiler from optimizing measured calls away.
var (
	sinkFound Found
	sinkBool  bool
	sinkItems []catalog.Item
)

// idleLoader is a Loader as a build finds it: an album page shown and
// loaded, feeds and a search done, over a placeholder without delay.
func idleLoader(tb testing.TB) (*Loader, posts) {
	tb.Helper()
	cat, queue := catalog.Placeholder(), make(posts, 16)
	l := New(catalog.NewPlaceholderSource(0), &fetchstate.Registry{}, cat, func(fn func()) { queue <- fn })
	l.Debounce = 0
	for _, res := range []fetchstate.Resource{HomeRes, ExploreRes, benchAlbum} {
		l.Show(res)
		(<-queue)()
	}
	l.Search("lumen")
	(<-queue)()
	if l.fetches.Get(benchAlbum).State != fetchstate.Loaded || l.Found().Query != "lumen" {
		tb.Fatalf("not loaded: %+v, found %q", l.fetches.Get(benchAlbum), l.Found().Query)
	}
	return l, queue
}

var benchAlbum = fetchstate.Resource{Kind: fetchstate.Album, ID: "neon-tide"}

// BenchmarkShow covers the same page shown again (every router change
// that keeps the resource) and a refetch from a source without delay.
func BenchmarkShow(b *testing.B) {
	l, queue := idleLoader(b)
	b.Run("unchanged", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			l.Show(benchAlbum)
		}
	})
	b.Run("fetch", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			l.Retry(benchAlbum)
			(<-queue)()
		}
	})
}

// BenchmarkFound is read by the search page in every build.
func BenchmarkFound(b *testing.B) {
	l, _ := idleLoader(b)
	b.ReportAllocs()
	for b.Loop() {
		sinkFound = l.Found()
	}
}

// BenchmarkSearch covers the shown query again and a new query searched
// without debounce.
func BenchmarkSearch(b *testing.B) {
	l, queue := idleLoader(b)
	b.Run("unchanged", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			l.Search("lumen")
		}
	})
	// Outside the closure because -count reruns it on the same loader: an
	// odd run would end on "orbit", and the next would search for the
	// shown query, post nothing and block forever.
	queries := [2]string{"orbit", "lumen"}
	searches := 0
	b.Run("new", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			l.Search(queries[searches%2])
			(<-queue)()
			searches++
		}
	})
}

// benchHome is a home feed of shelves with perShelf items each, some
// repeated, as real feeds repeat items.
func benchHome(shelves, perShelf int) catalog.Home {
	var home catalog.Home
	for s := range shelves {
		shelf := catalog.Shelf{Title: "Shelf " + strconv.Itoa(s)}
		for i := range perShelf {
			id := strconv.Itoa(s*perShelf + i/2)
			switch i % 3 {
			case 0:
				shelf.Items = append(shelf.Items, catalog.Item{Kind: catalog.ItemSong, Song: catalog.Song{ID: "s" + id, Title: "Song " + id}})
			case 1:
				shelf.Items = append(shelf.Items, catalog.Item{Kind: catalog.ItemAlbum, Album: catalog.Album{ID: "MPREb_" + id, Title: "Album " + id}})
			default:
				shelf.Items = append(shelf.Items, catalog.Item{Kind: catalog.ItemPlaylist, Playlist: catalog.Playlist{ID: "RD" + id, Name: "Mix " + id}})
			}
		}
		home.Shelves = append(home.Shelves, shelf)
	}
	return home
}

// homeCases are a YouTube Music-sized feed and the scaled case. Allocs
// today are the catalog's, as it adds the feed's music, and shelfMusic's
// three slices.
var homeCases = []struct {
	name       string
	shelves, n int
	limit      float64
}{
	{"feed", 6, 16, 25},   // 21 allocs today.
	{"2000", 10, 200, 38}, // 33 allocs today.
}

func BenchmarkKeepHome(b *testing.B) {
	for _, tc := range homeCases {
		b.Run(tc.name, func(b *testing.B) {
			cat := catalog.New(nil, nil, nil)
			l := New(nil, &fetchstate.Registry{}, cat, nil)
			home := benchHome(tc.shelves, tc.n)
			// Each call takes a clone (see cloneHome), made a batch at a
			// time off the timer: stopping it reads MemStats, a
			// stop-the-world.
			fresh := make([]catalog.Home, 16)
			b.ReportAllocs()
			for i := 0; b.Loop(); i++ {
				if i%len(fresh) == 0 {
					b.StopTimer()
					for k := range fresh {
						fresh[k] = cloneHome(home)
					}
					b.StartTimer()
				}
				l.KeepHome(fresh[i%len(fresh)])
			}
		})
	}
}

// cloneHome copies the item slices because Unique changes them in place.
func cloneHome(home catalog.Home) catalog.Home {
	clone := catalog.Home{Shelves: make([]catalog.Shelf, len(home.Shelves))}
	for i, shelf := range home.Shelves {
		shelf.Items = append([]catalog.Item(nil), shelf.Items...)
		clone.Shelves[i] = shelf
	}
	return clone
}

func BenchmarkUnique(b *testing.B) {
	items := benchHome(1, 2000).Shelves[0].Items
	buf := make([]catalog.Item, len(items))
	b.ReportAllocs()
	for b.Loop() {
		copy(buf, items)
		sinkItems = Unique(buf[:len(items)], func(item catalog.Item) string { return item.Key() })
	}
}
