package loop

import (
	"slices"
	"sync"
	"testing"
	"time"
)

// started runs l on a goroutine until the test ends.
func started(t *testing.T, l *Loop) *Loop {
	t.Helper()
	done := run(l)
	t.Cleanup(func() {
		l.Stop()
		<-done
	})
	return l
}

// run runs l on a goroutine, closing the channel it returns once Run
// returned.
func run(l *Loop) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		l.Run()
		close(done)
	}()
	return done
}

// flush waits until every func posted so far ran.
func flush(t *testing.T, l *Loop) {
	t.Helper()
	ran := make(chan struct{})
	l.Post(func() { close(ran) })
	select {
	case <-ran:
	case <-time.After(5 * time.Second):
		t.Fatal("the loop did not run")
	}
}

func TestRunsInOrderOnOneGoroutine(t *testing.T) {
	var got []int // touched only on the loop
	l := started(t, New(nil))
	var wg sync.WaitGroup
	for g := range 4 {
		wg.Go(func() {
			for i := range 100 {
				l.Post(func() { got = append(got, g*1000+i) })
			}
		})
	}
	wg.Wait()
	flush(t, l)
	if len(got) != 400 {
		t.Fatalf("ran %d funcs, want 400", len(got))
	}
	// Each poster's funcs run in the order it posted them.
	for g := range 4 {
		var mine []int
		for _, v := range got {
			if v/1000 == g {
				mine = append(mine, v%1000)
			}
		}
		if !slices.IsSorted(mine) {
			t.Errorf("poster %d's funcs ran out of order: %v", g, mine)
		}
	}
}

func TestAfterRunsAfterEachBatch(t *testing.T) {
	var log []string
	l := New(func() { log = append(log, "after") })
	// Queued before Run: one batch.
	l.Post(func() { log = append(log, "a") })
	l.Post(func() {
		log = append(log, "b")
		// Posted while the batch runs: the next batch.
		l.Post(func() { log = append(log, "c") })
	})
	started(t, l)
	flush(t, l)
	got := make(chan []string, 1)
	l.Post(func() { got <- slices.Clone(log) })
	want := []string{"a", "b", "after", "c", "after"}
	// flush's own func joins c's batch or makes one of its own.
	if ran := <-got; len(ran) < len(want) || !slices.Equal(ran[:len(want)], want) {
		t.Errorf("ran %q, want %q first", ran, want)
	}
}

func TestStop(t *testing.T) {
	l := New(func() { t.Error("after ran past Stop") })
	ran := []string{}
	l.Post(func() {
		ran = append(ran, "stopping")
		l.Stop()
	})
	l.Post(func() { ran = append(ran, "dropped") })
	done := run(l)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after Stop")
	}
	if !slices.Equal(ran, []string{"stopping"}) {
		t.Errorf("ran %q", ran)
	}
	// Posting after Stop drops the func, never blocking.
	l.Post(func() { t.Error("ran after Stop") })
	l.Stop()
}

func TestStopFromOutside(t *testing.T) {
	l := New(nil)
	done := run(l)
	l.Stop()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after Stop")
	}
}
