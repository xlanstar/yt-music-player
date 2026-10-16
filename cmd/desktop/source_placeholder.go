//go:build placeholder

package main

import (
	"cmp"
	"os"
	"time"
	"yt-music-player/internal/catalog"

	"github.com/rs/zerolog/log"
)

// The placeholder's songs are made up and cannot play.
const demo = true

// newSource returns the offline fake, slowed by YT_SOURCE_DELAY (as
// "800ms").
func newSource() catalog.Source {
	delay, err := time.ParseDuration(cmp.Or(os.Getenv("YT_SOURCE_DELAY"), "0"))
	if err != nil {
		log.Warn().Err(err).Msg("YT_SOURCE_DELAY")
	}
	return catalog.NewPlaceholderSource(delay)
}
