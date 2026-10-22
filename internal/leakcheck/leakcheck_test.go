package leakcheck

import (
	"io"
	"strings"
	"testing"
	"time"
)

// This binary's TestMain is the default one: the test below leaks on
// purpose, once per run (-count).
func TestReport(t *testing.T) {
	before, err := Report(io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	go func() {
		close(started)
		<-make(chan struct{})
	}()
	<-started
	// The goroutine blocks just after started closes.
	deadline := time.Now().Add(5 * time.Second)
	var out strings.Builder
	for n, _ := Report(&out); n == before; n, _ = Report(&out) {
		if time.Now().After(deadline) {
			t.Fatal("the leak went unreported")
		}
		out.Reset()
		time.Sleep(time.Millisecond)
	}
	if !strings.Contains(out.String(), "leakcheck.TestReport.func1") {
		t.Errorf("the report misses the leaked goroutine's stack:\n%s", out.String())
	}
}
