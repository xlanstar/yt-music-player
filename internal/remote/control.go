package remote

import (
	"yt-music-player/internal/api"
	"yt-music-player/internal/catalog"
)

// Controller is playback and the queue, as a front-end's UI drives them.
// Its methods send at once, in order, and never block; only the daemon's
// events change the Mirror. A request that fails or goes unanswered is
// told as notice.ControlFailed, and a timeout pings the daemon.
type Controller interface {
	// Play plays ids from index start; songs describe them (the daemon
	// knows no song but those queued).
	Play(ids []string, start int, songs []catalog.Song)
	PlayAll(ids []string, songs []catalog.Song)
	Toggle()
	Pause()
	Resume()
	Next()
	Prev()
	Seek(seconds float64)
	// SetVolume sets the volume, 0 to 1.
	SetVolume(volume float64)
	ToggleMute()
	CycleRepeat()
	ToggleShuffle()
	// Enqueue queues ids after the current song if next, else at the end.
	Enqueue(ids []string, next bool, songs []catalog.Song)
	// Remove removes the queue's song at index.
	Remove(index int)
	Move(from, to int)
	ClearUpNext()
}

var _ Controller = (*Client)(nil)

// Play implements Controller.
func (c *Client) Play(ids []string, start int, songs []catalog.Song) {
	c.control(&api.Call{Method: api.MethodPlay, IDs: &api.IDsArgs{IDs: ids, Start: start, Songs: songs}})
}

// PlayAll implements Controller.
func (c *Client) PlayAll(ids []string, songs []catalog.Song) {
	c.control(&api.Call{Method: api.MethodPlayAll, IDs: &api.IDsArgs{IDs: ids, Songs: songs}})
}

// Toggle implements Controller.
func (c *Client) Toggle() { c.control(&api.Call{Method: api.MethodToggle}) }

// Pause implements Controller.
func (c *Client) Pause() { c.control(&api.Call{Method: api.MethodPause}) }

// Resume implements Controller.
func (c *Client) Resume() { c.control(&api.Call{Method: api.MethodResume}) }

// Next implements Controller.
func (c *Client) Next() { c.control(&api.Call{Method: api.MethodNext}) }

// Prev implements Controller.
func (c *Client) Prev() { c.control(&api.Call{Method: api.MethodPrev}) }

// Seek implements Controller.
func (c *Client) Seek(seconds float64) {
	c.control(&api.Call{Method: api.MethodSeek, Seek: &api.SeekArgs{Seconds: seconds}})
}

// SetVolume implements Controller.
func (c *Client) SetVolume(volume float64) {
	c.control(&api.Call{Method: api.MethodSetVolume, Volume: &api.VolumeArgs{Volume: volume}})
}

// ToggleMute implements Controller.
func (c *Client) ToggleMute() { c.control(&api.Call{Method: api.MethodToggleMute}) }

// CycleRepeat implements Controller.
func (c *Client) CycleRepeat() { c.control(&api.Call{Method: api.MethodCycleRepeat}) }

// ToggleShuffle implements Controller.
func (c *Client) ToggleShuffle() { c.control(&api.Call{Method: api.MethodToggleShuffle}) }

// Enqueue implements Controller.
func (c *Client) Enqueue(ids []string, next bool, songs []catalog.Song) {
	c.control(&api.Call{Method: api.MethodEnqueue, IDs: &api.IDsArgs{IDs: ids, Next: next, Songs: songs}})
}

// Remove implements Controller.
func (c *Client) Remove(index int) {
	c.control(&api.Call{Method: api.MethodRemove, Index: &api.IndexArgs{Index: index}})
}

// Move implements Controller.
func (c *Client) Move(from, to int) {
	c.control(&api.Call{Method: api.MethodMove, Move: &api.MoveArgs{From: from, To: to}})
}

// ClearUpNext implements Controller.
func (c *Client) ClearUpNext() { c.control(&api.Call{Method: api.MethodClearUpNext}) }
