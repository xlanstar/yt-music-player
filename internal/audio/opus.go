package audio

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"sync"
	"sync/atomic"
	"time"
	"yt-music-player/internal/webm"

	"github.com/pion/opus"
)

const (
	// Opus decodes at 48 kHz, MyGo's output rate: nothing resamples.
	opusRate = 48000
	// maxOpusFrame is the most samples per channel one packet decodes to:
	// 120 ms.
	maxOpusFrame = opusRate * 120 / 1000
	// defaultPreRoll is decoded before a seek's target when the file names
	// none: Opus needs 80 ms to converge.
	defaultPreRoll = 80 * time.Millisecond
)

// errChannels is an Opus stream of more than two channels, which YouTube
// does not serve.
var errChannels = errors.New("audio: Opus of more than two channels")

// opusStream decodes a WebM file's Opus track as MyGo's audio.Decoder
// asks, always in stereo. MyGo calls Read and SetPosition from one
// goroutine at a time; decoded and Err are read from others.
type opusStream struct {
	r   *webm.Reader
	dec opus.Decoder
	src io.Closer

	length  int64 // frames, -1 if unknown
	preSkip int64 // frames decoded before the start
	first   int64 // the first packet's position
	preRoll time.Duration

	pcm []float32
	// out is what pcm holds still to hand out; next is the position of
	// out's first frame, valid once synced (after the first packet
	// following a seek, whose timestamp sets it).
	out      []float32
	next     int64
	isSynced bool
	// target drops the frames before it, after a seek or at the start.
	target int64

	// decoded is the position after the last frame Read gave; atEnd, that
	// Read gave the last.
	decoded atomic.Int64
	atEnd   atomic.Bool
	errOnce sync.Once
	onErr   func(error)
}

// newOpusStream reads src's header; onErr hears of the first error that
// ends the stream early, from MyGo's decoding goroutine.
func newOpusStream(src io.ReadSeeker, onErr func(error)) (*opusStream, error) {
	r, err := webm.Open(src)
	if err != nil {
		return nil, err
	}
	tr := r.Track()
	if tr.CodecID != "A_OPUS" {
		return nil, fmt.Errorf("audio: codec %q, want Opus", tr.CodecID)
	}
	if tr.Channels > 2 {
		return nil, errChannels
	}
	dec, err := opus.NewDecoderWithOutput(opusRate, 2)
	if err != nil {
		return nil, err
	}
	s := &opusStream{
		r:       r,
		dec:     dec,
		length:  -1,
		preSkip: frameAt(tr.CodecDelay),
		preRoll: tr.SeekPreRoll,
		pcm:     make([]float32, maxOpusFrame*2),
		onErr:   onErr,
	}
	if c, ok := src.(io.Closer); ok {
		s.src = c
	}
	// The OpusHead's pre-skip, when the container gives no delay.
	if head := tr.CodecPrivate; s.preSkip == 0 && len(head) >= 12 && string(head[:8]) == "OpusHead" {
		s.preSkip = int64(binary.LittleEndian.Uint16(head[10:12]))
	}
	if s.preRoll <= 0 {
		s.preRoll = defaultPreRoll
	}
	if d := r.Duration(); d > 0 {
		s.length = frameAt(d)
	}
	s.first = frameAt(r.Start()) - s.preSkip
	return s, nil
}

// frameAt is the frame at d.
func frameAt(d time.Duration) int64 {
	return int64(math.Round(d.Seconds() * opusRate))
}

func (s *opusStream) SampleRate() int { return opusRate }
func (s *opusStream) Channels() int   { return 2 }
func (s *opusStream) Len() int64      { return s.length }

// Read hands out decoded frames, dropping the codec's delay at the start
// and those before a seek's target.
func (s *opusStream) Read(buf []float32) (int, error) {
	n := 0
	for n+2 <= len(buf) {
		if len(s.out) == 0 {
			if err := s.decodeNext(); err != nil {
				s.decoded.Store(s.next)
				if errors.Is(err, io.EOF) {
					s.atEnd.Store(true)
				} else {
					s.reportErr(err)
				}
				return n, err
			}
			continue
		}
		c := copy(buf[n:len(buf)&^1], s.out)
		n += c
		s.out = s.out[c:]
		s.next += int64(c / 2)
	}
	s.decoded.Store(s.next)
	return n, nil
}

// decodeNext decodes the next packet into out, trimmed.
func (s *opusStream) decodeNext() error {
	f, err := s.r.ReadFrame()
	if err != nil {
		return err
	}
	n, err := s.dec.DecodeToFloat32(f.Data, s.pcm)
	if err != nil {
		return fmt.Errorf("audio: decoding Opus: %w", err)
	}
	if !s.isSynced {
		s.next, s.isSynced = s.place(f.Time, int64(n)), true
	}
	frames := s.pcm[:2*n]
	if pad := frameAt(f.DiscardPadding); pad > 0 {
		frames = frames[:2*max(int64(n)-pad, 0)]
	}
	if drop := s.target - s.next; drop > 0 {
		drop = min(drop, int64(len(frames)/2))
		frames = frames[2*drop:]
		s.next += drop
	}
	s.out = frames
	return nil
}

// place returns the position of a packet of n frames at t. Timestamps are
// in whole milliseconds, and the codec's delay rarely is: in a stream of
// packets of one length, as encoders make, the packet's place on their
// grid from the start is exact, and within a millisecond of t.
func (s *opusStream) place(t time.Duration, n int64) int64 {
	at := frameAt(t) - s.preSkip
	if n <= 0 {
		return at
	}
	grid := s.first + (at-s.first+n/2)/n*n
	if diff := grid - at; diff >= -opusRate/1000 && diff <= opusRate/1000 {
		return grid
	}
	return at
}

// SetPosition moves to frame: the decoder starts again at the cluster
// before frame less the pre-roll, and Read drops what comes before frame.
func (s *opusStream) SetPosition(frame int64) error {
	frame = max(frame, 0)
	at := time.Duration(float64(frame+s.preSkip)/opusRate*float64(time.Second)) - s.preRoll
	if _, err := s.r.Seek(at); err != nil {
		// MyGo ends the stream where it cannot seek: as an error, not an
		// end.
		s.reportErr(err)
		return err
	}
	if err := s.dec.Init(opusRate, 2); err != nil {
		return err
	}
	s.out, s.isSynced, s.target, s.next = nil, false, frame, frame
	s.decoded.Store(frame)
	s.atEnd.Store(false)
	return nil
}

func (s *opusStream) reportErr(err error) {
	if s.onErr != nil {
		s.errOnce.Do(func() { s.onErr(err) })
	}
}

// Close closes the source, so that a read waiting on it returns.
func (s *opusStream) Close() error {
	if s.src == nil {
		return nil
	}
	return s.src.Close()
}
