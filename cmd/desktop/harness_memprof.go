//go:build memprof

package main

import (
	"cmp"
	"fmt"
	"net/http"
	_ "net/http/pprof" //nolint:gosec // the point of this build, on localhost
	"os"
	"runtime"
	"time"

	"github.com/egoist/mygo"
	"github.com/rs/zerolog/log"
)

func init() {
	// YT_PPROF_ADDR lets two builds be measured at once without one's
	// harness driving the other's app.
	addr := cmp.Or(os.Getenv("YT_PPROF_ADDR"), "127.0.0.1:6061")
	go func() {
		// Report a busy port, or profiling silently does nothing.
		if err := http.ListenAndServe(addr, nil); err != nil { //nolint:gosec // local profiling, no timeouts needed
			log.Error().Err(err).Str("addr", addr).Msg("pprof")
		}
	}()
}

// harness drives the UI like a user for a while, then idles; with
// YT_HARNESS=footprint it measures the app instead (harness_footprint.go).
func harness(a *app, w *mygo.Window) {
	switch os.Getenv("YT_HARNESS") {
	case "":
		return
	case "footprint":
		footprintHarness(a, w)
		return
	}
	go logMemStats()
	// Listed here, on the UI thread: fetches change the catalog there
	// while the harness runs.
	paths := harnessPaths(a)
	go drive(a, w, paths)
}

func logMemStats() {
	for range time.Tick(2 * time.Second) {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		fmt.Fprintf(
			os.Stderr,
			"mem heapAlloc=%dMB inuse=%dMB idle=%dMB released=%dMB sys=%dMB numGC=%d\n",
			m.HeapAlloc>>20, m.HeapInuse>>20, m.HeapIdle>>20, m.HeapReleased>>20, m.Sys>>20, m.NumGC,
		)
	}
}

func drive(a *app, w *mygo.Window, paths []string) {
	time.Sleep(2 * time.Second)
	for round := range 3 {
		for i, path := range paths {
			w.Update(func() {
				a.router.Push(path)
				if i%4 == 0 {
					a.panel.isShown = !a.panel.isShown
				}
				if i%3 == 0 {
					a.player.Next()
					a.player.Playing = true
				}
			})
			if i%7 == 0 {
				size := [][2]int{{1360, 860}, {1100, 700}, {1600, 1000}}[(i/7+round)%3]
				w.SetSize(size[0], size[1])
			}
			time.Sleep(350 * time.Millisecond)
		}
	}
	w.Update(func() {
		a.router.Push(pathHome)
		a.panel.isShown = true
		a.player.Playing = false
	})
	w.SetSize(1360, 860)
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Fprintf(
		os.Stderr,
		"HARNESS DONE heapAlloc=%dMB heapSys=%dMB released=%dMB\n",
		m.HeapAlloc>>20, m.HeapSys>>20, m.HeapReleased>>20,
	)
}

// harnessPaths is UI-thread only.
func harnessPaths(a *app) []string {
	paths := []string{pathHome, pathExplore, pathLibrary}
	for _, al := range a.cat.Albums {
		paths = append(paths, albumPath(al.ID))
	}
	paths = append(paths, pathLiked)
	for _, pl := range a.cat.Playlists {
		paths = append(paths, playlistPath(pl.ID))
	}
	return append(paths, searchPath("lumen"), pathHome)
}
