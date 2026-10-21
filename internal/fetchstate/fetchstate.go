// Package fetchstate names what the app fetches and how each fetch went.
// The loader and the session record their fetches in a Registry; pages
// read it. It fetches nothing.
package fetchstate

import "strconv"

// Kind is the type of a Resource.
type Kind uint8

// Resource kinds. The loader fetches pages and feeds; the session fetches
// Account, Library and Liked.
const (
	_ Kind = iota
	Album
	Playlist
	Home
	Explore
	Account
	Library
	Liked
)

var kindNames = [...]string{"none", "album", "playlist", "home", "explore", "account", "library", "liked"}

// String returns k's name for logs.
func (k Kind) String() string {
	if int(k) < len(kindNames) {
		return kindNames[k]
	}
	return "kind(" + strconv.Itoa(int(k)) + ")"
}

// Resource is something fetched. The zero Resource is nothing to fetch.
type Resource struct {
	Kind Kind
	ID   string // empty unless Album or Playlist
}

// State is the state of a fetch.
type State uint8

// The states of a fetch.
const (
	Idle State = iota // not asked for yet, or forgotten
	Loading
	Loaded
	Failed
	Missing   // the source has no such item
	SignedOut // it needs an account, and none is signed in
)

// Fetch is how fetching a resource went.
type Fetch struct {
	State State
	Err   error // set when Failed, or Loaded whose refresh failed
}

// Registry records each resource's Fetch. Its zero value is ready to use.
// UI thread only: fetches record results through their posted funcs.
type Registry struct {
	fetches map[Resource]Fetch
}

// Get returns res's Fetch: Idle when never asked for.
func (r *Registry) Get(res Resource) Fetch { return r.fetches[res] }

// Set records res's Fetch. Idle deletes res so forgotten resources cost
// no memory.
func (r *Registry) Set(res Resource, f Fetch) {
	if f.State == Idle {
		delete(r.fetches, res)
		return
	}
	if r.fetches == nil {
		r.fetches = map[Resource]Fetch{}
	}
	r.fetches[res] = f
}
