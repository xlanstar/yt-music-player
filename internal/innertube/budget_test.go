package innertube

import (
	"net/http"
	"testing"
	"unsafe"
	"yt-music-player/internal/budget"
)

func TestBudgetPayload(t *testing.T) {
	c := benchClient()
	body := benchBodies[0].body
	// 10 allocs today: the body's copy and encoding.
	budget.Allocs(t, 12, 100, func() { sinkBytes, _ = c.payload(body) })
}

func TestBudgetContext(t *testing.T) {
	var rc requestContext
	if got, want := unsafe.Sizeof(rc), unsafe.Sizeof(rc.Client); got != want {
		t.Errorf("context is %d bytes, client is %d; keep User before Client", got, want)
	}
	c := benchClient()
	// 0 allocs today: a pinned version.
	budget.Allocs(t, 0, 100, func() { sinkCtx = c.context() })
}

func TestBudgetAuthSet(t *testing.T) {
	var a Auth
	sign := func(*http.Request) error { return nil }
	// 1 alloc today: the func's box.
	budget.Allocs(t, 1, 100, func() { a.Set(sign) })
}

func TestBudgetErrorMessage(t *testing.T) {
	body := []byte(`{"error":{"code":400,"message":"Request contains an invalid argument."}}`)
	// 1 alloc today: the message.
	budget.Allocs(t, 1, 100, func() { sinkString = errorMessage(body) })
}

func TestBudgetRetryAfter(t *testing.T) {
	// 0 allocs today.
	budget.Allocs(t, 0, 100, func() { sinkWait = retryAfter("5") })
}
