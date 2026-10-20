package tui

import (
	"image"
	"math"
	"strings"
	"yt-music-player/internal/api"
	"yt-music-player/internal/remote"
	"yt-music-player/locales"
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

// barCoverRows is the bar's cover's height: the song and its progress.
const barCoverRows = barProgress - barSong + 1

// headerCoverRows is a page header's cover's height: a collection's title
// and subtitle.
const headerCoverRows = 2

// appName is the app's name, untranslated.
const appName = "YT Music Player"

// render prepares the screen's frame, draws it into the scene, readies
// the covers drawn and composes the screen.
func (m *model) render() {
	f := m.prepare()
	m.draw(&f)
	cf := m.covers.sync(m.scene.covers, m.probe.caps, m.width, m.height)
	m.content = m.scene.compose(&cf)
}

// draw draws f into m.scene, with the hits a click acts on, changing
// nothing else: the tabs, the page, the daemon's health unless connected,
// the player bar. Lists draw only their visible rows.
func (m *model) draw(f *frame) {
	w, h := m.width, m.height
	screen := m.scene.reset(w, h, m.cells)
	if w <= 0 || h <= 0 {
		return
	}
	r := &f.rects
	if r.body.Dy() > 0 {
		// drawLines places every hit marked so far: the body's, before the
		// tabs draw theirs. Transitional, as drawLines is.
		lines := m.appendBody(make([]string, 0, r.body.Dy()), w, f)
		m.drawLines(screen, f, lines)
		m.drawTabs(screen.sub(r.tabs))
	}
	if r.banner.Dy() > 0 {
		m.drawBanner(screen.sub(r.banner))
	}
	m.drawBar(screen.sub(r.bar), f)
}

// drawLines draws lines, the body's: the header's cover's cells, each
// line after the cover it is beside, and the hits marked on the lines.
// Transitional, until the pages draw into regions.
func (m *model) drawLines(screen region, f *frame, lines []string) {
	body := f.rects.body
	g := screen.sub(body)
	// The header claims the first of the covers' places, as it did.
	hc := f.covers[slotHeader]
	screen.cover(hc.rect, hc.slot, hc.art)
	for i, line := range lines {
		x := 0
		if y := body.Min.Y + i; hc.art != "" && y >= hc.rect.Min.Y && y < hc.rect.Max.Y {
			x = hc.rect.Max.X
		}
		g.line(x, i, line)
	}
	marked := m.scene.hits
	m.scene.hits = marked[:0]
	for _, h := range marked {
		g.hit(h.rect, h)
	}
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

// drawTabs draws in g the app's name, the tabs and the account, over a
// rule that marks the shown tab.
func (m *model) drawTabs(g region) {
	const y = tabsLine
	w, _ := g.size()
	c := &m.cells
	var left [numTabs + 2]piece
	n, gap := 0, 0
	if w >= 60 {
		left[n] = piece{text: appName, paint: styleAccentB, gap: padX}
		n, gap = n+1, 2
	}
	first := n
	labels := [numTabs]string{tabQueue: locales.Queue(), tabSearch: locales.NavSearch(), tabLibrary: locales.NavLibrary()}
	for i, label := range labels {
		style := paint{}
		if tab(i) == m.tab {
			style = styleAccentB
		}
		left[n] = piece{text: " " + label + " ", paint: style, gap: gap}
		n, gap = n+1, 1
	}
	left[n] = piece{gap: 1}
	n++
	at := c.measure(left[:n])
	account := ""
	switch {
	case m.session.IsSignedIn():
		account = m.session.DisplayName()
	case m.isSignedOut():
		account = locales.CliSignedOut()
	}
	right := [...]piece{{text: c.fit(account, max(w-at-2, 0)), paint: styleMuted}, {gap: padX}}
	rw := c.measure(right[:])
	rx := max(w-rw, 0)
	g.putRun(rx, y, w-rx, right[:], rw)
	if rx > 0 {
		g.putRun(0, y, rx-1, left[:n], at)
	}
	// The rule runs thin, thick under the shown tab.
	var rule strings.Builder
	x, thin := 0, 0
	for i := range left[:n] {
		p := &left[i]
		x += p.gap
		if i < first || i >= first+int(numTabs) {
			thin += p.gap + p.w
			x += p.w
			continue
		}
		t := tab(i - first)
		g.hit(image.Rect(x, y, x+p.w, y+1), hit{kind: hitTab, tab: t})
		mark := ruleThin
		if t == m.tab {
			mark = ruleThick
		}
		c.writeRepeat(&rule, ruleThin, thin+p.gap)
		c.writeRepeat(&rule, mark, p.w)
		thin = 0
		x += p.w
	}
	c.writeRepeat(&rule, ruleThin, thin)
	c.writeRepeat(&rule, ruleThin, w-at)
	g.fit(0, tabsRule, rule.String(), w, styleMuted)
}

// appendBody adds f's page, or the help, in exactly the body's lines.
func (m *model) appendBody(lines []string, w int, f *frame) []string {
	start, h := len(lines), f.rects.body.Dy()
	if m.isHelp {
		lines = m.appendHelp(lines, w, h)
	} else {
		p := &f.page
		if m.tab == tabSearch && m.opened[tabSearch].kind == 0 && !m.isTyping {
			m.mark(start, 0, w, hit{kind: hitInput})
		}
		lines = m.appendPageHeader(lines, w, &p.header, f.covers[slotHeader].rect)
		switch {
		case p.title != "":
			lines = m.appendMessage(lines, w, p)
		case p.list != nil:
			lines = m.appendList(lines, w, f.content.Dy(), p)
		}
	}
	if len(lines)-start > h {
		m.dropHits(start + h)
		lines = lines[:start+h]
	}
	for len(lines)-start < h {
		lines = append(lines, "")
	}
	return lines
}

// appendPageHeader adds a page's header, its lines beside cover after it:
// the search's input over its status, or a collection's title and
// subtitle over a blank line.
func (m *model) appendPageHeader(lines []string, w int, hd *header, cover image.Rectangle) []string {
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
		left := w
		if i < cover.Dy() {
			left -= cover.Max.X
		}
		lines = append(lines, m.cells.fit(text[i], left))
	}
	return lines
}

// searchInput is the search page's input line.
func (m *model) searchInput() string {
	prompt := styleKey.Render(" / ")
	switch {
	case m.isTyping:
		return prompt + m.query + styleAccent.Render(iconCaret)
	case m.query == "":
		return prompt + styleMuted.Render(locales.SearchPlaceholder())
	}
	return prompt + m.query
}

// appendMessage adds a page's message: its title, detail and hint.
func (m *model) appendMessage(lines []string, w int, p *page) []string {
	c := &m.cells
	lines = append(lines, "", " "+styleBold.Render(c.fit(p.title, w-2)))
	if p.detail != "" {
		for line := range strings.Lines(c.wrap(p.detail, max(w-2, 1))) {
			lines = append(lines, " "+styleMuted.Render(strings.TrimRight(line, "\n")))
		}
	}
	if p.hint != "" {
		m.mark(len(lines)+1, 0, w, hit{kind: hitHint})
		lines = append(lines, "", " "+c.fit(p.hint, w-2))
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
			m.mark(len(lines), 0, w, hit{kind: hitRow, i: i, list: l})
		}
		lines = append(lines, m.row(it, w, i == l.cursor))
	}
	return lines
}

// appendHelp adds the keys and what they do in h lines, in as many
// columns as that takes, each as wide as its text.
func (m *model) appendHelp(lines []string, w, h int) []string {
	c := &m.cells
	title := c.fit(locales.TuiHelpTitle(), w-2)
	mouse := c.fit(locales.TuiHelpMouse(), w-c.width(title)-4)
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
		keyWidth = max(keyWidth, c.width(k.keys))
	}
	doesWidth := make([]int, cols)
	for i, k := range keys {
		doesWidth[i/rows] = max(doesWidth[i/rows], c.width(k.does()))
	}
	for r := range rows {
		var b strings.Builder
		left := w
		for col := range cols {
			i := col*rows + r
			if i >= len(keys) || left <= 0 {
				break
			}
			key := c.fit("   "+c.pad(keys[i].keys, keyWidth)+"  ", left)
			left -= c.width(key)
			does := c.fit(keys[i].does(), max(left, 0))
			if col < cols-1 {
				does = c.pad(does, min(doesWidth[col], max(left, 0)))
			}
			left -= c.width(does)
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

// drawBanner draws in g how the daemon is when it is not connected,
// with the key that helps, if any, which a click presses.
func (m *model) drawBanner(g region) {
	w, _ := g.size()
	text, hasKey := m.bannerText()
	if hasKey {
		g.hit(image.Rect(0, 0, w, 1), hit{kind: hitBanner})
	}
	g.run(padX, 0, w-padX, []piece{{text: iconWarn, paint: styleWarn}, {text: text, gap: 1}})
}

// bannerText says how the daemon is when it is not connected, with the
// key that helps, if any (hasKey).
func (m *model) bannerText() (text string, hasKey bool) {
	l := m.link
	switch l.Health {
	case remote.Connected:
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
	return text, hasKey
}

// drawBar draws the player bar in g: a rule, the current song beside its
// cover, its progress, and the status line (shuffle, repeat, volume; a
// notice, or the help key).
func (m *model) drawBar(g region, f *frame) {
	st := m.mirror.State()
	w, _ := g.size()
	c := &m.cells
	g.putText(0, barRule, c.repeat(ruleThin, w), w, styleMuted)
	song, ok := m.mirror.Song(st.Current)
	if !ok || st.Current == "" {
		g.fit(padX, barSong, locales.TuiNothingPlaying(), w-2*padX, styleMuted)
	} else {
		g.cover(f.bar.cover, slotBar, song.Art)
		x, end := f.bar.textX, w-padX
		if m.session.Liked(song.ID) {
			hw := c.heart - 1 // a song's heart is a space and ♥
			g.putText(end-hw, barSong, iconLiked, hw, styleAccent)
			end -= hw + 1
		}
		icon := iconPaused
		switch {
		case st.Waiting:
			icon = iconWaiting
		case st.Playing:
			icon = iconPlaying
		}
		artist := ""
		if song.Artist != "" {
			artist = separator + song.Artist
		}
		g.run(x, barSong, end-x, []piece{
			{text: icon, paint: styleAccent},
			{text: song.Title, paint: styleBold, gap: 1},
			{text: artist, paint: styleMuted},
		})
		m.drawProgress(g, st, x, w-padX)
	}
	m.drawStatus(g, f.status)
}

// drawProgress draws the current song's time on the bar's progress line,
// from cell x to end: a bar between its elapsed and whole length.
func (m *model) drawProgress(g region, st api.State, x, end int) {
	const y = barProgress
	pos, c := m.position(st), &m.cells
	x += g.text(x, y, Clock(pos), paint{}) + 1
	if st.Duration <= 0 {
		return
	}
	total := Clock(st.Duration)
	tw := c.width(total)
	n := end - tw - 1 - x
	if n < 4 {
		g.text(x, y, "/", paint{})
		g.putText(x+2, y, total, tw, paint{})
		return
	}
	g.hit(image.Rect(x, y, x+n, y+1), hit{kind: hitProgress})
	done := min(int(pos/st.Duration*float64(n)), n)
	g.putText(x, y, c.repeat(ruleThick, done), done, styleAccent)
	g.putText(x+done, y, c.repeat(ruleThin, n-done), n-done, styleMuted)
	g.putText(end-tw, y, total, tw, paint{})
}

// drawStatus draws the bar's status line: status, then right of it a
// notice, or the help key.
func (m *model) drawStatus(g region, status string) {
	const y = barStatus
	w, _ := g.size()
	c := &m.cells
	right := []piece{{text: "?", paint: styleKey}, {text: locales.TuiHelp(), gap: 1}, {gap: padX}}
	if m.notice != "" {
		right = []piece{{text: c.fit(m.notice, max(w*2/3, 1)), paint: styleAccent}, {gap: padX}}
	}
	rw := c.measure(right)
	// Too narrow, the right is cut as one with its padding.
	rx := max(w-rw, 0)
	g.putRun(rx, y, w-rx, right, rw)
	if m.notice == "" {
		g.hit(image.Rect(w-rw, y, w-padX, y+1), hit{kind: hitHelp})
	}
	if rx > 0 {
		g.run(0, y, rx-1, []piece{{text: status, paint: styleMuted, gap: padX}})
	}
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
	return strings.Join(parts[:n], separator)
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
