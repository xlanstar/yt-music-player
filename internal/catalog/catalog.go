// Package catalog holds the music the player shows, keyed by stable string
// IDs so routes, queues and likes survive a reload or refetch.
package catalog

import "strings"

// Art names a release's artwork; the UI resolves it to a thumbnail or a
// generated cover.
type Art string

// IsURL reports artwork to fetch, not a generated cover as the placeholder's.
func (a Art) IsURL() bool {
	return strings.HasPrefix(string(a), "https://") || strings.HasPrefix(string(a), "http://")
}

// Song is a track.
type Song struct {
	ID, Title, Artist string
	// AlbumID is empty when the album is not in the catalog.
	Album, AlbumID string
	Seconds        int
	Art            Art
}

// Release is YouTube Music's release type.
type Release string

// Release types, by size.
const (
	ReleaseAlbum  Release = "Album"
	ReleaseEP     Release = "EP"
	ReleaseSingle Release = "Single"
)

// Album is a release by one artist.
type Album struct {
	ID, Title, Artist string
	Year              int
	Kind              Release
	Art               Art
}

// Playlist is a user's list of songs.
type Playlist struct {
	ID, Name, Owner string
	Art             Art
	Songs           []string
}

// Tint is an OKLCH color, so the catalog needn't import a UI toolkit.
type Tint struct{ L, C, H float32 }

// Mood is a mood or genre to browse by.
type Mood struct {
	Name string
	Tint Tint
	// ID is YouTube Music's "params" for the mood's page.
	ID string
}

// Key returns m's ID, or its name when it has none.
func (m Mood) Key() string {
	if m.ID != "" {
		return m.ID
	}
	return m.Name
}

// Artist is a performer, with the art of their first release.
type Artist struct {
	Name string
	Art  Art
	// ID is the artist's channel; empty when unknown.
	ID string
}

// Key returns the artist's channel, or the name when unknown.
func (a Artist) Key() string {
	if a.ID != "" {
		return a.ID
	}
	return a.Name
}

// ItemKind is the type of a feed Item.
type ItemKind uint8

// Feed item kinds, from 1: 0 is none, as in a Frame that lacks it
// (package api).
const (
	_ ItemKind = iota
	ItemSong
	ItemAlbum
	ItemPlaylist
	ItemArtist
)

// Item is a shelf entry; only the field matching Kind is set. A playlist's
// Songs are usually empty until fetched.
type Item struct {
	Kind     ItemKind
	Song     Song
	Album    Album
	Playlist Playlist
	Artist   Artist
}

// Key returns the ID of the item's kind (artist Key for artists). The UI
// keys elements by it, so a shelf keeps only the first item per key.
func (it *Item) Key() string {
	switch it.Kind {
	case ItemSong:
		return it.Song.ID
	case ItemAlbum:
		return it.Album.ID
	case ItemPlaylist:
		return it.Playlist.ID
	default:
		return it.Artist.Key()
	}
}

// Shelf is a titled row of a feed, in the source's order.
type Shelf struct {
	// Strapline is the small line above the title, e.g. "Start radio from
	// a song"; often empty.
	Title, Strapline string
	Items            []Item
}

// Home is the home feed.
type Home struct {
	Shelves []Shelf
}

// Explore is the explore page.
type Explore struct {
	NewReleases []Album
	Moods       []Mood
	// Trending is the chart, highest first.
	Trending []Song
}

// Catalog is the music known to the player, with lookups by ID. It is not
// safe for concurrent use: UI thread only.
type Catalog struct {
	Songs     []Song
	Albums    []Album
	Playlists []Playlist

	// Playlists also holds playlists only seen in a feed or opened;
	// libraryIDs and libraryPlaylists hold only the saved ones.
	libraryIDs       []string
	libraryPlaylists []Playlist

	songIndex     map[string]int
	albumIndex    map[string]int
	playlistIndex map[string]int
	lyrics        map[string][]string
	// albumTrackIDs keeps the track order of albums added by AddAlbum.
	albumTrackIDs map[string][]string
	gen           uint64
}

// New indexes the given music.
func New(songs []Song, albums []Album, playlists []Playlist) *Catalog {
	c := &Catalog{Songs: songs, Albums: albums, Playlists: playlists}
	c.index()
	return c
}

func (c *Catalog) index() {
	c.gen++
	c.songIndex = make(map[string]int, len(c.Songs))
	for i, song := range c.Songs {
		c.songIndex[song.ID] = i
	}
	c.albumIndex = make(map[string]int, len(c.Albums))
	for i, album := range c.Albums {
		c.albumIndex[album.ID] = i
	}
	c.playlistIndex = make(map[string]int, len(c.Playlists))
	for i, playlist := range c.Playlists {
		c.playlistIndex[playlist.ID] = i
	}
	c.indexLibrary()
}

func (c *Catalog) indexLibrary() {
	// A new slice, not the old one reused: the UI may hold the old one.
	// Sized for every ID, as nearly all are known playlists.
	c.libraryPlaylists = nil
	if len(c.libraryIDs) > 0 {
		c.libraryPlaylists = make([]Playlist, 0, len(c.libraryIDs))
	}
	for _, id := range c.libraryIDs {
		if playlist, ok := c.Playlist(id); ok {
			c.libraryPlaylists = append(c.libraryPlaylists, playlist)
		}
	}
}

// AlbumArtists returns each artist of albums once, in album order, with
// the art of their first album.
func AlbumArtists(albums []Album) []Artist {
	seen := map[string]bool{}
	var artists []Artist
	for _, album := range albums {
		if seen[album.Artist] {
			continue
		}
		seen[album.Artist] = true
		artists = append(artists, Artist{Name: album.Artist, Art: album.Art})
	}
	return artists
}

// Add merges music fetched from a Source: items with a known ID are
// replaced in place, new ones appended. It indexes only what it adds, as
// it runs for every page fetched.
func (c *Catalog) Add(songs []Song, albums []Album, playlists []Playlist) {
	c.gen++
	c.Songs = upsert(c.Songs, c.songIndex, songs, func(s Song) string { return s.ID })
	c.Albums = upsert(c.Albums, c.albumIndex, albums, func(a Album) string { return a.ID })
	for i, playlist := range playlists {
		// Feeds list playlists without songs; keep the songs already fetched.
		if known, ok := c.Playlist(playlist.ID); ok && playlist.Songs == nil {
			playlists[i].Songs = known.Songs
		}
	}
	c.Playlists = upsert(c.Playlists, c.playlistIndex, playlists, func(p Playlist) string { return p.ID })
	if len(playlists) > 0 {
		c.indexLibrary()
	}
}

// AddAlbum adds an album and its tracks, keeping their order.
func (c *Catalog) AddAlbum(album Album, tracks []Song) {
	c.Add(tracks, []Album{album}, nil)
	if c.albumTrackIDs == nil {
		c.albumTrackIDs = map[string][]string{}
	}
	ids := make([]string, len(tracks))
	for i, track := range tracks {
		ids[i] = track.ID
	}
	c.albumTrackIDs[album.ID] = ids
}

// Gen changes whenever Add, AddAlbum, KeepSongs or SetLibrary changes the
// music, so the UI can cache what it derives until then. Editing the
// exported slices in place does not change it.
func (c *Catalog) Gen() uint64 { return c.gen }

func upsert[T any](items []T, index map[string]int, add []T, id func(T) string) []T {
	for _, item := range add {
		if i, ok := index[id(item)]; ok {
			items[i] = item
			continue
		}
		index[id(item)] = len(items)
		items = append(items, item)
	}
	return items
}

// KeepSongs drops the songs at the indexes of Songs that keep rejects, the
// others in their order: a daemon's catalog holds only its queue's songs.
// keep may be asked twice an index. The slice Songs is new, as callers may
// hold the old one.
func (c *Catalog) KeepSongs(keep func(i int) bool) {
	n := 0
	for i := range c.Songs {
		if keep(i) {
			n++
		}
	}
	if n == len(c.Songs) {
		return
	}
	// Sized for n when many go, the index too (clear never shrinks it): a
	// queue replaced leaves twice its songs. Else room for songs added next.
	size := len(c.Songs)
	many := size-n > n/4
	if many {
		size = n
	}
	kept := make([]Song, 0, size)
	for i, s := range c.Songs {
		if keep(i) {
			kept = append(kept, s)
		}
	}
	c.gen++
	c.Songs = kept
	if many {
		c.songIndex = make(map[string]int, n)
	} else {
		clear(c.songIndex)
	}
	for i, s := range kept {
		c.songIndex[s.ID] = i
	}
}

// SongIDs returns the IDs of all songs, in catalog order.
func (c *Catalog) SongIDs() []string {
	ids := make([]string, len(c.Songs))
	for i, s := range c.Songs {
		ids[i] = s.ID
	}
	return ids
}

// SongIndex returns the index in Songs of the song with the given ID.
func (c *Catalog) SongIndex(id string) (int, bool) {
	i, ok := c.songIndex[id]
	return i, ok
}

// Song returns the song with the given ID.
func (c *Catalog) Song(id string) (Song, bool) {
	i, ok := c.songIndex[id]
	if !ok {
		return Song{}, false
	}
	return c.Songs[i], true
}

// SongsByID returns the known songs among ids, in their order.
func (c *Catalog) SongsByID(ids []string) []Song {
	songs := make([]Song, 0, len(ids))
	for _, id := range ids {
		if song, ok := c.Song(id); ok {
			songs = append(songs, song)
		}
	}
	return songs
}

// Album returns the album with the given ID.
func (c *Catalog) Album(id string) (Album, bool) {
	i, ok := c.albumIndex[id]
	if !ok {
		return Album{}, false
	}
	return c.Albums[i], true
}

// AlbumTracks returns an album's songs: in track order once added by
// AddAlbum, else in catalog order.
func (c *Catalog) AlbumTracks(albumID string) []Song {
	if ids, ok := c.albumTrackIDs[albumID]; ok {
		return c.SongsByID(ids)
	}
	var tracks []Song
	for _, song := range c.Songs {
		if song.AlbumID == albumID {
			tracks = append(tracks, song)
		}
	}
	return tracks
}

// Playlist returns the playlist with the given ID.
func (c *Catalog) Playlist(id string) (Playlist, bool) {
	i, ok := c.playlistIndex[id]
	if !ok {
		return Playlist{}, false
	}
	return c.Playlists[i], true
}

// SetLibrary sets the user's saved playlists, in order.
func (c *Catalog) SetLibrary(ids []string) {
	// The catalog alone reads its IDs: reuse their array, the old IDs past
	// the new ones cleared.
	old := len(c.libraryIDs)
	c.libraryIDs = append(c.libraryIDs[:0], ids...)
	if old > len(ids) {
		clear(c.libraryIDs[len(ids):old])
	}
	c.gen++
	c.indexLibrary()
}

// LibraryPlaylists returns the user's saved playlists. The slice is the
// catalog's own: don't modify it.
func (c *Catalog) LibraryPlaylists() []Playlist { return c.libraryPlaylists }

// Lyrics returns a song's lines, nil when it has none.
func (c *Catalog) Lyrics(songID string) []string { return c.lyrics[songID] }

// TotalSeconds returns the summed length of songs.
func TotalSeconds(songs []Song) int {
	sum := 0
	for _, s := range songs {
		sum += s.Seconds
	}
	return sum
}
