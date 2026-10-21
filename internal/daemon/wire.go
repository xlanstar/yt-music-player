package daemon

import (
	"yt-music-player/internal/account"
	"yt-music-player/internal/api"
	"yt-music-player/internal/player"
)

// The wire's enums count from 1, so that a zero is never valid; player's
// count from 0.

func wireRepeat(r player.Repeat) api.Repeat { return api.Repeat(r) + 1 }

// The account's Status and Likes convert as they are: a field added on
// one side fails to compile here until the other has it.

func accountEvent(st *account.Status) *api.AccountEvent {
	ev := api.AccountEvent(*st)
	return &ev
}

func likesEvent(l *account.Likes) *api.Likes {
	ev := api.Likes(*l)
	return &ev
}

func failedEvent(err error) *api.FailedEvent {
	return &api.FailedEvent{Err: api.Encode(err)}
}
