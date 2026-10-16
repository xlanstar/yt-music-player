package main

import (
	"context"
	"encoding/json/v2"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"
	"yt-music-player/internal/api"
	"yt-music-player/locales"
)

// statusCommand is `ytm status [--json]`.
func (e *env) statusCommand(ctx context.Context, args []string) int {
	asJSON := false
	switch {
	case len(args) == 1 && args[0] == "--json":
		asJSON = true
	case len(args) > 0:
		return e.usage("status")
	}
	c, peer, err := e.connect(ctx, false)
	switch {
	case errors.Is(err, errNotRunning):
		if asJSON {
			return e.writeJSON(stoppedJSON{}, exitStopped)
		}
		writeLine(e.stdout, locales.CliDaemonNotRunning())
		return exitStopped
	case err != nil:
		return e.fail(locales.CliStatusFailed(), err)
	}
	defer c.Close()
	if !e.checkPeer(peer) {
		if asJSON {
			// Scripts read the skew there too.
			return e.writeJSON(peerStatus(peer.Hello, false), exitFailed)
		}
		return exitFailed
	}
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	s, err := c.Status(ctx)
	if err != nil {
		return e.fail(locales.CliStatusFailed(), err)
	}
	if asJSON {
		return e.writeJSON(statusOf(&s), exitOK)
	}
	_, _ = io.WriteString(e.stdout, statusText(&s))
	return exitOK
}

// writeJSON writes v as a line of JSON and returns code.
func (e *env) writeJSON(v any, code int) int {
	if err := writeJSON(e.stdout, v); err != nil {
		return e.fail(locales.CliStatusFailed(), err)
	}
	return code
}

func writeJSON(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

// The JSON of `ytm status --json` (docs/daemon.md): its field
// names are an interface scripts read, held by testdata/status.json.
type (
	// stoppedJSON is no daemon running.
	stoppedJSON struct {
		Running bool `json:"running"`
	}
	// peerJSON is a daemon running, as it greeted.
	peerJSON struct {
		Running    bool   `json:"running"`
		Version    string `json:"version"`
		Proto      uint16 `json:"proto"`
		Compatible bool   `json:"compatible"`
	}
	statusJSON struct {
		peerJSON `json:",inline"`
		// State is playing, paused, or idle (an empty queue).
		State    string  `json:"state"`
		Position float64 `json:"position"`
		Duration float64 `json:"duration"`
		Volume   int     `json:"volume"` // 0 to 100
		Muted    bool    `json:"muted"`
		// Repeat is off, all or one.
		Repeat  string `json:"repeat"`
		Shuffle bool   `json:"shuffle"`
		// Song is null when idle.
		Song    *songJSON   `json:"song"`
		Queue   queueJSON   `json:"queue"`
		Account accountJSON `json:"account"`
	}
	songJSON struct {
		ID      string   `json:"id"`
		Title   string   `json:"title"`
		Artists []string `json:"artists"`
		Album   string   `json:"album"`
	}
	queueJSON struct {
		Index  int `json:"index"` // from 0
		Length int `json:"length"`
	}
	accountJSON struct {
		SignedIn bool   `json:"signedIn"`
		Name     string `json:"name"`
	}
)

func peerStatus(h api.Hello, compatible bool) peerJSON {
	return peerJSON{Running: true, Version: h.Version, Proto: h.Proto, Compatible: compatible}
}

// statusOf returns s as JSON.
func statusOf(s *api.Status) statusJSON {
	st := &s.State
	j := statusJSON{
		peerJSON: peerStatus(api.Hello{Version: s.Version, Proto: s.Proto}, true),
		State:    playState(s),
		Position: st.Position,
		Duration: st.Duration,
		Volume:   percent(st.Volume),
		Muted:    st.Muted,
		Repeat:   repeatName(st.Repeat),
		Shuffle:  st.Shuffled,
		Queue:    queueJSON{Index: st.Index, Length: s.Length},
		Account:  accountJSON{SignedIn: s.SignedIn, Name: s.Account.Name},
	}
	if s.Length > 0 {
		song := &s.Song
		// catalog.Song has its artists joined as YouTube Music shows them,
		// so they stay one: splitting at commas would break names that
		// have one.
		var artists []string
		if song.Artist != "" {
			artists = []string{song.Artist}
		}
		j.Song = &songJSON{ID: song.ID, Title: song.Title, Artists: artists, Album: song.Album}
	}
	return j
}

// playState is playing, paused, or idle when the queue is empty.
func playState(s *api.Status) string {
	switch {
	case s.Length == 0:
		return "idle"
	case s.State.Playing:
		return "playing"
	}
	return "paused"
}

func repeatName(r api.Repeat) string {
	switch r {
	case api.RepeatAll:
		return "all"
	case api.RepeatOne:
		return "one"
	}
	return "off"
}

// percent turns a volume of 0 to 1 into 0 to 100.
func percent(v float64) int {
	return int(math.Round(min(max(v, 0), 1) * 100))
}

// statusText says s in a few lines.
func statusText(s *api.Status) string {
	st := &s.State
	var b strings.Builder
	if s.Length == 0 {
		b.WriteString(locales.CliQueueEmpty())
	} else {
		song := s.Song.Title
		if s.Song.Artist != "" {
			song = locales.SongByArtist(s.Song.Title, s.Song.Artist)
		}
		if st.Playing {
			b.WriteString(locales.NowPlayingSong(song))
		} else {
			b.WriteString(locales.CliPausedSong(song))
		}
		b.WriteString("\n  ")
		position := clock(st.Position)
		if st.Duration > 0 {
			position = locales.SeekValue(position, clock(st.Duration))
		}
		b.WriteString(position)
		b.WriteString(" · ")
		b.WriteString(locales.CliQueuePosition(st.Index+1, s.Length))
		if st.Waiting {
			b.WriteString(" · ")
			b.WriteString(locales.Loading())
		}
	}
	b.WriteByte('\n')
	if st.Muted {
		b.WriteString(locales.CliVolumeMuted(percent(st.Volume)))
	} else {
		b.WriteString(locales.CliVolume(percent(st.Volume)))
	}
	b.WriteString(" · ")
	switch st.Repeat {
	case api.RepeatAll:
		b.WriteString(locales.RepeatAll())
	case api.RepeatOne:
		b.WriteString(locales.RepeatOne())
	default:
		b.WriteString(locales.RepeatOff())
	}
	b.WriteString(" · ")
	if st.Shuffled {
		b.WriteString(locales.CliShuffleOn())
	} else {
		b.WriteString(locales.CliShuffleOff())
	}
	b.WriteByte('\n')
	if s.SignedIn {
		b.WriteString(locales.SignedInAs(accountName(s.Account.Name, s.Account.Handle)))
	} else {
		b.WriteString(locales.CliSignedOut())
	}
	b.WriteByte('\n')
	return b.String()
}

// accountName is the name, or the handle without one.
func accountName(name, handle string) string {
	if name == "" {
		return handle
	}
	return name
}

// clock writes seconds as m:ss, or h:mm:ss from an hour.
func clock(seconds float64) string {
	s := int(max(seconds, 0))
	h, m, sec := s/3600, s/60%60, s%60
	pad := func(n int) string {
		if n < 10 {
			return "0" + strconv.Itoa(n)
		}
		return strconv.Itoa(n)
	}
	if h > 0 {
		return strconv.Itoa(h) + ":" + pad(m) + ":" + pad(sec)
	}
	return strconv.Itoa(m) + ":" + pad(sec)
}
