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

// encodeCases are frames a daemon sends often (state) or large, and a
// front-end's largest. limit holds allocations per steady Encode, types
// already sent: 0.1, not 0, as a GC under load empties gob's buffer
// sync.Pool, which a call may refill.
var encodeCases = []struct {
	name  string
	frame func() *Frame
	limit float64
}{
	{"state", func() *Frame { return &Frame{Event: &Event{State: testState()}} }, 0.1}, // 0 allocs today.
	// A snapshot of 5000 queued, its songs gone ahead in 16 events of 313.
	{"queue-5000", func() *Frame {
		ids, _ := songs5000()
		return &Frame{Event: &Event{State: testState(), Queue: &Queue{IDs: ids}}}
	}, 0.1}, // 0 allocs today.
	// A playlist of 5000 as ytmd replies: 16 parts of 313 songs, then the
	// playlist with its IDs.
	{"part-313", func() *Frame {
		ahead, _ := splitPlaylist5000()
		return &Frame{ID: 9, Ahead: ahead[0]}
	}, 0.1}, // 0 allocs today.
	{"playlist-5000", func() *Frame {
		_, r := splitPlaylist5000()
		return &Frame{ID: 9, Reply: r}
	}, 0.1}, // 0 allocs today.
	// 5000 songs enqueued, as a front-end sends them after 16 parts.
	{"enqueue-5000", func() *Frame {
		ids, songs := songs5000()
		call := &Call{Method: MethodEnqueue, IDs: &IDsArgs{IDs: ids, Songs: songs}}
		call.Split()
		return &Frame{ID: 9, Call: call}
	}, 0.1}, // 0 allocs today.
}

// splitPlaylist5000 is a reply of a playlist of 5000 songs, split as ytmd
// does.
func splitPlaylist5000() ([]*Ahead, *Reply) {
	ids, songs := songs5000()
	list := testPlaylist()
	list.Songs = ids
	r := &Reply{Playlist: &PlaylistReply{Playlist: list, Songs: songs}}
	return r.Split(), r
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

// partCases are long replies, sent in parts of PartItems, or more so that
// a reply takes about MaxParts. split and join hold each one's allocations
// (TestBudgetSplit, TestBudgetJoin).
var partCases = []struct {
	name        string
	reply       func() *Reply
	split, join float64
}{
	// 2 allocs today: the parts and their list; 1: the songs.
	{"playlist-5000", func() *Reply {
		ids, songs := songs5000()
		list := testPlaylist()
		list.Songs = ids
		return &Reply{Playlist: &PlaylistReply{Playlist: list, Songs: songs}}
	}, 2, 1},
	// 2 allocs today; 4: each list.
	{"library-2000", func() *Reply { return &Reply{Library: longLibrary(2000, 2000, 2000, 2000)} }, 2, 5},
}

var sinkAhead []*Ahead

// unsplit returns what puts back r's lists, to Split again.
func unsplit(r *Reply) func() {
	l, whole := r.lists(), *r
	songs, albums, artists, playlists := value(l.songs), value(l.albums), value(l.artists), value(l.playlists)
	return func() {
		*r = whole
		set(l.songs, songs)
		set(l.albums, albums)
		set(l.artists, artists)
		set(l.playlists, playlists)
	}
}

func value[T any](list *[]T) []T {
	if list == nil {
		return nil
	}
	return *list
}

func set[T any](list *[]T, v []T) {
	if list != nil {
		*list = v
	}
}

// unjoin empties r's lists, as Split leaves them, to Join again.
func unjoin(r *Reply) {
	l := r.lists()
	empty(l.songs)
	empty(l.albums)
	empty(l.artists)
	empty(l.playlists)
}

func empty[T any](list *[]T) { set(list, nil) }

func BenchmarkSplit(b *testing.B) {
	for _, tc := range partCases {
		b.Run(tc.name, func(b *testing.B) {
			r := tc.reply()
			undo := unsplit(r)
			b.ReportAllocs()
			for b.Loop() {
				undo()
				sinkAhead = r.Split()
			}
		})
	}
}

func BenchmarkJoin(b *testing.B) {
	for _, tc := range partCases {
		b.Run(tc.name, func(b *testing.B) {
			r := tc.reply()
			ahead := r.Split()
			b.ReportAllocs()
			for b.Loop() {
				r.Join(ahead)
				unjoin(r)
			}
		})
	}
}
