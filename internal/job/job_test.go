package job

import (
	"context"
	"errors"
	"testing"
	"time"
)

// posts stands in for the UI thread: the test runs queued results itself.
type posts chan func()

func (q posts) post(fn func()) { q <- fn }

// held waits for one result and returns it to run later.
func held(t *testing.T, queue posts) func() {
	t.Helper()
	select {
	case fn := <-queue:
		return fn
	case <-time.After(5 * time.Second):
		t.Fatal("no result came back")
		return nil
	}
}

// answered is work that waits for its answer and hands its context to the
// test. Like a source, it answers even once canceled.
func answered(ctxs chan<- context.Context, answers <-chan string) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		ctxs <- ctx
		answer := <-answers
		if answer == "" {
			return "", errors.New("failed")
		}
		return answer, nil
	}
}

func TestRunBringsResult(t *testing.T) {
	var j Job
	queue, ctxs, answers := make(posts, 1), make(chan context.Context, 1), make(chan string, 1)
	var got string
	var gotErr error
	Run(&j, queue.post, answered(ctxs, answers), func(v string, err error) { got, gotErr = v, err })
	ctx := <-ctxs
	if !j.IsRunning() {
		t.Fatal("not running")
	}
	answers <- "done"
	held(t, queue)()
	if got != "done" || gotErr != nil || j.IsRunning() || ctx.Err() == nil {
		t.Errorf("got %q, %v; running %v; context %v", got, gotErr, j.IsRunning(), ctx.Err())
	}

	Run(&j, queue.post, answered(ctxs, answers), func(v string, err error) { got, gotErr = v, err })
	<-ctxs
	answers <- ""
	held(t, queue)()
	if gotErr == nil {
		t.Error("no error")
	}
}

func TestStaleResultsDropped(t *testing.T) {
	var j Job
	queue, ctxs, answers := make(posts, 2), make(chan context.Context, 2), make(chan string, 2)
	var got []string
	done := func(v string, _ error) { got = append(got, v) }

	// Stopped while its result is already posted.
	Run(&j, queue.post, answered(ctxs, answers), done)
	first := <-ctxs
	answers <- "first"
	finish := held(t, queue)
	j.Stop()
	if first.Err() == nil || j.IsRunning() {
		t.Fatal("Stop left the run going")
	}
	finish()

	// Run again while the previous result is still posted: only the last counts.
	Run(&j, queue.post, answered(ctxs, answers), done)
	<-ctxs
	answers <- "second"
	finish = held(t, queue)
	Run(&j, queue.post, answered(ctxs, answers), done)
	<-ctxs
	finish()
	answers <- "third"
	held(t, queue)()
	if len(got) != 1 || got[0] != "third" {
		t.Errorf("kept %q, want only the last run's", got)
	}
}

func TestCanceledRunPostsNothing(t *testing.T) {
	var j Job
	queue, ctxs, answers := make(posts, 1), make(chan context.Context, 1), make(chan string, 1)
	Run(&j, queue.post, answered(ctxs, answers), func(string, error) { t.Error("a stopped run finished") })
	<-ctxs
	j.Stop()
	answers <- "late"
	select {
	case fn := <-queue:
		fn()
	case <-time.After(30 * time.Millisecond):
	}
}
