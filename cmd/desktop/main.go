// Command yt-music-player is a lightweight YouTube Music desktop player.
package main

import (
	"os"
	"yt-music-player/internal/ipc"
	"yt-music-player/internal/logfile"
	"yt-music-player/internal/remote"
	"yt-music-player/internal/settings"
	"yt-music-player/internal/theme"
	"yt-music-player/internal/version"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/updater"
	"github.com/egoist/mygo/plugins/updater/native"
	"github.com/egoist/mygo/ui"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	runYtm()
	// Unpackaged (go run, go build), MyGo names the app after its
	// executable, which go run names after this directory: keep the name
	// its menus, data and single-instance lock had before cmd/desktop.
	if !mygo.App.IsPackaged() {
		mygo.App.SetName("yt-music-player")
	}
	// zerolog's global level only: MyGo's own log.Printf output (frame
	// stats) is not affected.
	level, err := logfile.Level(os.Getenv("YT_LOG"))
	zerolog.SetGlobalLevel(level)
	if err != nil {
		log.Warn().Err(err).Msg("YT_LOG")
	}
	var a *app
	if demo {
		a = newDemoApp(nil)
	} else {
		a = newApp(nil)
	}
	// Connecting, starting ytmd if need be, takes a while: from now,
	// while the window opens. What it reports waits for the window.
	if d, err := systemYtmd(); err == nil {
		a.connect(d)
	} else {
		log.Error().Err(err).Msg("ytmd's paths")
		a.daemon.link.Health = remote.Unavailable
	}
	// Before the window is made, so it opens in the user's theme.
	if dir, err := settings.DefaultDir(); err == nil {
		a.useSettings(dir)
	}
	a.quiet.start()
	// In the app's language. Builds that cannot update themselves (go run,
	// the placeholder) never check; Check for Updates… says why.
	mygo.Use(native.New(updater.Options{}))
	mygo.App.SetMenu(appMenu())
	// Items labelled by their role follow the language themselves; Check
	// for Updates… is labelled once.
	mygo.App.OnLocaleChanged(func(string) { mygo.App.SetMenu(appMenu()) })
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
	// unfocused, the position draws fewer frames or none; regaining sight
	// goes through Update so a frame shows where the song is. Coming to
	// the front checks that ytmd answers.
	a.progress.setFocused(w.IsFocused())
	w.OnFocus(func() {
		a.foreground()
		w.Update(func() { a.progress.setFocused(true) })
	})
	w.OnBlur(func() { a.progress.setFocused(false) })
	w.OnMinimize(func() { a.progress.setShown(false) })
	w.OnHide(func() { a.progress.setShown(false) })
	w.OnRestore(func() { w.Update(func() { a.progress.setShown(true) }) })
	w.OnShow(func() { w.Update(func() { a.progress.setShown(true) }) })
	stopNetwork := a.followNetwork()
	w.OnClosed(func() {
		stopNetwork()
		a.stopWork()
	})
	harness(a, w)
}

// appVersion is the app's: mygo.json's, or a development build's, which
// takes part in no version check (docs/plans/daemon.md §8).
func appVersion() string {
	if mygo.IsDev() {
		return version.Dev
	}
	return mygo.App.Version()
}

// systemYtmd is the ytmd of this build: its socket, under the names of a
// development or placeholder build, and the ytmd found beside the app or
// at YTMD_PATH to start.
func systemYtmd() (ytmd, error) {
	paths, err := ipc.Default(mygo.IsDev())
	if err != nil {
		return ytmd{}, err
	}
	return ytmd{
		dialer:  &ipc.Dialer{Socket: paths.Socket, Spawn: ipc.SpawnDaemon},
		version: appVersion(),
		log:     paths.Log("ytmd"),
	}, nil
}
