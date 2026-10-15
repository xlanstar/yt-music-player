package main

import "github.com/egoist/mygo/ui"

func (a *app) syncPlayback(c *ui.Context) {
	if n, ok := a.playback.TakeNotice(); ok {
		a.tell(c, n)
	}
	if nextFrame := a.playback.Follow(); nextFrame > 0 {
		c.After(nextFrame)
	}
}
