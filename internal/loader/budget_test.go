package loader

import (
	"testing"
	"yt-music-player/internal/budget"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/fetchstate"
)

// TestBudgetIdle covers what every build and router change asks of the
// loader.
func TestBudgetIdle(t *testing.T) {
	l, _ := idleLoader(t)
	for _, tc := range []struct {
		name string
		call func()
	}{
		// 0 allocs today for each.
		{"Show/unchanged", func() { l.Show(benchAlbum) }},
		{"Search/unchanged", func() { l.Search("lumen") }},
		{"Found", func() { sinkFound = l.Found() }},
		{"Busy", func() { sinkBool = l.IsBusy() }},
	} {
		t.Run(tc.name, func(t *testing.T) { budget.Allocs(t, 0, 100, tc.call) })
	}
}

// TestBudgetFetch covers a fetch and a search from a source without
// delay: the job's allocations plus the placeholder's.
func TestBudgetFetch(t *testing.T) {
	l, queue := idleLoader(t)
	t.Run("Retry", func(t *testing.T) {
		budget.Allocs(t, 12, 100, func() { // 10 allocs today.
			l.Retry(benchAlbum)
			(<-queue)()
		})
	})
	queries := [2]string{"orbit", "lumen"}
	searches := 0
	t.Run("Search", func(t *testing.T) {
		budget.Allocs(t, 11, 100, func() { // 9 allocs today.
			l.Search(queries[searches%2])
			(<-queue)()
			searches++
		})
	})
}

func TestBudgetKeepHome(t *testing.T) {
	for _, tc := range homeCases {
		t.Run(tc.name, func(t *testing.T) {
			l := New(nil, &fetchstate.Registry{}, catalog.New(nil, nil, nil), nil)
			home := benchHome(tc.shelves, tc.n)
			fresh := make([]catalog.Home, 0, 21)
			for range cap(fresh) {
				fresh = append(fresh, cloneHome(home))
			}
			budget.Allocs(t, tc.limit, 20, func() {
				l.KeepHome(fresh[0])
				fresh = fresh[1:]
			})
		})
	}
}

func TestBudgetUnique(t *testing.T) {
	items := benchHome(1, 2000).Shelves[0].Items
	buf := make([]catalog.Item, len(items))
	// 9 allocs today: the map of seen keys.
	budget.Allocs(t, 11, 100, func() {
		copy(buf, items)
		sinkItems = Unique(buf, func(item catalog.Item) string { return item.Key() })
	})
}
