//go:build placeholder

package main

import (
	"cmp"
	"context"
	"os"
	"time"
	"yt-music-player/internal/audio"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/daemon"
	"yt-music-player/internal/stream"

	"github.com/rs/zerolog/log"
)

// options play the offline fake, slowed by YT_SOURCE_DELAY (as "800ms"),
// silently: its songs "play" on a clock, from the placeholder's queue when
// nothing was saved.
func options() daemon.Options {
	delay, err := time.ParseDuration(cmp.Or(os.Getenv("YT_SOURCE_DELAY"), "0"))
	if err != nil {
		log.Warn().Err(err).Msg("YT_SOURCE_DELAY")
	}
	return daemon.Options{
		Source:    catalog.NewPlaceholderSource(delay),
		Resolver:  fakeResolver{},
		NewEngine: newClockEngine,
		Seed:      catalog.Placeholder().Songs,
	}
}

// fakeResolver gives every song a stream that no one fetches.
type fakeResolver struct{}

func (fakeResolver) Resolve(_ context.Context, id string) (stream.Stream, error) {
	return stream.Stream{URL: "https://placeholder.invalid/" + id}, nil
}

func (fakeResolver) Forget(string) {}

// clockEngine is an audio.Fake whose songs play on the clock: ready at
// once, as long as the catalog says, ended when they reach it. It records
// no calls, as it runs for days.
type clockEngine struct {
	*audio.Fake
	events func(audio.Event)
	// at is the position when it last started or was sought; since is
	// when it started playing, zero while paused.
	at    float64
	since time.Time
	end   *time.Timer
}

func newClockEngine(events func(audio.Event)) (audio.Engine, error) {
	return &clockEngine{Fake: audio.NewFake(events), events: events}, nil
}

func (e *clockEngine) Load(m audio.Media, at float64) {
	e.stopEnd()
	e.Fake.Load(m, at)
	e.Dur = cmp.Or(m.Seconds, e.Info.Seconds)
	e.at, e.since = at, time.Time{}
	e.Calls = nil
	// Never from within a method, as Engine says.
	ready := audio.Event{Kind: audio.StreamReady, Load: e.Loads}
	go e.events(ready)
}

func (e *clockEngine) Play() {
	if e.Playing {
		return
	}
	e.Fake.Play()
	e.Calls = nil
	e.since = time.Now()
	e.armEnd()
}

func (e *clockEngine) Pause() {
	e.at, e.since = e.Position(), time.Time{}
	e.Fake.Pause()
	e.Calls = nil
	e.stopEnd()
}

func (e *clockEngine) Seek(sec float64) {
	e.Fake.Seek(sec)
	e.Calls = nil
	e.at = sec
	if e.Playing {
		e.since = time.Now()
		e.armEnd()
	}
}

func (e *clockEngine) SetVolume(v float64) {
	e.Fake.SetVolume(v)
	e.Calls = nil
}

func (e *clockEngine) Position() float64 {
	pos := e.at
	if !e.since.IsZero() {
		pos += time.Since(e.since).Seconds()
	}
	if e.Dur > 0 {
		pos = min(pos, e.Dur)
	}
	return pos
}

func (e *clockEngine) Close() {
	e.stopEnd()
	e.Fake.Close()
}

func (e *clockEngine) armEnd() {
	e.stopEnd()
	if e.Dur <= 0 {
		return
	}
	ended := audio.Event{Kind: audio.StreamEnded, Load: e.Loads}
	left := time.Duration((e.Dur - e.Position()) * float64(time.Second))
	e.end = time.AfterFunc(left, func() { e.events(ended) })
}

func (e *clockEngine) stopEnd() {
	if e.end != nil {
		e.end.Stop()
		e.end = nil
	}
}
