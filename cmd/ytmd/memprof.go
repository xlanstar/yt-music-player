//go:build memprof

package main

import (
	"cmp"
	"fmt"
	"net/http"
	_ "net/http/pprof" //nolint:gosec // the point of this build, on localhost
	"os"
	"runtime/metrics"

	"github.com/rs/zerolog/log"
)

// serveProfiles serves pprof, and runtime/metrics at /debug/metrics, which
// split the runtime's own memory (stacks, GC metadata) as MemStats does
// not; on 6062, as the Desktop's memprof build serves 6061, and set by a
// variable of its own, as front-ends pass ytmd their environment.
func serveProfiles() {
	addr := cmp.Or(os.Getenv("YTMD_PPROF_ADDR"), "127.0.0.1:6062")
	http.HandleFunc("/debug/metrics", writeMetrics)
	go func() {
		// Report a busy port, or profiling silently does nothing.
		if err := http.ListenAndServe(addr, nil); err != nil { //nolint:gosec // local profiling, no timeouts needed
			log.Error().Err(err).Str("addr", addr).Msg("pprof")
		}
	}()
}

// writeMetrics writes every scalar metric, one "name value" a line.
func writeMetrics(w http.ResponseWriter, _ *http.Request) {
	descs := metrics.All()
	samples := make([]metrics.Sample, len(descs))
	for i, d := range descs {
		samples[i].Name = d.Name
	}
	metrics.Read(samples)
	var b []byte
	for _, s := range samples {
		switch s.Value.Kind() {
		case metrics.KindUint64:
			b = fmt.Appendln(b, s.Name, s.Value.Uint64())
		case metrics.KindFloat64:
			b = fmt.Appendln(b, s.Name, s.Value.Float64())
		}
	}
	// A client gone: nothing to tell.
	_, _ = w.Write(b)
}
