// Command yt-music-player is a lightweight YouTube Music desktop player.
package main

import (
	"os"
	"yt-music-player/internal/audio"
	"yt-music-player/internal/settings"
	"yt-music-player/internal/stream"
	"yt-music-player/internal/theme"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/updater"
	"github.com/egoist/mygo/plugins/updater/native"
	"github.com/egoist/mygo/ui"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	// zerolog's global level only: MyGo's own log.Printf output (frame
	// stats) is not affected.
	level, err := logLevel(os.Getenv("YT_LOG"))
	zerolog.SetGlobalLevel(level)
	if err != nil {
		log.Warn().Err(err).Msg("YT_LOG")
	}
	var a *app
	if demo {
		a = newDemoApp(newSource())
	} else {
		a = newApp(newSource())
	}
	// Prefetching draws nothing, so it needs no frame.
	a.playback.Wake = mygo.RunOnMain
	if !demo {
		// yt-dlp is only a fallback: it tracks YouTube's changes within
		// days but takes seconds per song.
		a.playback.Resolver = stream.New(stream.Options{YTDLP: stream.LookYTDLP()})
		a.playback.NewEngine = audio.New
	}
	// Before the window is made, so it opens in the user's theme.
	if dir, err := settings.DefaultDir(); err == nil {
		a.useSettings(dir)
	}
	a.quiet.start()
	// English, as the rest of the app. Builds that cannot update themselves
	// (go run, the placeholder) never check; Check for Updates… says why.
	mygo.Use(native.New(updater.Options{Language: "en"}))
	mygo.App.SetMenu(appMenu())
	mygo.App.WhenReady(a.openWindow)
	if err := mygo.App.Run(); err != nil {
		log.Fatal().Err(err).Msg("run")
	}
}

// appMenu is MyGo's default menu bar with Check for Updates… after About,
// where Mac apps have it.
func appMenu() *mygo.Menu {
	return mygo.NewMenu([]*mygo.MenuItem{
		{Label: "YT Music Player", Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleAbout},
			updater.MenuItem(),
			mygo.Separator(),
			{Role: mygo.RoleServices},
			mygo.Separator(),
			{Role: mygo.RoleHide},
			{Role: mygo.RoleHideOthers},
			{Role: mygo.RoleUnhide},
			mygo.Separator(),
			{Role: mygo.RoleQuit},
		}},
		{Role: mygo.RoleFileMenu},
		{Role: mygo.RoleEditMenu},
		{Role: mygo.RoleViewMenu},
		{Role: mygo.RoleWindowMenu},
	})
}

func (a *app) openWindow() {
	w := mygo.NewWindow(mygo.WindowOptions{
		Title:     "YT Music Player",
		Width:     1360,
		Height:    860,
		MinWidth:  1040,
		MinHeight: 680,
		StateKey:  "main",
		// The view draws its own title bar so the panels reach the top
		// edge; the traffic lights sit over the sidebar.
		TitleBarStyle:   mygo.TitleBarHiddenInset,
		TitleBarHeight:  int(a.theme.Layout.TitleBar),
		BackgroundColor: theme.Hex(a.theme.Color.Base),
		Content:         ui.View(a.view),
	})
	a.updates.attach(w.Update)
	// Listeners run on the main thread, where the view builds. Hidden or
	// unfocused, playback draws no frames; regaining sight goes through
	// Update so a frame shows the current position.
	a.playback.SetFocused(w.IsFocused())
	w.OnFocus(func() { w.Update(func() { a.playback.SetFocused(true) }) })
	w.OnBlur(func() { a.playback.SetFocused(false) })
	w.OnMinimize(func() { a.playback.SetShown(false) })
	w.OnHide(func() { a.playback.SetShown(false) })
	w.OnRestore(func() { w.Update(func() { a.playback.SetShown(true) }) })
	w.OnShow(func() { w.Update(func() { a.playback.SetShown(true) }) })
	stopNetwork := a.followNetwork()
	w.OnClosed(func() {
		stopNetwork()
		a.stopWork()
	})
	harness(a, w)
}
