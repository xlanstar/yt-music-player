package main

import (
	"context"
	"math"
	"strconv"
	"yt-music-player/internal/api"
	"yt-music-player/internal/remote"
	"yt-music-player/locales"
)

// control sends a control without arguments: play, pause, toggle, next,
// prev.
func (e *env) control(ctx context.Context, m api.Method) int {
	return e.call(ctx, api.Call{Method: m})
}

// seekCommand is `ytm seek <sec|+sec|-sec>`: a signed amount moves from
// where the song is.
func (e *env) seekCommand(ctx context.Context, args []string) int {
	if len(args) != 1 {
		return e.usage("seek")
	}
	amount, ok := parseAmount(args[0])
	if !ok || (!amount.relative && amount.value < 0) {
		return e.usage("seek")
	}
	return e.adjust(ctx, amount, func(s *api.Status) api.Call {
		return api.Call{Method: api.MethodSeek, Seek: &api.SeekArgs{Seconds: seekTo(amount, s)}}
	})
}

// volumeCommand is `ytm volume <0-100|+n|-n>`: a signed amount changes
// the volume by that much.
func (e *env) volumeCommand(ctx context.Context, args []string) int {
	if len(args) != 1 {
		return e.usage("volume")
	}
	amount, ok := parseAmount(args[0])
	if !ok || (!amount.relative && (amount.value < 0 || amount.value > 100)) {
		return e.usage("volume")
	}
	return e.adjust(ctx, amount, func(s *api.Status) api.Call {
		return api.Call{Method: api.MethodSetVolume, Volume: &api.VolumeArgs{Volume: volumeTo(amount, s)}}
	})
}

// amount is a command's number: an absolute one, or a change with its
// sign.
type amount struct {
	value    float64
	relative bool
}

// parseAmount reads "12", "+5" or "-5".
func parseAmount(arg string) (amount, bool) {
	if arg == "" {
		return amount{}, false
	}
	relative := arg[0] == '+' || arg[0] == '-'
	v, err := strconv.ParseFloat(arg, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return amount{}, false
	}
	return amount{value: v, relative: relative}, true
}

// seekTo is where a seeks to, in seconds, from the status s: within the
// song, when its length is known.
func seekTo(a amount, s *api.Status) float64 {
	if !a.relative {
		return a.value
	}
	to := max(s.State.Position+a.value, 0)
	if d := s.State.Duration; d > 0 {
		to = min(to, d)
	}
	return to
}

// volumeTo is the volume a sets, 0 to 1, from the status s.
func volumeTo(a amount, s *api.Status) float64 {
	v := a.value
	if a.relative {
		v += float64(percent(s.State.Volume))
	}
	return min(max(v, 0), 100) / 100
}

// adjust sends the call build returns, which reads the daemon's status
// first when a is relative.
func (e *env) adjust(ctx context.Context, a amount, build func(*api.Status) api.Call) int {
	c, ok := e.open(ctx, locales.NoticeControlFailed())
	if !ok {
		return exitFailed
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	var s api.Status
	if a.relative {
		var err error
		if s, err = c.Status(ctx); err != nil {
			return e.fail(locales.NoticeControlFailed(), err)
		}
	}
	return e.callOn(ctx, c, build(&s))
}

// call connects, starting ytmd if need be, and sends call.
func (e *env) call(ctx context.Context, call api.Call) int {
	c, ok := e.open(ctx, locales.NoticeControlFailed())
	if !ok {
		return exitFailed
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	return e.callOn(ctx, c, call)
}

// callOn sends call over c and waits for the daemon to take it.
func (e *env) callOn(ctx context.Context, c *remote.Client, call api.Call) int {
	if _, err := c.Call(ctx, call); err != nil {
		return e.fail(locales.NoticeControlFailed(), err)
	}
	return exitOK
}
