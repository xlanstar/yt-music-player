package daemon

import (
	"yt-music-player/internal/account"
	"yt-music-player/internal/api"
	"yt-music-player/internal/player"
)

// The wire's enums count from 1, so that a zero is never valid; player's
// and account's count from 0.

func wireRepeat(r player.Repeat) api.Repeat { return api.Repeat(r) + 1 }

func wirePart(p account.Part) api.Part { return api.Part(p) + 1 }

// accountPart is p of a valid call (api.Call.Validate).
func accountPart(p api.Part) account.Part { return account.Part(p - 1) }

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

func failedEvent(f account.Failure) *api.FailedEvent {
	return &api.FailedEvent{Part: wirePart(f.Part), Err: api.Encode(f.Err)}
}
