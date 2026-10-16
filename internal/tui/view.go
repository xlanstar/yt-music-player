package tui

import (
	"math"
	"strings"
	"yt-music-player/internal/api"
	"yt-music-player/internal/remote"
	"yt-music-player/locales"

	"charm.land/lipgloss/v2"
)

// The screen's fixed parts, in lines.
const (
	headerLines = 2 // the tabs and their rule
	barLines    = 4 // the player bar's rule, song, progress and status
	// minBody is the least body worth showing; a shorter terminal shows
	// only the player bar.
	minBody = 1
)

// appName is the app's name, untranslated.
const appName = "YT Music Player"

// render draws the screen into m.content: the tabs, the shown page, the
// daemon's health when it is not connected, and the player bar at the
// bottom. Lists draw only their visible rows, so a long queue costs no
// more than a short one.
func (m *model) render() {
	w, h := m.width, m.height
	if w <= 0 || h <= 0 {
		m.content = ""
		return
	}
	lines := make([]string, 0, h+barLines)
	banner := m.banner(w)
	body := m.bodyHeight()
	if body >= minBody {
		lines = m.appendHeader(lines, w)
		lines = m.appendBody(lines, w, body)
	}
	if banner != "" {
		lines = append(lines, banner)
	}
	lines = m.appendBar(lines, w)
	if len(lines) > h {
		// Too short for the bar: its last lines.
		lines = lines[len(lines)-h:]
	}
	m.content = strings.Join(lines, "\n")
}

// bodyHeight is how many lines the page has.
func (m *model) bodyHeight() int {
	h := m.height - headerLines - barLines
	if m.link.Health != remote.Connected {
		h--
	}
	return h
}

// appendHeader adds the app's name, the tabs and the account, over a rule
// that marks the shown tab.
func (m *model) appendHeader(lines []string, w int) []string {
	var top, rule strings.Builder
	at := 0
	if w >= 60 {
		name := " " + appName + "  "
		styleAccentB.write(&top, name)
		at = width(name)
		rule.WriteString(strings.Repeat("─", at))
	}
	labels := [numTabs]string{tabQueue: m.words.queue, tabSearch: m.words.search, tabLibrary: m.words.library}
	for i, label := range labels {
		cell := " " + label + " "
		style, mark := paint{}, "─"
		if tab(i) == m.tab {
			style, mark = styleAccentB, "━"
		}
		style.write(&top, cell)
		top.WriteByte(' ')
		cw := width(cell)
		rule.WriteString(strings.Repeat(mark, cw))
		rule.WriteString("─")
		at += cw + 1
	}
	account := ""
	switch {
	case m.session.IsSignedIn():
		account = m.session.DisplayName()
	case m.isSignedOut():
		account = m.words.notSignedIn
	}
	line := spread(top.String(), styleMuted.Render(fit(account, max(w-at-2, 0))+" "), w)
	if at < w {
		rule.WriteString(strings.Repeat("─", w-at))
	}
	return append(lines, line, styleMuted.Render(fitStyled(rule.String(), w)))
}

// fitStyled cuts s, which may hold styles, to w cells.
func fitStyled(s string, w int) string {
	if width(s) <= w {
		return s
	}
	return truncate(s, w)
}

// appendBody adds the shown page, or the help, in exactly h lines.
func (m *model) appendBody(lines []string, w, h int) []string {
	start := len(lines)
	if m.isHelp {
		lines = m.appendHelp(lines, w, h)
	} else {
		p := m.page()
		for _, line := range p.header {
			lines = append(lines, fitStyled(line, w))
		}
		rows := h - len(p.header)
		switch {
		case p.title != "":
			lines = appendMessage(lines, w, &p)
		case p.list != nil:
			lines = m.appendList(lines, w, rows, &p)
		}
	}
	if len(lines)-start > h {
		lines = lines[:start+h]
	}
	for len(lines)-start < h {
		lines = append(lines, "")
	}
	return lines
}

// appendMessage adds a page's message: its title, detail and hint.
func appendMessage(lines []string, w int, p *page) []string {
	lines = append(lines, "", " "+styleBold.Render(fit(p.title, w-2)))
	if p.detail != "" {
		for line := range strings.Lines(lipgloss.Wrap(p.detail, max(w-2, 1), "")) {
			lines = append(lines, " "+styleMuted.Render(strings.TrimRight(line, "\n")))
		}
	}
	if p.hint != "" {
		lines = append(lines, "", " "+fitStyled(p.hint, w-2))
	}
	return lines
}

// appendList adds the rows of p's list that show in rows lines.
func (m *model) appendList(lines []string, w, rows int, p *page) []string {
	l := p.list
	l.fit(p.n, rows, p.selectable)
	end := min(l.offset+max(rows, 0), p.n)
	for i := l.offset; i < end; i++ {
		lines = append(lines, m.row(p.at(i), w, i == l.cursor))
	}
	return lines
}

// appendHelp adds the keys and what they do in h lines, in as many
// columns as that takes, each as wide as its text.
func (m *model) appendHelp(lines []string, w, h int) []string {
	lines = append(lines, "", " "+styleBold.Render(fit(m.words.helpTitle, w-2)))
	keys := m.words.helpKeys
	rows := max(h-2, 1)
	cols := (len(keys) + rows - 1) / rows
	rows = (len(keys) + cols - 1) / cols
	keyWidth := 0
	for _, k := range keys {
		keyWidth = max(keyWidth, width(k.keys))
	}
	doesWidth := make([]int, cols)
	for i, k := range keys {
		doesWidth[i/rows] = max(doesWidth[i/rows], width(k.does))
	}
	for r := range rows {
		var b strings.Builder
		left := w
		for c := range cols {
			i := c*rows + r
			if i >= len(keys) || left <= 0 {
				break
			}
			key := fit("   "+pad(keys[i].keys, keyWidth)+"  ", left)
			left -= width(key)
			does := fit(keys[i].does, max(left, 0))
			if c < cols-1 {
				does = pad(does, min(doesWidth[c], max(left, 0)))
			}
			left -= width(does)
			b.WriteString(styleKey.Render(key))
			b.WriteString(does)
		}
		lines = append(lines, b.String())
	}
	return lines
}

// keyHint shows a key and what it does.
func keyHint(key, does string) string { return styleKey.Render(key) + " " + does }

// banner is the line that says how the daemon is when it is not
// connected, with the key that helps; "" when connected.
func (m *model) banner(w int) string {
	var text string
	l := m.link
	switch l.Health {
	case remote.Connected:
		return ""
	case remote.Connecting:
		text = m.words.connecting
	case remote.Reconnecting:
		text = m.words.reconnecting
	case remote.Unresponsive:
		text = m.words.unresponsive + "  " + keyHint("R", m.words.restart)
	case remote.Unavailable:
		text = m.words.unavailable + "  " + keyHint("R", m.words.retry) + "  " + locales.TuiLog(m.daemonLog)
	case remote.DaemonOutdated:
		text = locales.TuiDaemonOutdated(l.Daemon.Hello.Version, l.Daemon.Version)
		if !l.Daemon.IsCompatible {
			text += "  " + keyHint("R", m.words.retry)
		}
	case remote.FrontendOutdated:
		text = locales.TuiFrontendOutdated(l.Daemon.Hello.Version, l.Daemon.Version)
	}
	return fitStyled(styleWarn.Render(" ! ")+text, w)
}

// appendBar adds the player bar: the current song, its progress, and
// the status line (shuffle, repeat, volume; a notice, or the help key).
func (m *model) appendBar(lines []string, w int) []string {
	st := m.mirror.State()
	lines = append(lines, styleMuted.Render(strings.Repeat("─", w)))
	song, ok := m.mirror.Song(st.Current)
	if !ok || st.Current == "" {
		lines = append(lines, " "+styleMuted.Render(fit(m.words.nothingPlaying, w-2)), "")
	} else {
		icon := "❙❙"
		switch {
		case st.Waiting:
			icon = "…"
		case st.Playing:
			icon = "▶"
		}
		heart := ""
		if m.session.Liked(song.ID) {
			heart = styleAccent.Render("♥") + " "
		}
		title := " " + styleAccent.Render(icon) + " " + styleBold.Render(song.Title)
		if song.Artist != "" {
			title += styleMuted.Render(" · " + song.Artist)
		}
		lines = append(lines, spread(title, heart, w), m.progress(st, w))
	}
	right := keyHint("?", m.words.help) + " "
	if m.notice != "" {
		right = styleAccent.Render(fit(m.notice, max(w*2/3, 1))) + " "
	}
	status := ""
	if m.link.Connection > 0 {
		// Before any daemon spoke, its volume is no one's.
		status = m.status(st)
	}
	return append(lines, spread(" "+styleMuted.Render(status), right, w))
}

// progress is the current song's time, as a bar between its elapsed and
// whole length.
func (m *model) progress(st api.State, w int) string {
	pos := m.mirror.Position(m.now())
	elapsed := clock(pos)
	if st.Duration <= 0 {
		return " " + elapsed
	}
	total := clock(st.Duration)
	bar := w - width(elapsed) - width(total) - 4
	if bar < 4 {
		return " " + elapsed + " / " + total
	}
	done := min(int(pos/st.Duration*float64(bar)), bar)
	return " " + elapsed + " " + styleAccent.Render(strings.Repeat("━", done)) +
		styleMuted.Render(strings.Repeat("─", bar-done)) + " " + total
}

// status says what is on of shuffle and repeat, and the volume.
func (m *model) status(st api.State) string {
	var parts [3]string
	n := 0
	if st.Shuffled {
		parts[n] = m.words.shuffleOn
		n++
	}
	switch st.Repeat {
	case api.RepeatAll:
		parts[n] = m.words.repeatAll
		n++
	case api.RepeatOne:
		parts[n] = m.words.rOne
		n++
	}
	parts[n] = m.volumeText(st)
	n++
	return strings.Join(parts[:n], " · ")
}

func (m *model) volumeText(st api.State) string {
	percent := int(math.Round(st.Volume * 100))
	c := &m.memo.volume
	if !c.ok || c.percent != percent || c.muted != st.Muted {
		text := locales.CliVolume(percent)
		if st.Muted {
			text = locales.CliVolumeMuted(percent)
		}
		*c = volumeMemo{percent: percent, muted: st.Muted, text: text, ok: true}
	}
	return c.text
}
