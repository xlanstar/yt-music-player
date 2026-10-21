package webm

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
	"testing"
	"time"
)

// Both fixtures hold the same 301 frames of 20 ms (see testdata/README.md).
const toneFrames = 301

var fixtures = []string{"testdata/tone.webm", "testdata/tone_cues_first.webm"}

func open(t testing.TB, path string) *Reader {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Open(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func readAll(t *testing.T, r *Reader) []Frame {
	t.Helper()
	var frames []Frame
	for {
		f, err := r.ReadFrame()
		if errors.Is(err, io.EOF) {
			return frames
		}
		if err != nil {
			t.Fatal(err)
		}
		f.Data = bytes.Clone(f.Data)
		frames = append(frames, f)
	}
}

func TestOpen(t *testing.T) {
	for _, path := range fixtures {
		t.Run(path, func(t *testing.T) {
			r := open(t, path)
			tr := r.Track()
			if tr.CodecID != "A_OPUS" || tr.Channels != 2 || tr.SampleRate != 48000 {
				t.Errorf("track %+v", tr)
			}
			if string(tr.CodecPrivate[:8]) != "OpusHead" {
				t.Errorf("codec private %q", tr.CodecPrivate)
			}
			if tr.CodecDelay != 6500*time.Microsecond || tr.SeekPreRoll != 80*time.Millisecond {
				t.Errorf("delay %v, pre-roll %v", tr.CodecDelay, tr.SeekPreRoll)
			}
			if d := r.Duration(); d < 6*time.Second || d > 6*time.Second+20*time.Millisecond {
				t.Errorf("duration %v", d)
			}
			if r.Start() != 0 {
				t.Errorf("start %v", r.Start())
			}
		})
	}
}

func TestReadFrame(t *testing.T) {
	for _, path := range fixtures {
		t.Run(path, func(t *testing.T) {
			frames := readAll(t, open(t, path))
			if len(frames) != toneFrames {
				t.Fatalf("%d frames, want %d", len(frames), toneFrames)
			}
			for i, f := range frames {
				// Timestamps are in whole milliseconds.
				if want := time.Duration(i) * 20 * time.Millisecond; (f.Time - want).Abs() > time.Millisecond {
					t.Fatalf("frame %d at %v, want %v", i, f.Time, want)
				}
				if len(f.Data) == 0 {
					t.Fatalf("frame %d empty", i)
				}
			}
			// The encoder pads the last frame to 20 ms: 6 s of audio after
			// 6.5 ms of codec delay, in 301 frames of 20 ms.
			last := frames[len(frames)-1]
			if want := 301*20*time.Millisecond - 6*time.Second - 6500*time.Microsecond; last.DiscardPadding != want {
				t.Errorf("last frame's padding %v, want %v", last.DiscardPadding, want)
			}
		})
	}
}

func TestSeek(t *testing.T) {
	for _, path := range fixtures {
		t.Run(path, func(t *testing.T) {
			r := open(t, path)
			all := readAll(t, open(t, path))
			// Clusters start at the first frame past two seconds each.
			const c1, c2, c3 = 1981 * time.Millisecond, 3981 * time.Millisecond, 5981 * time.Millisecond
			for _, tc := range []struct {
				to, want time.Duration
			}{
				{3 * time.Second, c1}, // the cluster before
				{c1, c1},
				{c2 - time.Millisecond, c1},
				{0, 0},
				{-time.Second, 0},
				{time.Hour, c3},
				{time.Second, 0},
			} {
				got, err := r.Seek(tc.to)
				if err != nil {
					t.Fatal(err)
				}
				if got != tc.want {
					t.Errorf("Seek(%v) = %v, want %v", tc.to, got, tc.want)
				}
				f, err := r.ReadFrame()
				if err != nil {
					t.Fatal(err)
				}
				i := int((f.Time + time.Millisecond) / (20 * time.Millisecond))
				if f.Time != got || !bytes.Equal(f.Data, all[i].Data) {
					t.Errorf("after Seek(%v), frame at %v, want %v's", tc.to, f.Time, got)
				}
			}
		})
	}
}

func TestMalformed(t *testing.T) {
	data, err := os.ReadFile(fixtures[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"not EBML", []byte("OggS\x00\x02\x00\x00\x00\x00\x00\x00\x00\x00")},
		{"header only", data[:40]},
		{"cut in the tracks", data[:200]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Open(bytes.NewReader(tc.data)); err == nil {
				t.Error("opened")
			}
		})
	}
	t.Run("cut in a cluster", func(t *testing.T) {
		r, err := Open(bytes.NewReader(data[:len(data)/2]))
		if err != nil {
			t.Fatal(err)
		}
		for {
			_, err := r.ReadFrame()
			if err == nil {
				continue
			}
			if !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Errorf("err %v, want io.ErrUnexpectedEOF", err)
			}
			return
		}
	})
}

func TestUnlace(t *testing.T) {
	r := &Reader{}
	for _, tc := range []struct {
		name string
		kind byte
		data []byte
		want []string
	}{
		{"Xiph", laceXiph, []byte("\x02\x01\x02abbccc"), []string{"a", "bb", "ccc"}},
		{"Xiph 255", laceXiph, append([]byte{1, 0xFF, 0}, bytes.Repeat([]byte("x"), 256)...), []string{string(bytes.Repeat([]byte("x"), 255)), "x"}},
		{"fixed", laceFixed, []byte("\x02aabbcc"), []string{"aa", "bb", "cc"}},
		// Sizes 2, then 2+1=3 (a one-byte signed difference, biased by 63).
		{"EBML", laceEBML, []byte("\x02\x82\xC0aabbbcccc"), []string{"aa", "bbb", "cccc"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frames, err := r.unlace(tc.kind, tc.data)
			if err != nil {
				t.Fatal(err)
			}
			if len(frames) != len(tc.want) {
				t.Fatalf("%d frames, want %d", len(frames), len(tc.want))
			}
			for i, f := range frames {
				if string(f) != tc.want[i] {
					t.Errorf("frame %d %q, want %q", i, f, tc.want[i])
				}
			}
		})
	}
	if _, err := r.unlace(laceXiph, []byte("\x01\x09ab")); err == nil {
		t.Error("sizes past the block unlaced")
	}
}

func TestSynthesized(t *testing.T) {
	r, err := Open(bytes.NewReader(synthWebM(1000, 50)))
	if err != nil {
		t.Fatal(err)
	}
	frames := readAll(t, r)
	if len(frames) != 1000 || frames[999].Time != 999*20*time.Millisecond || frames[999].Data[0] != 999&0xFF {
		t.Fatalf("%d frames", len(frames))
	}
	got, err := r.Seek(12 * time.Second)
	if err != nil || got != 10*time.Second {
		t.Fatalf("Seek(12s) = %v, %v", got, err)
	}
	if f, err := r.ReadFrame(); err != nil || f.Time != 10*time.Second || f.Data[0] != 500&0xFF {
		t.Errorf("after the seek, %v at %v", err, f.Time)
	}
}

// info is an Info element's body holding a Duration of d timecode units.
func info(d float64) []byte {
	b := []byte{0x44, 0x89, 0x88}
	return binary.BigEndian.AppendUint64(b, math.Float64bits(d))
}

func TestParseInfoDuration(t *testing.T) {
	for _, tc := range []struct {
		units float64
		want  time.Duration
	}{
		{6020, 6020 * time.Millisecond},
		{0, 0},
		{-1, 0},
		{math.NaN(), 0},
		{math.Inf(1), 0},
		// Past Duration's range in nanoseconds: unknown, not 292 years.
		{1e16, 0},
	} {
		var w Reader
		if err := w.parseInfo(info(tc.units)); err != nil {
			t.Fatal(err)
		}
		if w.Duration() != tc.want {
			t.Errorf("Duration of %v ms read as %v, want %v", tc.units, w.Duration(), tc.want)
		}
	}
}

// FuzzUnlace: any block unlaces into its frame count of frames, one run
// of its bytes, or fails; it never panics.
func FuzzUnlace(f *testing.F) {
	f.Add(byte(laceXiph), []byte("\x02\x01\x02abbccc"))
	f.Add(byte(laceFixed), []byte("\x02aabbcc"))
	f.Add(byte(laceEBML), []byte("\x02\x82\xC0aabbbcccc"))
	for _, tc := range laceCases {
		f.Add(tc.kind, lacedBlock(tc.kind, tc.frames))
	}
	r := &Reader{}
	f.Fuzz(func(t *testing.T, kind byte, data []byte) {
		kind = kind&0x06 | laceXiph
		frames, err := r.unlace(kind, data)
		if err != nil {
			return
		}
		if len(frames) != int(data[0])+1 {
			t.Fatalf("%d frames, want %d", len(frames), int(data[0])+1)
		}
		if !bytes.Contains(data[1:], bytes.Join(frames, nil)) {
			t.Fatalf("frames are not a run of the block's bytes")
		}
		r.laced = frames
	})
}
