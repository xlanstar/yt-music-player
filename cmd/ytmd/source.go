//go:build !placeholder

package main

import (
	"yt-music-player/internal/audio"
	"yt-music-player/internal/auth"
	"yt-music-player/internal/daemon"
	"yt-music-player/internal/httpclient"
	"yt-music-player/internal/innertube"
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
	// Shared, so they share connections; no timeout, for streams.
	hc := httpclient.New(0)
	api := innertube.New()
	api.HTTP = hc
	return daemon.Options{
		Source: ytmusic.New(api, store),
		// yt-dlp is only a fallback: it tracks YouTube's changes within
		// days but takes seconds per song.
		Resolver: stream.New(stream.Options{HTTP: hc, YTDLP: "yt-dlp"}),
		NewEngine: func(events func(audio.Event)) (audio.Engine, error) {
			return audio.New(events, hc)
		},
	}
}
