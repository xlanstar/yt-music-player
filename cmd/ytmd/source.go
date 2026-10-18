//go:build !placeholder

package main

import (
	"yt-music-player/internal/audio"
	"yt-music-player/internal/auth"
	"yt-music-player/internal/daemon"
	"yt-music-player/internal/stream"
	"yt-music-player/internal/ytmusic"

	"github.com/rs/zerolog/log"
)

// options play YouTube Music, signed in with the stored credentials.
func options() daemon.Options {
	store, err := auth.NewStore()
	if err != nil {
		// Signing in still works, for this run only.
		log.Warn().Err(err).Msg("credentials store")
		store = &auth.MemoryStore{}
	}
	return daemon.Options{
		Source: ytmusic.New(nil, store),
		// yt-dlp is only a fallback: it tracks YouTube's changes within
		// days but takes seconds per song.
		Resolver:  stream.New(stream.Options{YTDLP: "yt-dlp"}),
		NewEngine: audio.New,
	}
}
