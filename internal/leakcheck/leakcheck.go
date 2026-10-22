// Package leakcheck fails a test binary whose tests leave goroutines
// blocked for good, as Go's goroutineleak profile finds them: blocked on a
// channel, mutex or condition no running goroutine or global can reach.
// Goroutines that could still wake (a timer, a socket) are not leaks to it.
package leakcheck

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"runtime/pprof"
	"testing"
)

// Run runs m's tests and returns their exit code, or 1 when they passed
// but leaked: call it from TestMain, as os.Exit(leakcheck.Run(m)).
func Run(m *testing.M) int {
	code := m.Run()
	if code != 0 {
		return code
	}
	n, err := Report(os.Stderr)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "leakcheck:", err)
		return 1
	}
	if n > 0 {
		return 1
	}
	return 0
}

// Report runs a leak-detecting GC and writes the stacks of the goroutines
// it finds leaked to w, returning how many there are.
func Report(w io.Writer) (int, error) {
	p := pprof.Lookup("goroutineleak")
	var stacks bytes.Buffer
	// Writing the profile is what runs the detecting GC; Count only reads.
	if err := p.WriteTo(&stacks, 1); err != nil {
		return 0, err
	}
	n := p.Count()
	if n == 0 {
		return 0, nil
	}
	_, err := fmt.Fprintf(w, "leakcheck: %d goroutines leaked\n%s", n, stacks.Bytes())
	return n, err
}
