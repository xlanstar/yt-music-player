package main

import (
	"strconv"
	"time"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/player"
	"yt-music-player/internal/theme"

	"github.com/egoist/mygo/ui"
)

// sideZoneWidth is the width of each player bar side; equal widths keep the
// transport centered on the window.
const sideZoneWidth = 300

// What one arrow press moves: the position in seconds, the volume from 0 to 1.
const (
	seekStep   = 5
	volumeStep = 0.05
)

func (a *app) playerBar(c *ui.Context) {
	bar := ui.Row(c).Height(th.Layout.Player).Shrink(0).Padding(0, th.Space(6), 0, th.Space(5)).Gap(th.Space(4)).
		AlignItems(ui.Center)
	// On top, the bar is the title bar: it drags the window, and its side
	// zones keep clear of the window controls.
	onTop := th.Layout.PlayerBar == theme.Top
	if onTop {
		bar.DragWindow()
	}
	bar.Children(func() {
		song := a.currentSong()
		a.songZone(c, song, onTop)
		ui.Row(c).Grow(1).MinWidth(0).Justify(ui.Center).Children(func() {
			ui.Column(c).Grow(1).MaxWidth(640).Gap(th.Space(1)).AlignItems(ui.Center).Children(func() {
				a.transport(c)
				a.seekRow(c, song)
			})
		})
		a.volumeZone(c, onTop)
	})
}

func (a *app) songZone(c *ui.Context, song catalog.Song, onTop bool) {
	zone := ui.Row(c).Width(sideZoneWidth).Shrink(0).Gap(th.Space(3.5)).AlignItems(ui.Center)
	if onTop {
		zone.Padding(0, 0, 0, c.TitleBar().Left)
	}
	zone.Children(func() {
		if th.Layout.PanelSide == theme.Hidden {
			// No panel to show the queue in.
			barArt(c, song.Art)
		} else {
			art := ui.ButtonBase(c.Key("np-art")).Radius(th.Radius.Small).Label("Now playing").Tooltip("Show queue").
				Cursor(ui.CursorPointer).KeepFocus()
			art.Children(func() { barArt(c, song.Art) })
			if art.Clicked() {
				a.panel.isShown, a.panel.tab = true, tabQueue
			}
		}
		ui.Column(c).Shrink(1).MinWidth(0).Gap(th.Space(1)).Children(func() {
			ui.Text(c, song.Title).FontSize(th.Type.Title).FontWeight(600).SingleLine()
			ui.Text(c, song.Artist).FontSize(th.Type.Sub).TextColor(th.Color.Muted).SingleLine()
		})
		a.likeButton(c, song.ID, 34)
	})
}

func (a *app) seekRow(c *ui.Context, song catalog.Song) {
	ui.Row(c).FillWidth().Gap(th.Space(2)).AlignItems(ui.Center).Children(func() {
		length := a.playback.SongLength(song)
		ui.Text(c, clock(int(a.player.Position))).FontSize(th.Type.Time).TextColor(th.Color.Muted).FontFeatures("tnum").
			Width(40).TextAlign(ui.End)
		spoken := a.bar.seekValue(int(a.player.Position), int(length))
		// At least a second, so the bar never divides by zero.
		seekBar(c, &a.bar.seekPaint, "seek", &a.player.Position, max(length, 1), "Seek", spoken, seekStep).Grow(1)
		ui.Text(c, clock(int(length))).FontSize(th.Type.Time).TextColor(th.Color.Muted).FontFeatures("tnum").Width(40)
	})
}

func (a *app) volumeZone(c *ui.Context, onTop bool) {
	zone := ui.Row(c).Width(sideZoneWidth).Shrink(0).Gap(th.Space(1)).Justify(ui.End).AlignItems(ui.Center)
	if onTop {
		zone.Padding(0, c.TitleBar().Right, 0, 0)
	}
	zone.Children(func() {
		// The volume slider stays out of the focus group, which would take
		// its arrow keys.
		ui.Row(c).Gap(th.Space(1)).AlignItems(ui.Center).FocusGroup(ui.Horizontal).Children(func() {
			if th.Layout.PanelSide != theme.Hidden {
				a.panelButton(c, "lyrics", icLyrics, "Lyrics", tabLyrics)
				a.panelButton(c, "queue", icQueue, "Queue", tabQueue)
			}
			silent := a.player.IsSilent()
			mute := iconOpts{isToggle: true, isChecked: silent}
			muteIcon := icVolume
			if silent {
				muteIcon, mute.tip = icVolumeMuted, "Unmute"
			}
			if iconButton(c, "mute", muteIcon, "Mute", 36, 18, mute).Clicked() {
				a.player.ToggleMute()
			}
		})
		spoken := percent(int(a.player.Volume*100 + 0.5))
		volume := seekBar(c, &a.bar.volumePaint, "volume", &a.player.Volume, 1, "Volume", spoken, volumeStep).Width(112)
		if a.player.Muted {
			volume.Opacity(0.4)
		}
		if volume.Changed() {
			a.player.Muted = false
		}
	})
}

// panelButton shows the panel at tab, or hides it when tab already shows,
// so the lyrics and queue buttons switch tabs rather than fight.
func (a *app) panelButton(c *ui.Context, key any, icon *ui.SVG, label string, tab int) {
	open := a.panel.isShown && a.panel.tab == tab
	if iconButton(c, key, icon, label, 36, 18, iconOpts{isOn: open, isToggle: true, isChecked: open}).Clicked() {
		a.panel.isShown, a.panel.tab = !open, tab
	}
}

func barArt(c *ui.Context, art catalog.Art) {
	coverArt(c, art, 56, 56, th.Radius.Small).Shadow(0, 4, 14, 0, th.Color.Shadow.Alpha(0.5))
}

// barText caches the player bar's and the panel's strings and closures
// until their inputs change: they build every frame, and a frame that
// changes none of them must allocate nothing.
type barText struct {
	seek                   kept[[2]int, string] // by position and length
	from                   kept[string, string] // by album
	upNext                 []string
	tint                   ui.Color
	paintTint              func(*ui.Painter, ui.Rect)
	seekPaint, volumePaint seekPaint
}

// seekValue is the seek bar's value as screen readers say it.
func (b *barText) seekValue(pos, length int) string {
	if b.seek.stale([2]int{pos, length}) {
		b.seek.v = clock(pos) + " of " + clock(length)
	}
	return b.seek.v
}

// percents caches percent's results so the volume allocates nothing.
var percents [101]string

func percent(n int) string {
	if n < 0 || n >= len(percents) {
		return strconv.Itoa(n) + "%"
	}
	if percents[n] == "" {
		percents[n] = strconv.Itoa(n) + "%"
	}
	return percents[n]
}

func (a *app) transport(c *ui.Context) {
	ui.Row(c).Gap(th.Space(3.5)).AlignItems(ui.Center).FocusGroup(ui.Horizontal).Children(func() {
		a.shuffleButton(c, 34, 17)
		prev := iconOpts{fg: th.Color.Text, keys: hints.prev}
		if iconButton(c, "prev", icSkipBack, "Previous", 38, 20, prev).Clicked() {
			a.player.Prev()
		}
		a.playButton(c)
		next := iconOpts{fg: th.Color.Text, keys: hints.next}
		if iconButton(c, "next", icSkipForward, "Next", 38, 20, next).Clicked() {
			a.player.Next()
		}
		a.repeatButton(c)
	})
}

func (a *app) playButton(c *ui.Context) {
	icon, label := icPlay, "Play"
	if a.player.Playing {
		icon, label = icPause, "Pause"
	}
	play := ui.ButtonBase(c.Key("play")).Size(44, 44).Radius(22).Center().Background(th.Color.Text).
		Label(label).Cursor(ui.CursorPointer).KeepFocus()
	tip(c, play, label, hints.play)
	// A pose, as liftMotion explains.
	grow := boolToFloat(play.Hovered())
	if play.Pressed() {
		grow = -0.6
	}
	play.AnimatePose(ui.Pose{Scale: 1 + 0.06*grow}, ui.Animation{Duration: 120 * time.Millisecond})
	play.Children(func() { ui.Icon(c, icon).Size(18, 18).TextColor(th.Color.Base) })
	if play.Clicked() {
		a.player.TogglePlay()
	}
}

// repeatButton's label stays "Repeat"; screen readers read the mode as its
// value.
func (a *app) repeatButton(c *ui.Context) {
	icon, mode, opts := icRepeat, "Off", iconOpts{tip: "Repeat off"}
	switch a.player.Repeat {
	case player.RepeatAll:
		mode, opts.tip = "All", "Repeat all"
	case player.RepeatOne:
		icon, mode, opts.tip = icRepeatOne, "One", "Repeat one"
	}
	opts.isOn = a.player.Repeat != player.RepeatOff
	if iconButton(c, "repeat", icon, "Repeat", 34, 17, opts).Value(mode).Clicked() {
		a.player.CycleRepeat()
	}
}
