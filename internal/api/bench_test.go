package api

import (
	"bytes"
	"encoding/gob"
	"strconv"
	"strings"
	"testing"
	"yt-music-player/internal/catalog"
)

// songs5000 are a scaled queue or playlist, deterministic.
func songs5000() ([]string, []catalog.Song) {
	ids := make([]string, 5000)
	songs := make([]catalog.Song, len(ids))
	for i := range songs {
		songs[i] = testSong(i)
		ids[i] = songs[i].ID
	}
	return ids, songs
}

// encodeCases are frames a daemon sends often (state) or large: a queue's
// snapshot and a playlist of 5000 songs. limit holds allocations per steady
// Encode, the stream's types already sent. 0.1 rather than 0: gob keeps
// its buffers in a sync.Pool, which a GC under load empties, so one call
// in twenty may refill it.
var encodeCases = []struct {
	name  string
	frame func() *Frame
	limit float64
}{
	{"state", func() *Frame { return &Frame{Event: &Event{State: testState()}} }, 0.1}, // 0 allocs today.
	{"queue-5000", func() *Frame {
		ids, songs := songs5000()
		return &Frame{Event: &Event{Queue: &Queue{IDs: ids}, Songs: songs}}
	}, 0.1}, // 0 allocs today.
	{"playlist-5000", func() *Frame {
		ids, songs := songs5000()
		list := testPlaylist()
		list.Songs = ids
		return &Frame{ID: 9, Reply: &Reply{Playlist: &PlaylistReply{Playlist: list, Songs: songs}}}
	}, 0.1}, // 0 allocs today.
}

// steadyEncoder is an encoder that has sent f's types, writing into a
// buffer it empties before each frame.
func steadyEncoder(tb testing.TB, f *Frame) (encode func(), size int) {
	tb.Helper()
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(f); err != nil {
		tb.Fatal(err)
	}
	encode = func() {
		buf.Reset()
		if err := enc.Encode(f); err != nil {
			tb.Fatal(err)
		}
	}
	encode()
	return encode, buf.Len()
}

func BenchmarkEncode(b *testing.B) {
	for _, tc := range encodeCases {
		b.Run(tc.name, func(b *testing.B) {
			encode, size := steadyEncoder(b, tc.frame())
			b.SetBytes(int64(size))
			b.ReportAllocs()
			for b.Loop() {
				encode()
			}
		})
	}
}

// sinkInt keeps the compiler from optimizing measured calls away.
var sinkInt int

func BenchmarkValidate(b *testing.B) {
	call := &Call{Method: MethodEnqueue, IDs: &IDsArgs{IDs: []string{"a"}}}
	b.ReportAllocs()
	for b.Loop() {
		if call.Validate() == nil {
			sinkInt++
		}
	}
}

// sinkString keeps the compiler from optimizing measured calls away.
var sinkString string

// cookieHeader is a pasted Cookie header the size of YouTube Music's,
// over several lines.
var cookieHeader = func() string {
	var sb strings.Builder
	sb.WriteString("Cookie: ")
	for i := range 40 {
		sb.WriteString("NAME" + strconv.Itoa(i) + "=" + strings.Repeat("v", 40) + ";")
		if i%8 == 7 {
			sb.WriteString("\n ")
		} else {
			sb.WriteString(" ")
		}
	}
	return sb.String()
}()

func BenchmarkCleanCredentials(b *testing.B) {
	b.SetBytes(int64(len(cookieHeader)))
	b.ReportAllocs()
	for b.Loop() {
		sinkString = CleanCredentials(cookieHeader)
	}
}
