//go:build !placeholder

package main

import (
	"yt-music-player/internal/auth"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/ytmusic"

	"github.com/rs/zerolog/log"
)

// demo is a constant so that the release build links no placeholder
// catalog: source_placeholder.go has the offline fake.
const demo = false

// newSource returns YouTube Music.
func newSource() catalog.Source {
	store, err := auth.NewStore()
	if err != nil {
		// Signing in still works, for this run only.
		log.Warn().Err(err).Msg("credentials store")
		store = &auth.MemoryStore{}
	}
	return ytmusic.New(nil, store)
}
