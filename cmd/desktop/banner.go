package main

import (
	"net/url"
	"path/filepath"
	"strings"
	"yt-music-player/internal/remote"
	"yt-music-player/locales"

	"github.com/egoist/mygo/ui"
)

// bannerText is what the banner says of the connection to ytmd, "" when
// it shows nothing: connected, or connecting the first time.
type bannerText struct {
	text string
	icon *ui.SVG
}

// bannerOf is the banner for l, memoized on it: a Link changes seldom.
func (a *app) bannerOf(l remote.Link) bannerText {
	m := &a.memo.banner
	if !m.stale(l) {
		return m.v
	}
	daemon, ours := l.Daemon.Hello.Version, l.Daemon.Version
	switch l.Health {
	case remote.Reconnecting:
		m.v = bannerText{locales.DaemonReconnecting(), icRefresh}
	case remote.Unresponsive:
		m.v = bannerText{locales.DaemonUnresponsive(), icAlert}
	case remote.Unavailable:
		m.v = bannerText{locales.DaemonUnavailable(), icUnplug}
	case remote.DaemonOutdated:
		m.v = bannerText{locales.DaemonOutdated(daemon, ours), icAlert}
	case remote.FrontendOutdated:
		m.v = bannerText{locales.FrontendOutdated(daemon, ours), icAlert}
	default:
		m.v = bannerText{}
	}
	return m.v
}

// announceLink tells screen readers what the banner says as it changes:
// it may come and go without anyone looking.
func (a *app) announceLink(c *ui.Context) {
	h := a.daemon.link.Health
	if h == a.seen.health {
		return
	}
	a.seen.health = h
	if b := a.bannerOf(a.daemon.link); b.text != "" {
		c.Announce(b.text)
	}
}

// banner shows, over the top of the window, how the connection to ytmd
// is when it is not as it should be (docs/daemon.md, Health): a state
// that lasts until the connection mends, not a notice, so tell never
// says it. Its actions are what may mend it.
func (a *app) banner(c *ui.Context) {
	l := a.daemon.link
	b := a.bannerOf(l)
	if b.text == "" {
		return
	}
	ty, col := &th.Type, &th.Color
	// The row only centers the banner: the window's top stays draggable
	// and clickable beside it.
	ui.Row(c).Absolute().Top(th.Space(2)).Left(0).Right(0).Justify(ui.Center).PassThrough().Children(func() {
		panel := ui.Row(c.Key("daemon-banner")).AlignItems(ui.Center).Gap(th.Space(3)).MaxWidth(720).
			Padding(th.Space(2), th.Space(2.5), th.Space(2), th.Space(4)).Radius(th.Radius.Control).
			Background(col.Raised).Border(1, col.Line).Shadow(0, 8, 24, 0, col.Shadow.Alpha(0.4)).
			Role(ui.RoleStatus).Label(b.text)
		panel.Children(func() {
			ui.Icon(c, b.icon).Size(16, 16).Shrink(0).TextColor(col.Error)
			ui.Text(c, b.text).FontSize(ty.Label).FontWeight(600).Shrink(1).MaxLines(2)
			a.bannerActions(c, l.Health)
		})
	})
}

func (a *app) bannerActions(c *ui.Context, h remote.Health) {
	if a.sup == nil {
		return
	}
	switch h {
	case remote.Unresponsive:
		if bannerButton(c, "daemon-restart", locales.DaemonRestart()) {
			a.sup.Restart()
		}
	case remote.Unavailable, remote.DaemonOutdated, remote.FrontendOutdated:
		if bannerButton(c, "daemon-retry", locales.Retry()) {
			a.sup.Retry()
		}
	default:
		return
	}
	if path := a.daemon.ytmd.log; path != "" && bannerButton(c, "daemon-log", locales.ViewLog()) {
		c.OpenURL(fileURL(path))
	}
}

func bannerButton(c *ui.Context, key, label string) bool {
	return pill(c, key, nil, label, pillGhost).Height(30).Padding(0, th.Space(3.5)).Clicked()
}

// fileURL is the file: URL of path, which opens in the system's viewer
// for it (Console for a log on macOS).
func fileURL(path string) string {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		// C:/Users/… on Windows.
		p = "/" + p
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}
