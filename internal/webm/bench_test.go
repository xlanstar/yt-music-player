package webm

import (
	"bytes"
	"errors"
	"io"
	"os"
	"testing"
	"time"
)

var (
	sinkFrame Frame
	sinkTime  time.Duration
	sinkErr   error
)

// synthWebM builds a WebM file as YouTube's are laid out (cues before the
// clusters, a cluster every 5 s) of frames packets of 20 ms, each of size
// bytes: demuxing reads no codec data, so its bytes need not be Opus.
func synthWebM(frames, size int) []byte {
	el := func(id uint64, body []byte) []byte {
		var b []byte
		for shift := 24; shift >= 0; shift -= 8 {
			if c := byte(id >> shift); c != 0 || len(b) > 0 {
				b = append(b, c)
			}
		}
		n := uint64(len(body))
		b = append(b, 0x08, byte(n>>48), byte(n>>40), byte(n>>32), byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
		b[len(b)-8] = 0x01 // an 8-byte size
		return append(b, body...)
	}
	u := func(v uint64) []byte {
		return []byte{byte(v >> 56), byte(v >> 48), byte(v >> 40), byte(v >> 32), byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}
	}
	cat := func(parts ...[]byte) []byte { return bytes.Join(parts, nil) }
	const perCluster = 250 // 5 s
	var clusters [][]byte
	for start := 0; start < frames; start += perCluster {
		parts := [][]byte{el(idTimecode, u(uint64(start*20)))}
		for i := start; i < min(start+perCluster, frames); i++ {
			rel := (i - start) * 20
			block := append([]byte{0x81, byte(rel >> 8), byte(rel), 0x80}, bytes.Repeat([]byte{byte(i)}, size)...)
			parts = append(parts, el(idSimpleBlock, block))
		}
		clusters = append(clusters, el(idCluster, cat(parts...)))
	}
	head := []byte("OpusHead\x01\x02\x38\x01\x80\xbb\x00\x00\x00\x00\x00")
	tracks := el(idTracks, el(idTrackEntry, cat(
		el(idTrackNumber, u(1)), el(idTrackType, u(trackAudio)), el(idCodecID, []byte("A_OPUS")),
		el(idCodecPriv, head), el(idCodecDelay, u(6500000)), el(idSeekPreRoll, u(80000000)),
		el(idAudio, cat(el(idSampleRate, []byte{0x40, 0xE7, 0x70, 0, 0, 0, 0, 0}), el(idChannels, u(2)))),
	)))
	info := el(idInfo, el(idTimeScale, u(1000000)))
	// Cue positions depend on the cues' own size: el writes a cue point in
	// 69 bytes, behind the cues' 12.
	cueSize := 9 + (9 + 8) + (9 + (9+8)*2)
	cuesLen := 4 + 8 + cueSize*len(clusters)
	pos := len(info) + len(tracks) + cuesLen
	var cues [][]byte
	for i, c := range clusters {
		cues = append(cues, el(idCuePoint, cat(
			el(idCueTime, u(uint64(i*perCluster*20))),
			el(idCuePos, cat(el(idCueTrack, u(1)), el(idCueCluster, u(uint64(pos))))),
		)))
		pos += len(c)
	}
	segment := cat(append([][]byte{info, tracks, el(idCues, cat(cues...))}, clusters...)...)
	return cat(el(idEBML, el(0x4282, []byte("webm"))), el(idSegment, segment))
}

type benchFile struct {
	name string
	data []byte
	// openLimit is TestBudgetOpen's: 12 allocs today on the tone, 17 on
	// ten minutes (more cues).
	openLimit float64
}

// benchFiles are the fixture and a ten-minute song.
func benchFiles(tb testing.TB) []benchFile {
	return []benchFile{
		{"tone", readFile(tb, fixtures[1]), 14},
		{"10min", synthWebM(30000, 320), 20},
	}
}

func readFile(tb testing.TB, path string) []byte {
	tb.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		tb.Fatal(err)
	}
	return data
}

func BenchmarkOpen(b *testing.B) {
	for _, f := range benchFiles(b) {
		b.Run(f.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_, sinkErr = Open(bytes.NewReader(f.data))
			}
		})
	}
}

func BenchmarkReadFrame(b *testing.B) {
	for _, f := range benchFiles(b) {
		b.Run(f.name, func(b *testing.B) {
			r, err := Open(bytes.NewReader(f.data))
			if err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(len(f.data)))
			b.ReportAllocs()
			for b.Loop() {
				readThrough(b, r)
			}
		})
	}
}

// readThrough reads every frame from the start.
func readThrough(tb testing.TB, r *Reader) {
	if _, err := r.Seek(0); err != nil {
		tb.Fatal(err)
	}
	for {
		f, err := r.ReadFrame()
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			tb.Fatal(err)
		}
		sinkFrame = f
	}
}

func BenchmarkSeek(b *testing.B) {
	for _, f := range benchFiles(b) {
		b.Run(f.name, func(b *testing.B) {
			r, err := Open(bytes.NewReader(f.data))
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				sinkTime, sinkErr = r.Seek(4 * time.Second)
			}
		})
	}
}
