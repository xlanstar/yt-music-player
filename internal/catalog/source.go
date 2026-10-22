package catalog

import (
	"context"
	"errors"
)

// ErrNotFound is returned by a Source for an ID it does not know.
var ErrNotFound = errors.New("catalog: not found")

// ErrSignedOut is returned by a Source for requests that need an account
// while none is signed in.
var ErrSignedOut = errors.New("catalog: signed out")

// ErrInvalidCredentials is wrapped by Accounts.SignIn's error for
// credentials that hold no usable session, so a caller tells them from a
// failure to check them without knowing the Source.
var ErrInvalidCredentials = errors.New("catalog: invalid credentials")

// LikedID is YouTube Music's playlist ID for the user's liked songs.
const LikedID = "LM"

// Account is the signed-in user.
type Account struct {
	Name string
	// Handle is the channel handle, e.g. "@lan"; often empty.
	Handle string
	Photo  Art
	// ID tells this sign-in from any other, the same user's again too, as
	// names do not: random, made at sign-in and kept with the credentials.
	ID string
}

// Library is what the user saved, each list in the source's order.
type Library struct {
	Playlists []Playlist // Songs usually empty until fetched
	Albums    []Album
	Artists   []Artist
	Songs     []Song
}

// Results are search results, in the source's order.
type Results struct {
	Songs  []Song
	Albums []Album
}

// IsEmpty reports whether the search found nothing.
func (r Results) IsEmpty() bool { return len(r.Songs) == 0 && len(r.Albums) == 0 }

// Source is where music comes from: YouTube Music or the placeholder.
// Methods may be slow and are called off the UI thread, so they must be
// safe for concurrent use and honor ctx.
type Source interface {
	Search(ctx context.Context, query string) (Results, error)
	// Album returns an album with its tracks in order.
	Album(ctx context.Context, id string) (Album, []Song, error)
	// Playlist returns a playlist with its songs in order; the playlist's
	// Songs holds their IDs.
	Playlist(ctx context.Context, id string) (Playlist, []Song, error)

	Home(ctx context.Context) (Home, error)
	Explore(ctx context.Context) (Explore, error)

	// Account, Library and Like return ErrSignedOut when no one is signed in.
	Account(ctx context.Context) (Account, error)
	Library(ctx context.Context) (Library, error)
	Like(ctx context.Context, songID string, liked bool) error
}

// Accounts signs a Source in and out. Methods are safe for concurrent
// use; credentials persist across launches.
type Accounts interface {
	// SignIn checks credentials (a copied Cookie header), stores them and
	// returns their account. Bad credentials return an error and leave
	// the current account signed in.
	SignIn(ctx context.Context, credentials string) (Account, error)
	SignOut() error
}
