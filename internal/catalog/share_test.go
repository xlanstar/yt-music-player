package catalog

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"unsafe"
)

// decodedTracks are an album's tracks as decoding gives them: every
// string a copy of its own.
func decodedTracks() []Song {
	_, tracks := benchAlbum()
	for i := range tracks {
		s := &tracks[i]
		s.Artist, s.Album, s.AlbumID = strings.Clone(s.Artist), strings.Clone(s.Album), strings.Clone(s.AlbumID)
		s.Art = Art(strings.Clone(string(s.Art)))
	}
	return tracks
}

func TestShareStrings(t *testing.T) {
	songs := decodedTracks()
	songs[1].Album = "" // A song without one stays without.
	want := slices.Clone(songs)
	ShareStrings(songs)
	if !reflect.DeepEqual(songs, want) {
		t.Fatalf("songs changed: %+v, want %+v", songs, want)
	}
	first := songs[0]
	for _, s := range songs[2:] {
		for _, pair := range [][2]string{
			{first.Artist, s.Artist}, {first.Album, s.Album}, {first.AlbumID, s.AlbumID}, {string(first.Art), string(s.Art)},
		} {
			if unsafe.StringData(pair[0]) != unsafe.StringData(pair[1]) {
				t.Errorf("song %s: %q is a copy of its own", s.ID, pair[1])
			}
		}
	}
}
