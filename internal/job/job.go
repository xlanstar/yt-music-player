// Package job runs slow work, such as a fetch, off the UI thread and brings
// its result back, dropping results of work that was stopped or restarted
// meanwhile (a source may still answer after cancellation).
//
// A Job belongs to the UI thread. The work runs on its own goroutine and
// must not touch UI-owned state such as the catalog: it returns plain
// values, and done stores them on the UI thread.
package job

import "context"

// Job is work with at most one run in flight; its zero value is ready.
type Job struct {
	// ctx identifies the run in flight; a result from another run is stale.
	ctx    context.Context
	cancel context.CancelFunc
}

// IsRunning reports whether a run is in flight.
func (j *Job) IsRunning() bool { return j.cancel != nil }

// Stop cancels the run in flight and drops its result.
func (j *Job) Stop() {
	if j.cancel == nil {
		return
	}
	j.cancel()
	j.ctx, j.cancel = nil, nil
}

// Run stops j's run in flight and starts work on a goroutine. post runs a
// func on the UI thread; through it done gets the result unless j was
// stopped or run again meanwhile. The context is canceled when the run ends.
func (j *Job) Run[T any](post func(func()), work func(context.Context) (T, error), done func(T, error)) {
	j.Stop()
	ctx, cancel := context.WithCancel(context.Background())
	j.ctx, j.cancel = ctx, cancel
	go func() {
		result, err := work(ctx)
		if ctx.Err() != nil {
			return
		}
		post(func() {
			// Drops runs stopped after the ctx check above.
			if j.ctx != ctx {
				return
			}
			j.Stop()
			done(result, err)
		})
	}()
}
