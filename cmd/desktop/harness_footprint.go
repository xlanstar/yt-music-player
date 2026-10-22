//go:build memprof

package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	stdlog "log"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
)

// footprintHarness measures, in the real window, the footprint as macOS
// counts it and the frames drawn, idle and playing ("Memory on macOS" in
// docs/performance.md). Its ytmd, footprint.sh's placeholder, plays silently
// in its own process, so the daemon's memory is not counted.
func footprintHarness(a *app, w *mygo.Window) {
	if os.Getenv("MYGO_FRAME_STATS") != "all" {
		fmt.Fprintln(os.Stderr, "footprint: MYGO_FRAME_STATS=all is needed to count frames")
		mygo.App.Quit()
		return
	}
	frames := &frameCounter{out: os.Stderr}
	// MyGo logs each frame through the standard logger.
	stdlog.SetOutput(frames)
	h := &footprintRun{a: a, w: w, frames: frames}
	go h.run()
}

type footprintRun struct {
	a      *app
	w      *mygo.Window
	frames *frameCounter
	phases []footprintPhase
	notes  []string
}

const (
	// sampleEvery is how often measure samples the footprint (footprint(1)
	// takes about 15 ms): often enough to catch Metal's ~1 s hold after
	// each frame several times.
	sampleEvery = 200 * time.Millisecond
	// The position's steps (playback.go). A playing phase
	// measures whole steps, as the footprint rises with each step's frame
	// and falls back.
	focusedTick = focusedStep * time.Second
	blurredTick = blurredStep * time.Second
)

type windowState uint8

const (
	stateFocused   windowState = iota // shown, with the keyboard focus
	stateUnfocused                    // shown, without it
	stateShown                        // shown, focused or not
	stateHidden                       // minimized
)

// holds reports whether every sample of p found the window in state s.
func (s windowState) holds(p footprintPhase) bool {
	n := len(p.mb)
	switch s {
	case stateFocused:
		return p.focused == n && p.shown == n
	case stateUnfocused:
		return p.focused == 0 && p.shown == n
	case stateShown:
		return p.shown == n
	}
	return p.shown == 0
}

type footprintPhase struct {
	name           string
	focused, shown int // samples that found the window so
	secs           float64
	frameMS        []float64
	mb, graphics   []float64 // footprint samples and their unmapped graphics part, in MB
}

func (h *footprintRun) run() {
	w := h.w
	// Focused phases come first, while the app still has its launch focus:
	// macOS may not give it back later (see focus).
	h.focus()
	w.Update(func() { h.a.router.Push(pathHome) })
	h.settle(time.Minute)
	// Idle phases measure 3 s after settle's 3 s without a frame: long
	// enough to catch focused-step frames wrongly drawn when nothing plays.
	h.measure("idle", 3*time.Second, stateShown)

	h.oneFrame()

	h.focus()
	w.Update(func() { h.a.playAll(h.a.cat.SongIDs()) })
	h.waitFor(10*time.Second, func() bool { return h.position() > 0 })
	// Wait out the song-start and focus animations, which draw far more
	// often than a step.
	h.quiet(focusedTick/2, 10*time.Second)
	h.align(focusedTick)
	h.measure("playing-focused", 5*focusedTick, stateFocused)

	w.Update(h.a.client.Pause)
	h.settle(30 * time.Second)
	h.measure("paused", 3*time.Second, stateShown)

	idle := mean(h.phases[0].mb)
	paths := onUI(func() []string { return harnessPaths(h.a) })
	for _, p := range paths {
		w.Update(func() { h.a.router.Push(p) })
		h.shown(p)
	}
	w.Update(func() { h.a.router.Push(pathHome) })
	h.settle(time.Minute)
	h.measure("idle-after-pages", 3*time.Second, stateShown)
	grew := mean(h.phases[len(h.phases)-1].mb) - idle
	h.note("pages: %d shown, idle footprint %+.0f MB, %+.1f MB a page", len(paths), grew, grew/float64(len(paths)))

	w.Update(h.a.client.Resume)
	yieldFocus()
	if err := exec.CommandContext(context.Background(), "open", "-a", "Finder").Run(); err != nil {
		h.note("could not focus Finder: %v", err)
	}
	h.waitFor(10*time.Second, func() bool { return !w.IsFocused() })
	// Wait out the frames of losing focus and Metal's hold for them, so
	// the mean has only the steps' own frames and holds.
	h.settle(15 * time.Second)
	h.align(blurredTick)
	h.measure("playing-unfocused", 2*blurredTick, stateUnfocused)

	w.Minimize()
	h.waitFor(10*time.Second, w.IsMinimized)
	h.settle(15 * time.Second)
	// Longer than a blurred step, to show none is drawn while minimized.
	h.measure("playing-minimized", blurredTick+time.Second, stateHidden)

	w.Update(h.a.client.Pause)
	w.Restore()
	if _, err := os.Stdout.WriteString(h.report()); err != nil {
		fmt.Fprintln(os.Stderr, "footprint:", err)
	}
	mygo.App.Quit()
}

// focus asks for the keyboard focus and waits for it: since macOS 14 an
// app may not take it from the one the user is in, so a click on the
// window may be needed.
func (h *footprintRun) focus() {
	h.w.Focus()
	for i := range 600 {
		if i == 10 {
			fmt.Fprintln(os.Stderr, "footprint: waiting for the focus: click the window")
		}
		if h.w.IsFocused() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// waitFor doesn't report a timeout: measure notes a phase whose window
// state was wrong.
func (h *footprintRun) waitFor(limit time.Duration, cond func() bool) {
	for end := time.Now().Add(limit); !cond() && time.Now().Before(end); {
		time.Sleep(50 * time.Millisecond)
	}
}

// settle waits until no frame came for 3 s, at most limit: Metal holds
// about 250 MB for about 1 s after a frame (see oneFrame), and the
// footprint is back about 2 s after it.
func (h *footprintRun) settle(limit time.Duration) { h.quiet(3*time.Second, limit) }

func (h *footprintRun) quiet(d, limit time.Duration) {
	for end := time.Now().Add(limit); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if last := h.frames.lastFrame(); !last.IsZero() && time.Since(last) >= d {
			return
		}
	}
}

// shown waits until the page at path drew all it loads: no fetch or
// thumbnail in flight and no frame for a moment. At most 5 s, enough for
// thumbnails over the network (-live).
func (h *footprintRun) shown(path string) {
	start := time.Now()
	busy := func() bool {
		return h.a.loader.IsBusy() || h.a.art != nil && h.a.art.IsBusy() || h.a.session.IsBusy()
	}
	for time.Since(start) < 5*time.Second {
		last := h.frames.lastFrame()
		if last.Before(start) {
			last = start
		}
		if time.Since(last) >= 150*time.Millisecond && !onUI(busy) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	fmt.Fprintln(os.Stderr, "footprint: still loading after 5 s:", path)
}

// position is -1 before a song plays.
func (h *footprintRun) position() time.Duration {
	return onUI(func() time.Duration {
		st := h.a.mirror.State()
		if !st.Playing || st.Waiting {
			return -1
		}
		return time.Duration(h.a.mirror.Position(time.Now()) * float64(time.Second))
	})
}

// align waits until the position is clear of a step boundary, where
// playback draws a frame, so a measure of whole steps counts each step's
// frame once, whatever its lag.
func (h *footprintRun) align(step time.Duration) {
	const margin = 250 * time.Millisecond
	switch frac := h.position() % step; {
	case frac < 0:
	case frac < margin:
		time.Sleep(margin - frac)
	case frac > step-margin:
		time.Sleep(step - frac + margin)
	}
}

// measure notes a phase invalid whose window wasn't in state want at
// every sample, as after a user's click: its numbers measure another case.
func (h *footprintRun) measure(name string, d time.Duration, want windowState) {
	phase := footprintPhase{name: name}
	h.frames.take()
	start := time.Now()
	// Evenly spaced samples and an end at d sharp: a playing phase's
	// samples and frames cover whole steps.
	for i := time.Duration(0); i*sampleEvery < d; i++ {
		time.Sleep(time.Until(start.Add(i * sampleEvery)))
		phase.focused += boolToInt(h.w.IsFocused())
		phase.shown += boolToInt(h.w.IsVisible() && !h.w.IsMinimized())
		mb, gfx := sampleFootprint()
		phase.mb, phase.graphics = append(phase.mb, mb), append(phase.graphics, gfx)
	}
	time.Sleep(time.Until(start.Add(d)))
	phase.frameMS = h.frames.take()
	phase.secs = time.Since(start).Seconds()
	if !want.holds(phase) {
		h.note(
			"invalid: %s: the window was not as the phase needs: focused %s, shown %s",
			name,
			share(phase.focused, len(phase.mb)),
			share(phase.shown, len(phase.mb)),
		)
	}
	h.phases = append(h.phases, phase)
}

// oneFrame draws a single frame in an idle window and notes how much the
// footprint grew after it, and for how long.
func (h *footprintRun) oneFrame() {
	base, _ := sampleFootprint()
	h.frames.take()
	start := time.Now()
	h.w.Invalidate()
	var peak, held float64
	for time.Since(start) < 5*time.Second {
		mb, _ := sampleFootprint()
		peak = max(peak, mb)
		rose := peak-base > 20
		at := time.Since(start).Seconds()
		// Held while above halfway between base and peak.
		if rose && mb-base > (peak-base)/2 {
			held = at
		}
		// Back at base for a while, so a late frame would have shown.
		if rose && mb-base < 5 && at > held+0.5 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	ms := h.frames.take()
	h.note(
		"one frame: %d drawn (%s ms), footprint %.0f MB, %+.0f MB at its peak, held about %.1f s",
		len(ms),
		joinMS(ms),
		base,
		peak-base,
		held,
	)
}

func (h *footprintRun) note(format string, args ...any) {
	h.notes = append(h.notes, fmt.Sprintf(format, args...))
}

// report is built whole, then printed once, so there is one write to check.
func (h *footprintRun) report() string {
	var out strings.Builder
	width, height := h.w.ContentSize()
	fmt.Fprintf(&out, "# window: %dx%d points\n", width, height)
	// MB-mean is of 5 samples a second: the footprint over time, as it
	// rises with each frame and falls back. Self-describing lines without
	// column widths: a baseline's diff shows the phases that changed alone.
	out.WriteString("# phases: one line each, key=value; frame-ms: frame times; MB: footprint(1)'s, sampled 5 times a second; gfx-MB: its unmapped graphics part\n")
	for _, p := range h.phases {
		n := len(p.mb)
		fmt.Fprintf(&out, "phase=%s focus=%s shown=%s secs=%.1f frames=%d frames/min=%.1f frame-ms-median=%.1f frame-ms-max=%.1f MB-min=%.0f MB-mean=%.0f MB-max=%.0f gfx-MB-mean=%.0f gfx-MB-max=%.0f\n",
			p.name, share(p.focused, n), share(p.shown, n), p.secs, len(p.frameMS), float64(len(p.frameMS))/p.secs*60,
			median(p.frameMS), slices.Max(append(p.frameMS, 0)),
			slices.Min(p.mb), mean(p.mb), slices.Max(p.mb), mean(p.graphics), slices.Max(p.graphics))
	}
	for _, n := range h.notes {
		fmt.Fprintf(&out, "# %s\n", n)
	}
	out.WriteString("# end\n")
	return out.String()
}

// sampleFootprint returns the process's footprint and its "Owned physical
// footprint (unmapped) (graphics)" part, in MB, as footprint(1) has them.
func sampleFootprint() (total, graphics float64) {
	pid := strconv.Itoa(os.Getpid())
	//nolint:gosec // footprint(1) on this process
	out, err := exec.CommandContext(context.Background(), "footprint", "-f", "bytes", "-p", pid).Output()
	if err != nil {
		fmt.Fprintln(os.Stderr, "footprint:", err)
		return 0, 0
	}
	for line := range strings.Lines(string(out)) {
		if _, after, ok := strings.Cut(line, "Footprint: "); ok {
			_, _ = fmt.Sscan(after, &total)
		}
		if strings.HasSuffix(strings.TrimSpace(line), "Owned physical footprint (unmapped) (graphics)") {
			_, _ = fmt.Sscan(line, &graphics)
		}
	}
	return total / (1 << 20), graphics / (1 << 20)
}

// frameCounter reads MyGo's frame log (MYGO_FRAME_STATS=all) on its way
// to out: when each frame was drawn and how long it took.
type frameCounter struct {
	out  io.Writer
	mu   sync.Mutex
	last time.Time
	ms   []float64
}

func (f *frameCounter) Write(b []byte) (int, error) {
	if _, frame, ok := bytes.Cut(b, []byte("mygo: frame ")); ok {
		var n int
		var ms float64
		if _, err := fmt.Sscanf(string(frame), "%d took %g ms", &n, &ms); err == nil {
			f.mu.Lock()
			f.last, f.ms = time.Now(), append(f.ms, ms)
			f.mu.Unlock()
		}
	}
	return f.out.Write(b)
}

// take returns the frame times, in ms, since the last take.
func (f *frameCounter) take() []float64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	ms := f.ms
	f.ms = nil
	return ms
}

// lastFrame is zero before any frame.
func (f *frameCounter) lastFrame() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.last
}

func onUI[T any](f func() T) T {
	var v T
	mygo.RunOnMain(func() { v = f() })
	return v
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := slices.Sorted(slices.Values(v))
	return s[len(s)/2]
}

func joinMS(ms []float64) string {
	parts := make([]string, len(ms))
	for i, m := range ms {
		parts[i] = strconv.FormatFloat(m, 'f', 1, 64)
	}
	return strings.Join(parts, ", ")
}

func share(n, all int) string {
	switch n {
	case all:
		return "yes"
	case 0:
		return "no"
	}
	return "part"
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func mean(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	var sum float64
	for _, x := range v {
		sum += x
	}
	return sum / float64(len(v))
}
