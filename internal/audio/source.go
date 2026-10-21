package audio

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// chunkSize is the unit the source keeps, drops and reuses: 8 s at
	// 128 kbit/s.
	chunkSize = 128 << 10
	// readAhead is how far past the reader the source downloads: about a
	// minute at 128 kbit/s, half that at 256. The next request starts once
	// half of it is left, and asks for what the reader used.
	readAhead = 1 << 20
	// keepBehind is how much the source keeps behind the reader, for short
	// seeks back; one further back costs a request, as one forward does.
	keepBehind = 256 << 10
	// seekGap: a reader this far past what a request is downloading has it
	// stopped, and a request started where it reads.
	seekGap = 256 << 10
	// maxAttempts is how many times a range is asked for before the
	// source fails; a failed attempt waits retryWait, doubled each time.
	maxAttempts = 4
	retryWait   = 500 * time.Millisecond
	// requestTimeout bounds waiting for a response's headers, or its next
	// bytes.
	requestTimeout = 15 * time.Second
)

// errClosed is what reads return once the source is closed.
var errClosed = errors.New("audio: source closed")

// httpSource reads a stream through HTTP range requests, as YouTube wants
// its streams fetched. A goroutine downloads ahead of the reader into
// chunks; a read waits for its bytes. It is an io.ReadSeeker for one
// reader; Close may come from any goroutine.
type httpSource struct {
	client *http.Client
	url    string
	header http.Header
	ctx    context.Context
	cancel context.CancelFunc
	// onWait is called, outside mu, as a read starts and stops waiting
	// for the network.
	onWait func(waiting bool)

	mu   sync.Mutex
	cond sync.Cond
	size int64 // -1 until known
	// chunks are each a prefix of their chunkSize bytes, from the
	// chunk's start; those outside the window around the reader go to
	// free, for the next ones, so that a song allocates its window once.
	chunks  map[int64][]byte
	free    [][]byte
	pos     int64
	err     error // ends the stream past what is kept
	closed  bool
	waiting bool
}

// newHTTPSource starts downloading url, whose length the first response
// tells. Close it to stop.
func newHTTPSource(client *http.Client, rawURL string, header http.Header, onWait func(bool)) *httpSource {
	ctx, cancel := context.WithCancel(context.Background())
	s := &httpSource{
		client: client,
		url:    rawURL,
		header: header,
		ctx:    ctx,
		cancel: cancel,
		onWait: onWait,
		size:   -1,
		chunks: map[int64][]byte{},
	}
	s.cond.L = &s.mu
	go s.download()
	return s
}

// window is the chunks kept, [lo, hi): keepBehind behind the reader and
// readAhead past it, rounded out to whole chunks; the caller holds mu.
func (s *httpSource) window() (lo, hi int64) {
	return max(s.pos-keepBehind, 0) / chunkSize, (s.pos + readAhead + chunkSize - 1) / chunkSize
}

// chunkLen is how many bytes chunk i holds when whole; the caller holds mu.
func (s *httpSource) chunkLen(i int64) int64 {
	if s.size < 0 {
		return chunkSize
	}
	return min(chunkSize, s.size-i*chunkSize)
}

func (s *httpSource) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	s.mu.Lock()
	for {
		n, done, err := s.readLocked(p)
		if done {
			changed := s.waiting
			s.waiting = false
			s.mu.Unlock()
			if changed && s.onWait != nil {
				s.onWait(false)
			}
			return n, err
		}
		if !s.waiting {
			s.waiting = true
			if s.onWait != nil {
				// Told without mu, then checked again.
				s.mu.Unlock()
				s.onWait(true)
				s.mu.Lock()
				continue
			}
		}
		s.cond.Wait()
	}
}

// readLocked reads what is kept at the reader's position, and reports
// false when it must wait for the network; the caller holds mu.
func (s *httpSource) readLocked(p []byte) (n int, done bool, err error) {
	switch {
	case s.closed:
		return 0, true, errClosed
	case s.size >= 0 && s.pos >= s.size:
		return 0, true, io.EOF
	}
	i, off := s.pos/chunkSize, s.pos%chunkSize
	if c := s.chunks[i]; int64(len(c)) > off {
		n = copy(p, c[off:])
		s.pos += int64(n)
		// The downloader may wait for the reader to move on.
		s.cond.Broadcast()
		return n, true, nil
	}
	if s.err != nil {
		return 0, true, s.err
	}
	return 0, false, nil
}

// isWaiting reports whether a read waits for the network.
func (s *httpSource) isWaiting() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.waiting
}

func (s *httpSource) Seek(offset int64, whence int) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		offset += s.pos
	case io.SeekEnd:
		if s.size < 0 {
			return 0, errors.New("audio: seek from the end of a stream of unknown size")
		}
		offset += s.size
	default:
		return 0, fmt.Errorf("audio: seek: unknown whence %d", whence)
	}
	if offset < 0 {
		return 0, errors.New("audio: seek before the start")
	}
	s.pos = offset
	s.cond.Broadcast()
	return offset, nil
}

// Close stops the download and fails reads, the one waiting included.
func (s *httpSource) Close() error {
	s.mu.Lock()
	s.closed = true
	s.chunks, s.free = nil, nil
	s.cond.Broadcast()
	s.mu.Unlock()
	s.cancel()
	return nil
}

// download runs until Close: it fetches what the reader will want next.
func (s *httpSource) download() {
	failures := 0
	buf := make([]byte, 32<<10)
	for {
		start, end, ok := s.nextRange()
		if !ok {
			return
		}
		err := s.fetch(start, end, buf)
		switch {
		case err == nil:
			failures = 0
			continue
		case s.ctx.Err() != nil:
			return
		case errors.Is(err, errMoved):
			continue
		}
		failures++
		status, isStatus := errors.AsType[statusError](err)
		isFinal := isStatus && status.final()
		if isFinal || failures >= maxAttempts {
			s.fail(err)
			return
		}
		select {
		case <-time.After(retryWait << (failures - 1)):
		case <-s.ctx.Done():
			return
		}
	}
}

// nextRange waits until the reader wants bytes not kept, and returns the
// range to ask for: from the first missing byte at or after the reader to
// the window's end, ending before what is kept beyond.
func (s *httpSource) nextRange() (start, end int64, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		if s.closed || s.err != nil {
			return 0, 0, false
		}
		start, ok = s.firstMissing()
		if ok && start-s.pos < readAhead/2 {
			_, hi := s.window()
			end = hi * chunkSize
			if s.size >= 0 {
				end = min(end, s.size)
			}
			// Stop before the next chunk kept whole.
			for i := start/chunkSize + 1; i*chunkSize < end; i++ {
				if int64(len(s.chunks[i])) == s.chunkLen(i) {
					end = i * chunkSize
					break
				}
			}
			return start, end, true
		}
		s.cond.Wait()
	}
}

// firstMissing returns the first byte at or after the reader's chunk not
// kept, within the window, and false when there is none; the caller holds
// mu.
func (s *httpSource) firstMissing() (int64, bool) {
	_, hi := s.window()
	for i := s.pos / chunkSize; i < hi; i++ {
		if s.size >= 0 && i*chunkSize >= s.size {
			return 0, false
		}
		if n := int64(len(s.chunks[i])); n < s.chunkLen(i) {
			return i*chunkSize + n, true
		}
	}
	return 0, false
}

// errMoved stops a request the reader moved away from.
var errMoved = errors.New("audio: the reader moved")

// statusError is an HTTP status a range request was answered with.
type statusError int

func (e statusError) Error() string {
	return "audio: stream answered HTTP " + strconv.Itoa(int(e)) + " " + http.StatusText(int(e))
}

// final reports whether asking again cannot help: the URL expired or was
// refused, which playback answers by resolving the song anew.
func (e statusError) final() bool { return e >= 400 && e < 500 && e != http.StatusTooManyRequests }

// fetch downloads [start, end) into the chunks, through buf.
func (s *httpSource) fetch(start, end int64, buf []byte) error {
	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return withoutURL(err)
	}
	maps.Copy(req.Header, s.header)
	req.Header.Set("Range", "bytes="+strconv.FormatInt(start, 10)+"-"+strconv.FormatInt(end-1, 10))
	// Cancels a request whose answer or body stalls: store resets it as
	// bytes come.
	idle := time.AfterFunc(requestTimeout, cancel)
	defer idle.Stop()
	resp, err := s.client.Do(req)
	if err != nil {
		return withoutURL(err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusPartialContent:
		if err := s.learnSize(resp.Header.Get("Content-Range")); err != nil {
			return err
		}
	case http.StatusOK:
		// The whole file: only good from its start.
		if start != 0 {
			return errors.New("audio: stream ignores range requests")
		}
		if resp.ContentLength > 0 {
			s.setSize(resp.ContentLength)
		}
	default:
		return statusError(resp.StatusCode)
	}
	return s.store(resp.Body, start, end, buf, idle, cancel)
}

// learnSize reads the length off a Content-Range ("bytes 0-99/1234").
func (s *httpSource) learnSize(contentRange string) error {
	_, total, ok := strings.Cut(contentRange, "/")
	if !ok || total == "*" {
		return nil
	}
	size, err := strconv.ParseInt(total, 10, 64)
	if err != nil || size <= 0 {
		return fmt.Errorf("audio: bad Content-Range %q", contentRange)
	}
	s.setSize(size)
	return nil
}

func (s *httpSource) setSize(size int64) {
	s.mu.Lock()
	if s.size < 0 {
		s.size = size
		s.cond.Broadcast()
	}
	s.mu.Unlock()
}

// store copies body, which starts at at, into the chunks until end, or
// errMoved once the reader wants bytes elsewhere.
func (s *httpSource) store(body io.Reader, at, end int64, buf []byte, idle *time.Timer, stop func()) error {
	for at < end {
		n, err := body.Read(buf[:min(int64(len(buf)), end-at)])
		if n > 0 {
			idle.Reset(requestTimeout)
			moved, closed := s.put(at, buf[:n])
			at += int64(n)
			if closed {
				return context.Canceled
			}
			if moved {
				stop()
				return errMoved
			}
		}
		if err == io.EOF {
			if at < end && s.knownEnd() > at {
				return io.ErrUnexpectedEOF
			}
			return nil
		}
		if err != nil {
			return withoutURL(err)
		}
	}
	return nil
}

func (s *httpSource) knownEnd() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.size < 0 {
		return 1<<63 - 1
	}
	return s.size
}

// put keeps b, read at offset at, and reports whether the reader moved
// away from the request.
func (s *httpSource) put(at int64, b []byte) (moved, closed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false, true
	}
	for len(b) > 0 {
		i, off := at/chunkSize, at%chunkSize
		c := s.chunks[i]
		if int64(len(c)) != off {
			// Kept already, or a gap: chunks are prefixes.
			n := min(int64(len(b)), chunkSize-off)
			at, b = at+n, b[n:]
			continue
		}
		if c == nil {
			c = s.newChunk()
		}
		n := min(int64(len(b)), s.chunkLen(i)-off)
		s.chunks[i] = append(c, b[:n]...)
		at, b = at+n, b[n:]
	}
	s.cond.Broadcast()
	s.evict()
	// A reader behind the request, or far beyond it, waits on bytes it
	// will not bring; past the window, the bytes would be dropped.
	i, off := s.pos/chunkSize, s.pos%chunkSize
	isKept := int64(len(s.chunks[i])) > off
	readerAway := !isKept && (s.pos < at-chunkSize || s.pos > at+seekGap)
	_, hi := s.window()
	pastWindow := at > hi*chunkSize
	return readerAway || pastWindow, false
}

// newChunk returns an empty chunk, a dropped one if any; the caller holds
// mu.
func (s *httpSource) newChunk() []byte {
	if n := len(s.free); n > 0 {
		c := s.free[n-1]
		s.free = s.free[:n-1]
		return c
	}
	return make([]byte, 0, chunkSize)
}

// evict drops the chunks outside the window, for newChunk; the caller
// holds mu.
func (s *httpSource) evict() {
	lo, hi := s.window()
	for i, c := range s.chunks {
		if i < lo || i >= hi {
			delete(s.chunks, i)
			s.free = append(s.free, c[:0])
		}
	}
}

func (s *httpSource) fail(err error) {
	s.mu.Lock()
	if s.err == nil {
		s.err = err
	}
	s.cond.Broadcast()
	s.mu.Unlock()
}

// withoutURL drops the URL net/http puts in its errors: a stream URL
// carries its signature, which must not reach logs or notices.
func withoutURL(err error) error {
	if urlErr, ok := errors.AsType[*url.Error](err); ok {
		return fmt.Errorf("audio: %s: %w", urlErr.Op, urlErr.Err)
	}
	return err
}
