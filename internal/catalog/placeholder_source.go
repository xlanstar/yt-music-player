package catalog

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
)

// PlaceholderSource serves the placeholder catalog through the Source
// interface, so the UI fetches it as it would YouTube Music.
type PlaceholderSource struct {
	// cat is private to the source and read-only, which makes the source
	// safe for concurrent use.
	cat     *Catalog
	artists []Artist
	delay   time.Duration
	// songText and albumText hold Search's lowercased fields by catalog
	// index, so typing doesn't lowercase the catalog per keystroke.
	songText, albumText [][]string

	mu sync.Mutex
	// likes holds liked song IDs, newest first; guarded by mu.
	likes []string
}

var _ Source = (*PlaceholderSource)(nil)

// NewPlaceholderSource returns a Source of the placeholder music. It
// answers after delay to mimic a network, so loading states show.
func NewPlaceholderSource(delay time.Duration) *PlaceholderSource {
	return newPlaceholderSource(Placeholder(), delay)
}

// newPlaceholderSource takes cat so benchmarks can pass a large one.
func newPlaceholderSource(cat *Catalog, delay time.Duration) *PlaceholderSource {
	s := &PlaceholderSource{
		cat:       cat,
		artists:   AlbumArtists(cat.Albums),
		delay:     delay,
		songText:  make([][]string, len(cat.Songs)),
		albumText: make([][]string, len(cat.Albums)),
		likes:     PlaceholderLikes(),
	}
	for i, song := range cat.Songs {
		s.songText[i] = lower(song.Title, song.Artist, song.Album)
	}
	for i, album := range cat.Albums {
		s.albumText[i] = lower(album.Title, album.Artist)
	}
	return s
}

// lower lowercases fields in place.
func lower(fields ...string) []string {
	for i, field := range fields {
		fields[i] = strings.ToLower(field)
	}
	return fields
}

// Search implements Source. It matches songs by title, artist or album
// and albums by title or artist, in catalog order, ignoring case and
// surrounding space. A blank query finds nothing.
func (s *PlaceholderSource) Search(ctx context.Context, query string) (Results, error) {
	if err := s.wait(ctx); err != nil {
		return Results{}, err
	}
	return s.search(query), nil
}

func (s *PlaceholderSource) search(query string) Results {
	needle := strings.ToLower(strings.TrimSpace(query))
	var results Results
	if needle == "" {
		return results
	}
	for i, fields := range s.songText {
		if contains(fields, needle) {
			results.Songs = append(results.Songs, s.cat.Songs[i])
		}
	}
	for i, fields := range s.albumText {
		if contains(fields, needle) {
			results.Albums = append(results.Albums, s.cat.Albums[i])
		}
	}
	return results
}

func contains(fields []string, needle string) bool {
	return slices.ContainsFunc(fields, func(field string) bool { return strings.Contains(field, needle) })
}

// Album implements Source.
func (s *PlaceholderSource) Album(ctx context.Context, id string) (Album, []Song, error) {
	if err := s.wait(ctx); err != nil {
		return Album{}, nil, err
	}
	album, ok := s.cat.Album(id)
	if !ok {
		return Album{}, nil, fmt.Errorf("album %q: %w", id, ErrNotFound)
	}
	return album, s.cat.AlbumTracks(id), nil
}

// Playlist implements Source. LikedID returns the songs liked through Like.
func (s *PlaceholderSource) Playlist(ctx context.Context, id string) (Playlist, []Song, error) {
	if err := s.wait(ctx); err != nil {
		return Playlist{}, nil, err
	}
	if id == LikedID {
		s.mu.Lock()
		ids := slices.Clone(s.likes)
		s.mu.Unlock()
		liked := Playlist{ID: LikedID, Name: "Liked music", Owner: placeholderUser, Songs: ids}
		return liked, s.cat.SongsByID(ids), nil
	}
	playlist, ok := s.cat.Playlist(id)
	if !ok {
		return Playlist{}, nil, fmt.Errorf("playlist %q: %w", id, ErrNotFound)
	}
	// The caller keeps it; don't share songs other goroutines read.
	playlist.Songs = slices.Clone(playlist.Songs)
	return playlist, s.cat.SongsByID(playlist.Songs), nil
}

func (s *PlaceholderSource) wait(ctx context.Context) error {
	if s.delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(s.delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

const quickPicks = 12

// Home implements Source. Playlists come without songs, as in YouTube
// Music's feed.
func (s *PlaceholderSource) Home(ctx context.Context) (Home, error) {
	if err := s.wait(ctx); err != nil {
		return Home{}, err
	}
	picks := Shelf{Title: "Quick picks", Strapline: "Start radio from a song"}
	songs := s.cat.Songs[:min(quickPicks, len(s.cat.Songs))]
	picks.Items = make([]Item, len(songs))
	for i, song := range songs {
		picks.Items[i] = Item{Kind: ItemSong, Song: song}
	}
	albums := Shelf{Title: "Albums for you", Strapline: "Based on your listening"}
	albums.Items = make([]Item, len(s.cat.Albums))
	for i, album := range s.cat.Albums {
		albums.Items[i] = Item{Kind: ItemAlbum, Album: album}
	}
	mixes := Shelf{Title: "Mixed for you", Strapline: "Made for you"}
	mixList := placeholderMixes()
	mixes.Items = make([]Item, len(mixList))
	for i, mix := range mixList {
		mix.Songs = nil
		mixes.Items[i] = Item{Kind: ItemPlaylist, Playlist: mix}
	}
	artists := Shelf{Title: "Artists you love"}
	artists.Items = make([]Item, len(s.artists))
	for i, artist := range s.artists {
		artists.Items[i] = Item{Kind: ItemArtist, Artist: artist}
	}
	return Home{Shelves: []Shelf{picks, albums, mixes, artists}}, nil
}

// Explore implements Source. New releases are the albums, last added
// first.
func (s *PlaceholderSource) Explore(ctx context.Context) (Explore, error) {
	if err := s.wait(ctx); err != nil {
		return Explore{}, err
	}
	releases := slices.Clone(s.cat.Albums)
	slices.Reverse(releases)
	return Explore{
		NewReleases: releases,
		Moods:       placeholderMoods(),
		Trending:    s.cat.SongsByID(placeholderTrending()),
	}, nil
}

// placeholderUser is always signed in, so the placeholder needs no
// Accounts.
const placeholderUser = "Lan"

// Account implements Source.
func (s *PlaceholderSource) Account(ctx context.Context) (Account, error) {
	if err := s.wait(ctx); err != nil {
		return Account{}, err
	}
	return Account{Name: placeholderUser, Handle: "@lan"}, nil
}

// Library implements Source: the saved playlists and every song, album
// and artist, all copied so the caller may keep them.
func (s *PlaceholderSource) Library(ctx context.Context) (Library, error) {
	if err := s.wait(ctx); err != nil {
		return Library{}, err
	}
	playlists := slices.Clone(s.cat.LibraryPlaylists())
	for i := range playlists {
		playlists[i].Songs = slices.Clone(playlists[i].Songs)
	}
	return Library{
		Playlists: playlists,
		Albums:    slices.Clone(s.cat.Albums),
		Artists:   slices.Clone(s.artists),
		Songs:     slices.Clone(s.cat.Songs),
	}, nil
}

// Like implements Source. The last liked song goes first, as on YouTube
// Music; an unknown song is ErrNotFound.
func (s *PlaceholderSource) Like(ctx context.Context, songID string, liked bool) error {
	if err := s.wait(ctx); err != nil {
		return err
	}
	if _, ok := s.cat.Song(songID); !ok {
		return fmt.Errorf("song %q: %w", songID, ErrNotFound)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.likes = slices.DeleteFunc(s.likes, func(id string) bool { return id == songID })
	if liked {
		s.likes = slices.Insert(s.likes, 0, songID)
	}
	return nil
}
