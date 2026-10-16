// Package webm reads the frames of an audio track out of a WebM (Matroska)
// file, as YouTube serves its Opus audio: the header up to the first
// cluster is read once, then frames one at a time, and Seek jumps through
// the cues. Only what playing audio needs is read; video, tags and
// chapters are skipped.
package webm

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"time"
)

// Element IDs, with their length marker, as the specification writes them.
const (
	idEBML        = 0x1A45DFA3
	idSegment     = 0x18538067
	idInfo        = 0x1549A966
	idTimeScale   = 0x2AD7B1
	idDuration    = 0x4489
	idTracks      = 0x1654AE6B
	idTrackEntry  = 0xAE
	idTrackNumber = 0xD7
	idTrackType   = 0x83
	idCodecID     = 0x86
	idCodecPriv   = 0x63A2
	idCodecDelay  = 0x56AA
	idSeekPreRoll = 0x56BB
	idAudio       = 0xE1
	idSampleRate  = 0xB5
	idChannels    = 0x9F
	idSeekHead    = 0x114D9B74
	idSeek        = 0x4DBB
	idSeekID      = 0x53AB
	idSeekPos     = 0x53AC
	idCues        = 0x1C53BB6B
	idCuePoint    = 0xBB
	idCueTime     = 0xB3
	idCuePos      = 0xB7
	idCueTrack    = 0xF7
	idCueCluster  = 0xF1
	idCluster     = 0x1F43B675
	idTimecode    = 0xE7
	idSimpleBlock = 0xA3
	idBlockGroup  = 0xA0
	idBlock       = 0xA1
	idDiscardPad  = 0x75A2

	trackAudio = 2
	// unknownSize is an element whose size its header leaves open, as a
	// live stream's segment and clusters.
	unknownSize = -1
	// maxHeaderElement bounds the elements read whole into memory: info,
	// tracks and cues, and a block. Real ones are a few KB.
	maxHeaderElement = 16 << 20
	// maxLaced bounds the frames of one laced block.
	maxLaced = 256
)

var (
	// ErrNoAudio is a file without an audio track.
	ErrNoAudio = errors.New("webm: no audio track")
	// ErrFormat is data that is not WebM, or is broken.
	ErrFormat = errors.New("webm: malformed")
)

// Track describes the audio track a Reader reads.
type Track struct {
	Number  uint64
	CodecID string // e.g. "A_OPUS"
	// CodecPrivate is the codec's header, an OpusHead for Opus.
	CodecPrivate []byte
	// CodecDelay is how much decoded audio precedes the start (Opus's
	// pre-skip); SeekPreRoll how much to decode before a seek's target
	// for the decoder to settle.
	CodecDelay, SeekPreRoll time.Duration
	SampleRate              float64
	Channels                int
}

// Frame is one coded frame of the track.
type Frame struct {
	// Data is valid until the next ReadFrame or Seek.
	Data []byte
	// Time is the block's timestamp: the frames of a laced block share it.
	Time time.Duration
	// DiscardPadding is how much decoded audio to drop from the frame's
	// end, as the last frame's padding.
	DiscardPadding time.Duration
}

type cue struct {
	time    time.Duration
	cluster int64 // offset from the segment's data
}

// Reader reads one audio track's frames. It is not safe for concurrent use.
type Reader struct {
	src io.ReadSeeker
	br  *bufio.Reader
	off int64 // of the next byte br gives

	track    Track
	scale    time.Duration // per timecode unit
	duration time.Duration
	segment  int64 // offset of the segment's data
	// segmentEnd is the segment's end, or -1 when its size is unknown.
	segmentEnd int64
	cues       []cue
	// cuesAt is where the seek head says the cues are, when they follow
	// the clusters (ffmpeg's default), read at the first Seek; -1 once read.
	cuesAt int64
	// firstCluster is where the first cluster starts; start is its first
	// frame's time.
	firstCluster int64
	start        time.Duration
	// pending is the first frame, which Open reads for its time.
	pending    Frame
	hasPending bool

	// clusterTime is the timecode of the cluster being read.
	clusterTime time.Duration

	block []byte
	// laced holds a laced block's frames, from lacedNext on still to hand
	// out.
	laced     [][]byte
	lacedNext int
	lacedAt   time.Duration
	lacedPad  time.Duration
	sizes     [maxLaced]int
}

// Open reads r's header up to the first cluster and returns a Reader of
// its first audio track, positioned at its first frame.
func Open(r io.ReadSeeker) (*Reader, error) {
	start, err := r.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil, err
	}
	w := &Reader{src: r, br: bufio.NewReaderSize(r, 16<<10), off: start, scale: time.Millisecond, cuesAt: -1}
	if err := w.readHeader(); err != nil {
		return nil, err
	}
	if w.track.Number == 0 {
		return nil, ErrNoAudio
	}
	f, err := w.ReadFrame()
	if err != nil {
		return nil, truncated("no frame", err)
	}
	w.pending, w.hasPending, w.start = f, true, f.Time
	return w, nil
}

// Track returns the audio track read.
func (w *Reader) Track() Track { return w.track }

// Duration returns the segment's duration, or 0 when it gives none.
func (w *Reader) Duration() time.Duration { return w.duration }

// Start returns the first frame's time, which may be negative: YouTube
// moves its timestamps back by the codec delay.
func (w *Reader) Start() time.Duration { return w.start }

func (w *Reader) readHeader() error {
	id, size, err := w.readElementHeader()
	if err != nil {
		return truncated("no EBML header", err)
	}
	if id != idEBML {
		return fmt.Errorf("%w: no EBML header", ErrFormat)
	}
	if err := w.skip(size); err != nil {
		return err
	}
	for {
		id, size, err = w.readElementHeader()
		if err != nil {
			return truncated("no segment", err)
		}
		if id == idSegment {
			break
		}
		if err := w.skip(size); err != nil {
			return err
		}
	}
	w.segment, w.segmentEnd = w.off, unknownSize
	if size != unknownSize {
		w.segmentEnd = w.off + size
	}
	hasCues := false
	for {
		at := w.off
		id, size, err := w.readElementHeader()
		if err != nil {
			return truncated("no cluster", err)
		}
		switch id {
		case idSeekHead, idInfo, idTracks, idCues:
			body, err := w.readBody(size)
			if err != nil {
				return err
			}
			switch id {
			case idSeekHead:
				err = w.parseSeekHead(body)
			case idInfo:
				err = w.parseInfo(body)
			case idTracks:
				err = w.parseTracks(body)
			default:
				hasCues = true
				err = w.parseCues(body)
			}
			if err != nil {
				return err
			}
		case idCluster:
			w.firstCluster = at
			if hasCues {
				w.cuesAt = -1
			}
			w.clusterTime = 0
			return nil
		default:
			if err := w.skip(size); err != nil {
				return err
			}
		}
	}
}

func (w *Reader) parseSeekHead(b []byte) error {
	return children(b, func(id uint64, v []byte) error {
		if id != idSeek {
			return nil
		}
		var target uint64
		var pos int64 = -1
		err := children(v, func(id uint64, v []byte) error {
			switch id {
			case idSeekID:
				target = readUint(v)
			case idSeekPos:
				pos = readOffset(v)
			}
			return nil
		})
		if target == idCues && pos >= 0 {
			w.cuesAt = w.segment + pos
		}
		return err
	})
}

func (w *Reader) parseInfo(b []byte) error {
	scale := uint64(time.Millisecond)
	var duration float64
	err := children(b, func(id uint64, v []byte) error {
		switch id {
		case idTimeScale:
			scale = readUint(v)
		case idDuration:
			duration = readFloat(v)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if scale == 0 || scale > uint64(time.Second) {
		return fmt.Errorf("%w: timecode scale %d", ErrFormat, scale)
	}
	w.scale = time.Duration(scale)
	if duration > 0 && !math.IsInf(duration, 0) {
		w.duration = time.Duration(duration * float64(scale))
	}
	return nil
}

func (w *Reader) parseTracks(b []byte) error {
	return children(b, func(id uint64, v []byte) error {
		if id != idTrackEntry || w.track.Number != 0 {
			return nil
		}
		t := Track{SampleRate: 8000, Channels: 1}
		var kind uint64
		err := children(v, func(id uint64, v []byte) error {
			switch id {
			case idTrackNumber:
				t.Number = readUint(v)
			case idTrackType:
				kind = readUint(v)
			case idCodecID:
				t.CodecID = string(v)
			case idCodecPriv:
				t.CodecPrivate = append([]byte(nil), v...)
			case idCodecDelay:
				t.CodecDelay = time.Duration(max(readOffset(v), 0))
			case idSeekPreRoll:
				t.SeekPreRoll = time.Duration(max(readOffset(v), 0))
			case idAudio:
				return children(v, func(id uint64, v []byte) error {
					switch id {
					case idSampleRate:
						t.SampleRate = readFloat(v)
					case idChannels:
						if c := readUint(v); c <= 255 {
							t.Channels = int(c)
						}
					}
					return nil
				})
			}
			return nil
		})
		if err == nil && kind == trackAudio && t.Number != 0 {
			w.track = t
		}
		return err
	})
}

func (w *Reader) parseCues(b []byte) error {
	w.cues = w.cues[:0]
	return children(b, func(id uint64, v []byte) error {
		if id != idCuePoint {
			return nil
		}
		var c cue
		var hasTime, hasPos bool
		err := children(v, func(id uint64, v []byte) error {
			switch id {
			case idCueTime:
				t := readOffset(v)
				c.time, hasTime = time.Duration(t), t >= 0
			case idCuePos:
				var track uint64
				var pos int64 = -1
				err := children(v, func(id uint64, v []byte) error {
					switch id {
					case idCueTrack:
						track = readUint(v)
					case idCueCluster:
						pos = readOffset(v)
					}
					return nil
				})
				isTrack := track != 0 && (w.track.Number == 0 || track == w.track.Number)
				if pos >= 0 && !hasPos && isTrack {
					c.cluster, hasPos = pos, true
				}
				return err
			}
			return nil
		})
		if hasTime && hasPos {
			w.cues = append(w.cues, c)
		}
		return err
	})
}

// ReadFrame returns the track's next frame, io.EOF after the last.
func (w *Reader) ReadFrame() (Frame, error) {
	if w.hasPending {
		w.hasPending = false
		return w.pending, nil
	}
	if w.lacedNext < len(w.laced) {
		f := Frame{Data: w.laced[w.lacedNext], Time: w.lacedAt}
		w.lacedNext++
		if w.lacedNext == len(w.laced) {
			f.DiscardPadding = w.lacedPad
		}
		return f, nil
	}
	for {
		if w.segmentEnd >= 0 && w.off >= w.segmentEnd {
			return Frame{}, io.EOF
		}
		id, size, err := w.readElementHeader()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return Frame{}, io.EOF
			}
			return Frame{}, err
		}
		switch id {
		case idCluster:
			// Its children follow, as elements of their own.
			w.clusterTime = 0
		case idTimecode:
			body, err := w.readBody(size)
			if err != nil {
				return Frame{}, err
			}
			w.clusterTime = time.Duration(max(readOffset(body), 0)) * w.scale
		case idSimpleBlock, idBlockGroup:
			f, ok, err := w.readBlock(id, size)
			if err != nil || ok {
				return f, err
			}
		default:
			if err := w.skip(size); err != nil {
				return Frame{}, err
			}
		}
	}
}

// readBlock reads a SimpleBlock or a BlockGroup and reports whether it
// held a frame of the track.
func (w *Reader) readBlock(id uint64, size int64) (Frame, bool, error) {
	body, err := w.readBody(size)
	if err != nil {
		return Frame{}, false, err
	}
	var pad time.Duration
	if id == idBlockGroup {
		var block []byte
		err := children(body, func(id uint64, v []byte) error {
			switch id {
			case idBlock:
				block = v
			case idDiscardPad:
				pad = time.Duration(readInt(v))
			}
			return nil
		})
		if err != nil {
			return Frame{}, false, err
		}
		if block == nil {
			return Frame{}, false, nil
		}
		body = block
	}
	track, n := readVint(body, false)
	if n == 0 || len(body) < n+3 {
		return Frame{}, false, fmt.Errorf("%w: short block", ErrFormat)
	}
	if track != w.track.Number {
		return Frame{}, false, nil
	}
	// A signed 16-bit offset from the cluster's timecode.
	rel := int(binary.BigEndian.Uint16(body[n:]))
	if rel >= 1<<15 {
		rel -= 1 << 16
	}
	flags := body[n+2]
	data := body[n+3:]
	at := w.clusterTime + time.Duration(rel)*w.scale
	if flags&0x06 == 0 {
		return Frame{Data: data, Time: at, DiscardPadding: max(pad, 0)}, true, nil
	}
	frames, err := w.unlace(flags&0x06, data)
	if err != nil {
		return Frame{}, false, err
	}
	w.laced, w.lacedNext, w.lacedAt, w.lacedPad = frames, 1, at, max(pad, 0)
	f := Frame{Data: frames[0], Time: at}
	if len(frames) == 1 {
		f.DiscardPadding = w.lacedPad
	}
	return f, true, nil
}

// Lacing kinds, from a block's flags.
const (
	laceXiph  = 0x02
	laceFixed = 0x04
	laceEBML  = 0x06
)

// unlace splits a laced block's data into its frames.
func (w *Reader) unlace(kind byte, data []byte) ([][]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: empty laced block", ErrFormat)
	}
	count := int(data[0]) + 1
	data = data[1:]
	sizes := w.sizes[:count]
	total := 0
	switch kind {
	case laceFixed:
		if len(data)%count != 0 {
			return nil, fmt.Errorf("%w: uneven fixed lacing", ErrFormat)
		}
		for i := range sizes {
			sizes[i] = len(data) / count
		}
		total = len(data)
	case laceXiph:
		for i := range count - 1 {
			size := 0
			for {
				if len(data) == 0 {
					return nil, fmt.Errorf("%w: short Xiph lacing", ErrFormat)
				}
				b := data[0]
				data = data[1:]
				size += int(b)
				if b != 0xFF {
					break
				}
			}
			sizes[i] = size
			total += size
		}
	case laceEBML:
		first, n := readVint(data, false)
		if n == 0 {
			return nil, fmt.Errorf("%w: short EBML lacing", ErrFormat)
		}
		data = data[n:]
		if first > math.MaxInt32 {
			return nil, fmt.Errorf("%w: laced size %d", ErrFormat, first)
		}
		size := int64(first)
		sizes[0], total = int(size), int(size)
		for i := 1; i < count-1; i++ {
			raw, n := readVint(data, false)
			if n == 0 {
				return nil, fmt.Errorf("%w: short EBML lacing", ErrFormat)
			}
			data = data[n:]
			if raw > math.MaxInt32 {
				return nil, fmt.Errorf("%w: laced size difference %d", ErrFormat, raw)
			}
			// A signed difference from the previous size.
			size += int64(raw) - (int64(1)<<(7*n-1) - 1)
			if size < 0 || size > math.MaxInt32 {
				return nil, fmt.Errorf("%w: negative laced size", ErrFormat)
			}
			sizes[i] = int(size)
			total += int(size)
		}
	}
	if kind != laceFixed {
		if total > len(data) {
			return nil, fmt.Errorf("%w: laced sizes overflow the block", ErrFormat)
		}
		sizes[count-1] = len(data) - total
	}
	frames := w.laced[:0]
	if cap(frames) < count {
		frames = make([][]byte, 0, count)
	}
	for _, size := range sizes {
		frames = append(frames, data[:size])
		data = data[size:]
	}
	return frames, nil
}

// Seek moves to the last cue at or before t, from where the next
// ReadFrame reads, and returns the time of that cue: frames of the
// cluster before t come first. Before the first cue, or without cues, it
// moves to the first frame.
func (w *Reader) Seek(t time.Duration) (time.Duration, error) {
	if w.cuesAt >= 0 {
		// Read once: a file without cues where the seek head says goes
		// on without them.
		at := w.cuesAt
		w.cuesAt = -1
		if err := w.readCuesAt(at); err != nil {
			return 0, err
		}
	}
	at, cueTime := w.firstCluster, w.start
	for _, c := range w.cues {
		ct := c.time * w.scale
		if ct > t {
			break
		}
		if ct > cueTime {
			at, cueTime = w.segment+c.cluster, ct
		}
	}
	if err := w.seekTo(at); err != nil {
		return 0, err
	}
	w.hasPending = false
	w.laced, w.lacedNext = w.laced[:0], 0
	return cueTime, nil
}

func (w *Reader) readCuesAt(off int64) error {
	if err := w.seekTo(off); err != nil {
		return err
	}
	id, size, err := w.readElementHeader()
	if err != nil || id != idCues {
		return nil //nolint:nilerr // no cues there: seek without them
	}
	body, err := w.readBody(size)
	if err != nil {
		return err
	}
	return w.parseCues(body)
}

func (w *Reader) seekTo(off int64) error {
	if _, err := w.src.Seek(off, io.SeekStart); err != nil {
		return err
	}
	w.br.Reset(w.src)
	w.off = off
	return nil
}

// readElementHeader reads an element's ID and size; size is unknownSize
// when the header leaves it open.
func (w *Reader) readElementHeader() (id uint64, size int64, err error) {
	id, err = w.readVint(true)
	if err != nil {
		return 0, 0, err
	}
	raw, n, err := w.readSize()
	if err != nil {
		return 0, 0, err
	}
	if raw == 1<<(7*n)-1 {
		return id, unknownSize, nil
	}
	if raw > math.MaxInt64/2 {
		return 0, 0, fmt.Errorf("%w: element size %d", ErrFormat, raw)
	}
	return id, int64(raw), nil
}

func (w *Reader) readVint(keepMarker bool) (uint64, error) {
	v, _, err := w.readVarint(keepMarker, 4)
	return v, err
}

func (w *Reader) readSize() (uint64, int, error) { return w.readVarint(false, 8) }

func (w *Reader) readVarint(keepMarker bool, maxLen int) (uint64, int, error) {
	first, err := w.br.ReadByte()
	if err != nil {
		return 0, 0, err
	}
	w.off++
	n := 1
	for mask := byte(0x80); first&mask == 0; mask >>= 1 {
		n++
		if mask == 1 || n > maxLen {
			return 0, 0, fmt.Errorf("%w: bad variable-size integer", ErrFormat)
		}
	}
	v := uint64(first)
	if !keepMarker {
		v &= 0xFF >> n
	}
	for range n - 1 {
		b, err := w.br.ReadByte()
		if err != nil {
			return 0, 0, noEOF(err)
		}
		w.off++
		v = v<<8 | uint64(b)
	}
	return v, n, nil
}

// readBody reads an element's data into the Reader's buffer, valid until
// the next readBody.
func (w *Reader) readBody(size int64) ([]byte, error) {
	if size == unknownSize || size > maxHeaderElement {
		return nil, fmt.Errorf("%w: element of %d bytes", ErrFormat, size)
	}
	if int64(cap(w.block)) < size {
		w.block = make([]byte, size)
	}
	b := w.block[:size]
	n, err := io.ReadFull(w.br, b)
	w.off += int64(n)
	if err != nil {
		return nil, noEOF(err)
	}
	return b, nil
}

func (w *Reader) skip(size int64) error {
	if size == unknownSize {
		return fmt.Errorf("%w: cannot skip an element of unknown size", ErrFormat)
	}
	if size <= int64(w.br.Buffered()) {
		n, err := w.br.Discard(int(size))
		w.off += int64(n)
		return noEOF(err)
	}
	return w.seekTo(w.off + size)
}

// truncated is a read failing before the header ended: ErrFormat for a
// file that ends there, else the reader's error, as a network's.
func truncated(what string, err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return fmt.Errorf("%w: %s", ErrFormat, what)
	}
	return err
}

// noEOF turns an EOF inside an element into ErrUnexpectedEOF.
func noEOF(err error) error {
	if errors.Is(err, io.EOF) {
		return io.ErrUnexpectedEOF
	}
	return err
}

// children calls fn with each child element of an element's data.
func children(b []byte, fn func(id uint64, v []byte) error) error {
	for len(b) > 0 {
		id, n := readVint(b, true)
		if n == 0 {
			return fmt.Errorf("%w: bad element ID", ErrFormat)
		}
		b = b[n:]
		size, m := readVint(b, false)
		if m == 0 || size > math.MaxInt32 || int(size) > len(b)-m {
			return fmt.Errorf("%w: element overflows its parent", ErrFormat)
		}
		b = b[m:]
		if err := fn(id, b[:size]); err != nil {
			return err
		}
		b = b[size:]
	}
	return nil
}

// readVint decodes a variable-size integer at the start of b and returns
// it with its length, 0 when b holds none.
func readVint(b []byte, keepMarker bool) (uint64, int) {
	if len(b) == 0 || b[0] == 0 {
		return 0, 0
	}
	n := 1
	for mask := byte(0x80); b[0]&mask == 0; mask >>= 1 {
		n++
	}
	if n > len(b) {
		return 0, 0
	}
	v := uint64(b[0])
	if !keepMarker {
		v &= 0xFF >> n
	}
	for _, c := range b[1:n] {
		v = v<<8 | uint64(c)
	}
	return v, n
}

func readUint(b []byte) uint64 {
	var v uint64
	for _, c := range b[:min(len(b), 8)] {
		v = v<<8 | uint64(c)
	}
	return v
}

// readOffset reads an unsigned integer as an offset or a duration, -1 when
// it does not fit.
func readOffset(b []byte) int64 {
	v := readUint(b)
	if v > math.MaxInt64 {
		return -1
	}
	return int64(v)
}

func readInt(b []byte) int64 {
	if len(b) == 0 {
		return 0
	}
	v := int64(b[0])
	if v >= 0x80 {
		v -= 0x100 // negative
	}
	for _, c := range b[1:min(len(b), 8)] {
		v = v<<8 | int64(c)
	}
	return v
}

func readFloat(b []byte) float64 {
	switch len(b) {
	case 4:
		return float64(math.Float32frombits(binary.BigEndian.Uint32(b)))
	case 8:
		return math.Float64frombits(binary.BigEndian.Uint64(b))
	}
	return 0
}
