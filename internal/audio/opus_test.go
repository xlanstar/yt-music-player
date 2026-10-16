package audio

import (
	"bytes"
	"errors"
	"io"
	"math"
	"os"
	"testing"
)

// toneFrames is the fixture's length: 6 s, as ffmpeg decodes it.
const toneFrames = 6 * opusRate

var toneFixtures = []string{"../webm/testdata/tone.webm", "../webm/testdata/tone_cues_first.webm"}

func readFixture(t testing.TB, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func openTone(t testing.TB, path string) *opusStream {
	t.Helper()
	s, err := newOpusStream(bytes.NewReader(readFixture(t, path)), nil)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// decodeAll reads s to its end, in reads of MyGo's size.
func decodeAll(t testing.TB, s *opusStream) []float32 {
	t.Helper()
	var all []float32
	buf := make([]float32, 2048*2)
	for {
		n, err := s.Read(buf)
		all = append(all, buf[:n]...)
		if errors.Is(err, io.EOF) {
			return all
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestOpusDecodesTheTone(t *testing.T) {
	for _, path := range toneFixtures {
		t.Run(path, func(t *testing.T) {
			s := openTone(t, path)
			if s.Len() < toneFrames || s.Len() > toneFrames+opusRate/50 {
				t.Errorf("Len %d, want about %d", s.Len(), toneFrames)
			}
			all := decodeAll(t, s)
			// The codec's delay dropped at the start, the padding at the
			// end: the frames ffmpeg decodes.
			if len(all) != 2*toneFrames {
				t.Fatalf("%d frames, want %d", len(all)/2, toneFrames)
			}
			if got := s.decoded.Load(); got != toneFrames {
				t.Errorf("decoded %d", got)
			}
			for ch, hz := range []float64{440, 660} {
				crossings, sum := 0, 0.0
				for i := 2 + ch; i < len(all); i += 2 {
					if (all[i-2] < 0) != (all[i] < 0) {
						crossings++
					}
					sum += float64(all[i]) * float64(all[i])
				}
				// ffmpeg's sine is 1/8 full scale.
				rms := math.Sqrt(sum / toneFrames)
				if got := float64(crossings) / 2 / 6; math.Abs(got-hz) > 2 || math.Abs(rms-0.125/math.Sqrt2) > 0.005 {
					t.Errorf("channel %d: %.1f Hz at %.4f RMS, want %v Hz at %.4f", ch, got, rms, hz, 0.125/math.Sqrt2)
				}
			}
		})
	}
}

func TestOpusSetPosition(t *testing.T) {
	for _, path := range toneFixtures {
		t.Run(path, func(t *testing.T) {
			all := decodeAll(t, openTone(t, path))
			s := openTone(t, path)
			buf := make([]float32, 4800*2)
			for _, frame := range []int64{3 * opusRate, opusRate / 2, 0, 5*opusRate + 123, toneFrames - 1000, 2 * opusRate} {
				if err := s.SetPosition(frame); err != nil {
					t.Fatal(err)
				}
				got := fill(t, s, buf)
				want := all[2*frame:][:len(got)]
				// After the pre-roll the decoder converges to what decoding
				// from the start gives; the tone is 1/8 full scale.
				worst := 0.0
				for i := range got {
					worst = max(worst, math.Abs(float64(got[i]-want[i])))
				}
				if worst > 0.003 || len(got) < min(len(buf), int(2*(toneFrames-frame))) {
					t.Errorf("at %d: %d samples, off by up to %.4f", frame, len(got), worst)
				}
			}
		})
	}
}

// fill reads s into buf until it is full or s ends.
func fill(t *testing.T, s *opusStream, buf []float32) []float32 {
	t.Helper()
	n := 0
	for n < len(buf) {
		c, err := s.Read(buf[n:])
		n += c
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	return buf[:n]
}

func TestOpusRejects(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"not WebM", []byte("ID3\x04\x00\x00\x00\x00\x00\x00 an MP3, say")},
		{"cut short", readFixture(t, toneFixtures[0])[:300]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := newOpusStream(bytes.NewReader(tc.data), nil); err == nil {
				t.Error("opened")
			}
		})
	}
}

func TestOpusReportsErrors(t *testing.T) {
	data := readFixture(t, toneFixtures[0])
	var got []error
	s, err := newOpusStream(bytes.NewReader(data[:len(data)/2]), func(err error) { got = append(got, err) })
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]float32, 4096)
	for {
		if _, err := s.Read(buf); err != nil {
			if errors.Is(err, io.EOF) {
				t.Fatal("a file cut in half ended without an error")
			}
			break
		}
	}
	_, _ = s.Read(buf)
	if len(got) != 1 {
		t.Errorf("reported %v, want one error", got)
	}
}
