package tui

import (
	"math"
	"strings"
	"yt-music-player/internal/api"
	"yt-music-player/internal/remote"
	"yt-music-player/locales"

	"charm.land/lipgloss/v2"
)

// The tabs' lines, top to bottom; tabsRows counts them.
const (
	tabsLine = iota // the app's name, the tabs and the account
	tabsRule        // the rule that marks the shown tab
	tabsRows
)

// The player bar's lines, top to bottom; barRows counts them.
const (
	barRule     = iota
	barSong     // the current song, beside its cover
	barProgress // its progress, beside its cover
	barStatus   // shuffle, repeat and volume; a notice, or the help key
	barRows
)

// appName is the app's name, untranslated.
const appName = "YT Music Player"

// render prepares the screen's frame and draws it.
func (m *model) render() {
	f := m.prepare()
	m.draw(&f)
}

// draw draws f into m.content, and m.hits, what a click acts on, changing
// nothing else: the tabs, the page, the daemon's health unless connected,
// the player bar. Lists draw only their visible rows.
func (m *model) draw(f *frame) {
	w, h := m.width, m.height
	m.hits = m.hits[:0]
	m.gfx.unplace()
	if w <= 0 || h <= 0 {
		m.content = ""
		return
	}
	r := &f.rects
	// Lines go top to bottom from r.top(), the hits' y with them.
	lines := make([]string, 0, h+barRows)
	if r.body.Dy() > 0 {
		lines = m.appendTabs(lines, w)
		lines = m.appendBody(lines, w, f)
	}
	if r.banner.Dy() > 0 {
		banner, hasKey := m.banner(w)
		if hasKey {
			m.mark(hit{y: len(lines), x1: w, kind: hitBanner})
		}
		lines = append(lines, banner)
	}
	lines = m.appendBar(lines, w, f)
	if cut := -r.top(); cut > 0 {
		// Too short for the bar: its last lines.
		m.keepHits(cut, len(lines))
		m.gfx.clip(cut)
		lines = lines[cut:]
	}
	m.gfx.endFrame()
	m.content = m.gfx.join(lines)
}

// layoutIn is what the screen's layout reads of m.
func (m *model) layoutIn() layoutIn {
	return layoutIn{
		w: m.width, h: m.height,
		hasBanner: m.link.Health != remote.Connected,
		tabsRows:  tabsRows,
		barRows:   barRows,
	}
}

// appendTabs adds the app's name, the tabs and the account, over a rule
// that marks the shown tab.
func (m *model) appendTabs(lines []string, w int) []string {
	y := len(lines)
	var top, rule strings.Builder
	at := 0
	if w >= 60 {
		name := " " + appName + "  "
		styleAccentB.write(&top, name)
		at = width(name)
		rule.WriteString(strings.Repeat("─", at))
	}
	labels := [numTabs]string{tabQueue: locales.Queue(), tabSearch: locales.NavSearch(), tabLibrary: locales.NavLibrary()}
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
		m.mark(hit{y: y + tabsLine, x0: at, x1: at + cw, kind: hitTab, tab: tab(i)})
		at += cw + 1
	}
	account := ""
	switch {
	case m.session.IsSignedIn():
		account = m.session.DisplayName()
	case m.isSignedOut():
		account = locales.CliSignedOut()
	}
	var tabs [tabsRows]string
	tabs[tabsLine] = spread("", top.String(), styleMuted.Render(fit(account, max(w-at-2, 0))+" "), w)
	if at < w {
		rule.WriteString(strings.Repeat("─", w-at))
	}
	tabs[tabsRule] = styleMuted.Render(fit(rule.String(), w))
	return append(lines, tabs[:]...)
}

// appendBody adds f's page, or the help, in exactly the body's lines.
func (m *model) appendBody(lines []string, w int, f *frame) []string {
	start, h := len(lines), f.rects.body.Dy()
	if m.isHelp {
		lines = m.appendHelp(lines, w, h)
	} else {
		p := &f.page
		if m.tab == tabSearch && m.opened[tabSearch].kind == 0 && !m.isTyping {
			m.mark(hit{y: start, x1: w, kind: hitInput})
		}
		cover, _ := m.gfx.show(slotHeader, start, f.art[slotHeader])
		lines = m.appendPageHeader(lines, w, &p.header, &cover)
		switch {
		case p.title != "":
			lines = m.appendMessage(lines, w, p)
		case p.list != nil:
			lines = m.appendList(lines, w, f.content.Dy(), p)
		}
	}
	if len(lines)-start > h {
		m.keepHits(0, start+h)
		lines = lines[:start+h]
	}
	for len(lines)-start < h {
		lines = append(lines, "")
	}
	return lines
}

// appendPageHeader adds a page's header, each of its first lines after
// its cover's: the search's input over its status, or a collection's
// title and subtitle over a blank line.
func (m *model) appendPageHeader(lines []string, w int, hd *header, cover *[coverRows]string) []string {
	var text [3]string
	switch hd.kind {
	case headerSearch:
		text[0] = m.searchInput()
		if hd.status != "" {
			text[1] = styleMuted.Render(" " + hd.status)
		}
	case headerCollection:
		text[0] = " " + styleBold.Render(hd.title)
		text[1] = " " + styleMuted.Render(hd.subtitle)
	}
	for i := range hd.rows() {
		line := text[i]
		if i < coverRows {
			line = cover[i] + line
		}
		lines = append(lines, fit(line, w))
	}
	return lines
}

// searchInput is the search page's input line.
func (m *model) searchInput() string {
	prompt := styleKey.Render(" / ")
	switch {
	case m.isTyping:
		return prompt + m.query + styleAccent.Render("█")
	case m.query == "":
		return prompt + styleMuted.Render(locales.SearchPlaceholder())
	}
	return prompt + m.query
}

// appendMessage adds a page's message: its title, detail and hint.
func (m *model) appendMessage(lines []string, w int, p *page) []string {
	lines = append(lines, "", " "+styleBold.Render(fit(p.title, w-2)))
	if p.detail != "" {
		for line := range strings.Lines(lipgloss.Wrap(p.detail, max(w-2, 1), "")) {
			lines = append(lines, " "+styleMuted.Render(strings.TrimRight(line, "\n")))
		}
	}
	if p.hint != "" {
		m.mark(hit{y: len(lines) + 1, x1: w, kind: hitHint})
		lines = append(lines, "", " "+fit(p.hint, w-2))
	}
	return lines
}

// appendList adds the rows of p's list that show in rows lines, which
// prepare fitted it to.
func (m *model) appendList(lines []string, w, rows int, p *page) []string {
	l := p.list
	end := min(l.offset+max(rows, 0), p.n)
	for i := l.offset; i < end; i++ {
		it := p.at(i)
		if it.kind != itemHeading {
			m.mark(hit{y: len(lines), x1: w, kind: hitRow, i: i, list: l})
		}
		lines = append(lines, m.row(it, w, i == l.cursor))
	}
	return lines
}

// appendHelp adds the keys and what they do in h lines, in as many
// columns as that takes, each as wide as its text.
func (m *model) appendHelp(lines []string, w, h int) []string {
	title := fit(locales.TuiHelpTitle(), w-2)
	mouse := fit(locales.TuiHelpMouse(), w-width(title)-4)
	if mouse != "" {
		mouse = "  " + styleMuted.Render(mouse)
	}
	lines = append(lines, "", " "+styleBold.Render(title)+mouse)
	keys := helpKeys
	rows := max(h-2, 1)
	cols := (len(keys) + rows - 1) / rows
	rows = (len(keys) + cols - 1) / cols
	keyWidth := 0
	for _, k := range keys {
		keyWidth = max(keyWidth, width(k.keys))
	}
	doesWidth := make([]int, cols)
	for i, k := range keys {
		doesWidth[i/rows] = max(doesWidth[i/rows], width(k.does()))
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
			does := fit(keys[i].does(), max(left, 0))
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

// helpKey is a line of the help: keys and what they do.
type helpKey struct {
	keys string
	does func() string
}

var helpKeys = []helpKey{
	{"space", locales.TuiHelpToggle},
	{"n / p", locales.TuiHelpNext},
	{"← / →", locales.TuiHelpSeek},
	{"+ / -", locales.TuiHelpVolume},
	{"s", locales.TuiHelpShuffle},
	{"r", locales.TuiHelpRepeat},
	{"↑ / ↓", locales.TuiHelpSelect},
	{"enter", locales.TuiHelpPlay},
	{"a / ⌫", locales.TuiHelpAdd},
	{"l", locales.TuiHelpLike},
	{"/", locales.TuiHelpSearch},
	{"tab", locales.TuiHelpTab},
	{"esc", locales.TuiHelpBack},
	{"R", locales.TuiHelpRetry},
	{"?", locales.TuiHelpHelp},
	{"q", locales.TuiHelpQuit},
}

// keyHint shows a key and what it does.
func keyHint(key, does string) string { return styleKey.Render(key) + " " + does }

// banner is the line that says how the daemon is when it is not
// connected, with the key that helps, if any (hasKey); "" when connected.
func (m *model) banner(w int) (line string, hasKey bool) {
	var text string
	l := m.link
	switch l.Health {
	case remote.Connected:
		return "", false
	case remote.Connecting:
		text = locales.TuiConnecting()
	case remote.Reconnecting:
		text = locales.TuiReconnecting()
	case remote.Unresponsive:
		text, hasKey = locales.TuiUnresponsive()+"  "+keyHint("R", locales.TuiRestart()), true
	case remote.Unavailable:
		text, hasKey = locales.TuiUnavailable()+"  "+keyHint("R", locales.Retry())+"  "+locales.TuiLog(m.daemonLog), true
	case remote.DaemonOutdated:
		text = locales.TuiDaemonOutdated(l.Daemon.Hello.Version, l.Daemon.Version)
		if !l.Daemon.IsCompatible {
			text, hasKey = text+"  "+keyHint("R", locales.Retry()), true
		}
	case remote.FrontendOutdated:
		text = locales.TuiFrontendOutdated(l.Daemon.Hello.Version, l.Daemon.Version)
	}
	return fit(styleWarn.Render(" ! ")+text, w), hasKey
}

// appendBar adds the player bar: the current song, its progress, and
// the status line (shuffle, repeat, volume; a notice, or the help key).
func (m *model) appendBar(lines []string, w int, f *frame) []string {
	st := m.mirror.State()
	y := len(lines)
	var bar [barRows]string
	bar[barRule] = styleMuted.Render(strings.Repeat("─", w))
	song, ok := m.mirror.Song(st.Current)
	if !ok || st.Current == "" {
		bar[barSong] = " " + styleMuted.Render(fit(locales.TuiNothingPlaying(), w-2))
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
		// The cover spans the song and its progress.
		cover, x := m.gfx.show(slotBar, y+barSong, f.art[slotBar])
		bar[barSong] = spread(cover[0], title, heart, w-x)
		bar[barProgress] = m.progress(st, cover[1], x, y+barProgress, w)
	}
	right := keyHint("?", locales.TuiHelp()) + " "
	if m.notice != "" {
		right = styleAccent.Render(fit(m.notice, max(w*2/3, 1))) + " "
	} else {
		m.mark(hit{y: y + barStatus, x0: w - width(right), x1: w - 1, kind: hitHelp})
	}
	bar[barStatus] = spread("", " "+styleMuted.Render(f.status), right, w)
	return append(lines, bar[:]...)
}

// progress is the current song's time on line y of w cells, after cover,
// x cells wide, as a bar between its elapsed and whole length, a space
// before the end.
func (m *model) progress(st api.State, cover string, x, y, w int) string {
	pos := m.position(st)
	elapsed := Clock(pos)
	if st.Duration <= 0 {
		return cover + " " + elapsed
	}
	total := Clock(st.Duration)
	start := x + 1 + width(elapsed) + 1
	bar := w - start - 1 - width(total) - 1
	if bar < 4 {
		return cover + " " + elapsed + " / " + total
	}
	m.mark(hit{y: y, x0: start, x1: start + bar, kind: hitProgress})
	done := min(int(pos/st.Duration*float64(bar)), bar)
	return cover + " " + elapsed + " " + styleAccent.Render(strings.Repeat("━", done)) +
		styleMuted.Render(strings.Repeat("─", bar-done)) + " " + total
}

// status says what is on of shuffle and repeat, and the volume.
func (m *model) status(st api.State) string {
	var parts [3]string
	n := 0
	if st.Shuffled {
		parts[n] = locales.CliShuffleOn()
		n++
	}
	switch st.Repeat {
	case api.RepeatAll:
		parts[n] = locales.RepeatAll()
		n++
	case api.RepeatOne:
		parts[n] = locales.RepeatOne()
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
