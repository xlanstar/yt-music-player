// Package accountcache keeps the signed-in account's library and liked
// songs on disk for the front-ends, so that one starting shows them at
// once, before its fetch through ytmd brings them anew (package session).
//
// A File is one JSON file (ipc.Paths.AccountCache), replaced whole
// (package atomicfile): front-ends saving at once leave the last one's.
// Its methods do I/O: call them off the UI thread.
package accountcache

import (
	"encoding/json/v2"
	"fmt"
	"io"
	"io/fs"
	"os"
	"yt-music-player/internal/atomicfile"
	"yt-music-player/internal/catalog"
)

// version is the file's "v"; a file of another loads as none.
const version = 2

// Saved is what a File keeps.
type Saved struct {
	// Account is whose they are; only its ID is kept, which tells
	// sign-ins apart (catalog.Account.ID).
	Account catalog.Account
	Library catalog.Library
	// Liked are the liked songs' IDs, newest first; LikedSongs describe
	// them, or some of them.
	Liked      []string
	LikedSongs []catalog.Song
}

// file is a File's contents.
type file struct {
	V          int                 `json:"v"`
	Account    struct{ ID string } `json:"account"`
	Library    catalog.Library     `json:"library"`
	Liked      []string            `json:"liked"`
	LikedSongs []catalog.Song      `json:"likedSongs"`
}

// File is the path of a cache; "" keeps nothing.
type File string

// Load reads the cache: fs.ErrNotExist without one, another error if it
// is unreadable. The songs share the strings they repeat
// (catalog.ShareStrings), as the session keeps them long.
func (f File) Load() (Saved, error) {
	if f == "" {
		return Saved{}, fs.ErrNotExist
	}
	r, err := os.Open(string(f))
	if err != nil {
		return Saved{}, err
	}
	defer r.Close() //nolint:errcheck // read only
	// Decoded as read: read whole first, its megabytes would be held too.
	var in file
	if err := json.UnmarshalRead(r, &in); err != nil {
		return Saved{}, fmt.Errorf("accountcache: %w", err)
	}
	if in.V != version {
		return Saved{}, fmt.Errorf("accountcache: version %d", in.V)
	}
	catalog.ShareStrings(in.Library.Songs)
	catalog.ShareStrings(in.LikedSongs)
	return Saved{
		Account:    catalog.Account{ID: in.Account.ID},
		Library:    in.Library,
		Liked:      in.Liked,
		LikedSongs: in.LikedSongs,
	}, nil
}

// Save replaces the cache with s, whole or not at all.
func (f File) Save(s *Saved) error {
	if f == "" {
		return nil
	}
	out := file{V: version, Library: s.Library, Liked: s.Liked, LikedSongs: s.LikedSongs}
	out.Account.ID = s.Account.ID
	// Into the file, not to bytes first: it takes megabytes. No Sync: a
	// cache a crash loses costs a fetch.
	return atomicfile.Write(string(f), atomicfile.Options{}, func(w io.Writer) error { return json.MarshalWrite(w, &out) })
}

// Remove deletes the cache, if any, and a crash's temporary files of it.
func (f File) Remove() error {
	if f == "" {
		return nil
	}
	return atomicfile.Remove(string(f))
}
