package job

import (
	"context"
	"testing"
)

// Sinks keep the compiler from optimizing measured calls away.
var (
	sinkBool   bool
	sinkString string
)

func instant(context.Context) (string, error) { return "done", nil }

// roundTrip costs a fetch whose source answers at once: the overhead a
// page fetch or search adds to the source's own.
func roundTrip(j *Job, queue posts) {
	Run(j, queue.post, instant, func(v string, _ error) { sinkString = v })
	(<-queue)()
}

func BenchmarkRun(b *testing.B) {
	var j Job
	queue := make(posts, 1)
	b.ReportAllocs()
	for b.Loop() {
		roundTrip(&j, queue)
	}
}

func BenchmarkRunning(b *testing.B) {
	var j Job
	b.ReportAllocs()
	for b.Loop() {
		sinkBool = j.IsRunning()
	}
}

func BenchmarkStop(b *testing.B) {
	var j Job
	b.ReportAllocs()
	for b.Loop() {
		j.Stop()
	}
}
