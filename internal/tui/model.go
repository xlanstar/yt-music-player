package tui

import (
	"math"
	"strings"
	"time"
	"yt-music-player/internal/api"
	"yt-music-player/internal/catalog"
	"yt-music-player/internal/fetchstate"
	"yt-music-player/internal/loader"
	"yt-music-player/internal/notice"
	"yt-music-player/internal/noticetext"
	"yt-music-player/internal/remote"
	"yt-music-player/internal/session"
	"yt-music-player/locales"

	tea "charm.land/bubbletea/v2"
)

// The model's times and steps.
const (
	// seekStep is how far ← and → seek.
	seekStep = 5
	// volumeStep is how much + and - change the volume, of 1.
	volumeStep = 0.05
	// noticeFor is how long a notice shows in the status line.
	noticeFor = 5 * time.Second
)

// tab is one of the TUI's pages.
type tab uint8

const (
	tabQueue tab = iota
	tabSearch
	numTabs
)

// playback is the daemon's playback as the TUI reads it: remote.Mirror.
type playback interface {
	State() api.State
	Position(now time.Time) float64
	Queue() []string
	Song(id string) (catalog.Song, bool)
}

var _ playback = (*remote.Mirror)(nil)

// supervisor is what the TUI asks of remote.Supervisor.
type supervisor interface {
	Retry()
	Restart()
	Foreground()
}

var _ supervisor = (*remote.Supervisor)(nil)

// deps are what the model works with; all but ctl's and sup's methods
// belong to the UI thread, Update.
type deps struct {
	ctl     remote.Controller
	mirror  playback
	sup     supervisor
	session *session.Session
	loader  *loader.Loader
	cat     *catalog.Catalog
	states  *fetchstate.Registry
	posts   *poster
	// now is the clock the progress reads.
	now func() time.Time
	// daemonLog is the path of ytmd's log, shown when it fails.
	daemonLog string
}

// model is the TUI's state. Bubble Tea calls Update and View on one
// goroutine, the UI thread: the session, the loader, the catalog and the
// Mirror are touched there only. Update renders the screen once per
// message that changed anything; View returns it.
type model struct {
	deps
	words words

	width, height int
	tab           tab
	// isBlurred holds once the terminal said it lost focus; one that
	// reports no focus counts as focused.
	isBlurred bool
	link      remote.Link

	queue, search listState
	// queueFollows keeps the queue's cursor on the current song until
	// the user moves it.
	queueFollows bool

	query    string
	isTyping bool

	// searched is what the loader was last told to search.
	searched string

	// notice is the status line's message, until a clearNoticeMsg of
	// noticeSeq; isNoticeNew asks settle to schedule that.
	notice      string
	noticeSeq   uint64
	isNoticeNew bool
	isTicking   bool

	memo    memo
	content string
}

// tickMsg moves the progress on by a second.
type tickMsg struct{}

// clearNoticeMsg ends the notice seq.
type clearNoticeMsg struct{ seq uint64 }

func newModel(d deps) *model {
	return &model{deps: d, words: newWords(), queueFollows: true}
}

// Init implements tea.Model.
func (m *model) Init() tea.Cmd { return nil }

// View implements tea.Model: the screen Update rendered last.
func (m *model) View() tea.View {
	v := tea.NewView(m.content)
	v.AltScreen = true
	v.ReportFocus = true
	return v
}

// Update implements tea.Model.
func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case postedMsg:
		m.posts.run()
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.FocusMsg:
		m.isBlurred = false
		m.sup.Foreground()
	case tea.BlurMsg:
		m.isBlurred = true
	case tickMsg:
		m.isTicking = false
	case clearNoticeMsg:
		if msg.seq != m.noticeSeq {
			return m, nil
		}
		m.notice = ""
	case tea.PasteMsg:
		if !m.isTyping {
			return m, nil
		}
		m.typeText(msg.Content)
	case tea.KeyPressMsg:
		var ok bool
		if ok, cmd = m.key(msg); !ok {
			return m, nil
		}
	default:
		return m, nil
	}
	return m, m.settle(cmd)
}

// settle follows what a message changed: it tells the session's notices,
// tells the loader what shows, schedules the progress and the notice's
// end, and renders.
func (m *model) settle(cmd tea.Cmd) tea.Cmd {
	for _, n := range m.session.TakeNotices() {
		m.tell(n)
	}
	m.syncLoad()
	m.render()
	cmds := []tea.Cmd{cmd}
	if m.isNoticeNew {
		m.isNoticeNew = false
		seq := m.noticeSeq
		cmds = append(cmds, tea.Tick(noticeFor, func(time.Time) tea.Msg { return clearNoticeMsg{seq} }))
	}
	if !m.isTicking && m.wantsTicks() {
		m.isTicking = true
		cmds = append(cmds, tea.Tick(m.untilNextSecond(), func(time.Time) tea.Msg { return tickMsg{} }))
	}
	return tea.Batch(cmds...)
}

// wantsTicks reports the progress moving where someone sees it.
func (m *model) wantsTicks() bool {
	st := m.mirror.State()
	return st.Playing && !st.Waiting && !m.isBlurred && st.Current != ""
}

// untilNextSecond is how long until the shown position's next second.
func (m *model) untilNextSecond() time.Duration {
	pos := m.mirror.Position(m.now())
	_, frac := math.Modf(pos)
	// A little past it, so the second has turned when the tick comes.
	return time.Duration((1-frac)*float64(time.Second)) + 10*time.Millisecond
}

// syncLoad tells the loader the search page's query.
func (m *model) syncLoad() {
	query := ""
	if m.tab == tabSearch {
		query = strings.TrimSpace(m.query)
	}
	if query != m.searched {
		m.searched = query
		m.loader.Search(query)
	}
}

// tell shows n in the status line: every notice, the session's and
// remote's, goes through here.
func (m *model) tell(n notice.Notice) {
	title, detail := noticetext.Render(n)
	if detail != "" {
		title = locales.Announcement(title, detail)
	}
	m.say(title)
}

// say shows text in the status line for a while.
func (m *model) say(text string) {
	m.notice = text
	m.noticeSeq++
	m.isNoticeNew = true
}

// setLink keeps the connection's health; a new connection fetches again
// what failed meanwhile.
func (m *model) setLink(l remote.Link) {
	isNew := l.Connection != m.link.Connection
	m.link = l
	if isNew {
		m.loader.RetryFailed()
		m.session.RetryFailed()
	}
}

// key handles a key press; ok is false for keys that do nothing, which
// leave the screen as it is.
func (m *model) key(k tea.KeyPressMsg) (ok bool, cmd tea.Cmd) {
	if k.Code == 0 && k.Text == "" {
		// Windows sends a lone Ctrl as NUL.
		return false, nil
	}
	s := k.String()
	if s == "ctrl+c" {
		return true, tea.Quit
	}
	if m.isTyping {
		return m.typeKey(k, s), nil
	}
	if ok, cmd := m.playbackKey(s); ok {
		return true, cmd
	}
	return m.browseKey(s)
}

// playbackKey handles the keys that work on every page.
func (m *model) playbackKey(s string) (ok bool, cmd tea.Cmd) {
	switch s {
	case "q":
		return true, tea.Quit
	case "space":
		m.ctl.Toggle()
	case "n":
		m.ctl.Next()
	case "p":
		m.ctl.Prev()
	case "left":
		m.seekBy(-seekStep)
	case "right":
		m.seekBy(seekStep)
	case "+", "=":
		m.volumeBy(volumeStep)
	case "-", "_":
		m.volumeBy(-volumeStep)
	case "s":
		m.ctl.ToggleShuffle()
	case "r":
		m.ctl.CycleRepeat()
	case "R":
		return m.retryDaemon(), nil
	default:
		return false, nil
	}
	return true, nil
}

// browseKey handles the keys of the pages.
func (m *model) browseKey(s string) (ok bool, cmd tea.Cmd) {
	switch s {
	case "tab":
		m.tab = (m.tab + 1) % numTabs
	case "shift+tab":
		m.tab = (m.tab + numTabs - 1) % numTabs
	case "/":
		m.tab = tabSearch
		m.isTyping = true
	case "up":
		m.moveBy(-1)
	case "down":
		m.moveBy(1)
	case "pgup":
		m.moveBy(-max(m.bodyHeight()-1, 1))
	case "pgdown":
		m.moveBy(max(m.bodyHeight()-1, 1))
	case "home":
		m.moveBy(math.MinInt / 2)
	case "end":
		m.moveBy(math.MaxInt / 2)
	case "enter":
		return m.activate(), nil
	default:
		return false, nil
	}
	return true, nil
}

// typeKey edits the search query, which takes every key while typing.
func (m *model) typeKey(k tea.KeyPressMsg, s string) bool {
	switch s {
	case "esc", "enter":
		m.isTyping = false
	case "backspace":
		if m.query == "" {
			return false
		}
		r := []rune(m.query)
		m.query = string(r[:len(r)-1])
	case "ctrl+u":
		m.query = ""
	default:
		if k.Text == "" {
			return false
		}
		m.typeText(k.Text)
		return true
	}
	m.search = listState{}
	return true
}

// typeText adds text, typed or pasted, to the query, on one line.
func (m *model) typeText(text string) {
	text = strings.Map(func(r rune) rune {
		if r < ' ' || r == 0x7f {
			return ' '
		}
		return r
	}, text)
	m.query += text
	m.search = listState{}
}

func (m *model) seekBy(seconds float64) {
	st := m.mirror.State()
	if st.Current == "" {
		return
	}
	pos := m.mirror.Position(m.now()) + seconds
	if st.Duration > 0 {
		pos = min(pos, st.Duration)
	}
	m.ctl.Seek(max(pos, 0))
}

func (m *model) volumeBy(step float64) {
	v := m.mirror.State().Volume + step
	// Whole steps, so that 0 and 1 are reached.
	v = math.Round(v/volumeStep) * volumeStep
	m.ctl.SetVolume(min(max(v, 0), 1))
}

// retryDaemon connects again, or restarts a daemon that does not answer.
func (m *model) retryDaemon() bool {
	switch m.link.Health {
	case remote.Connected:
		return false
	case remote.Unresponsive:
		m.sup.Restart()
	default:
		m.sup.Retry()
	}
	return true
}

// isSignedOut reports that the daemon says no one is signed in.
func (m *model) isSignedOut() bool {
	return m.states.State(session.AccountRes).State == fetchstate.SignedOut
}

// moveBy moves the shown list's cursor by delta items, over headings.
func (m *model) moveBy(delta int) {
	p := m.page()
	if p.list == nil || p.n == 0 {
		return
	}
	if p.list == &m.queue {
		m.queueFollows = false
	}
	p.list.moveBy(delta, p.n, p.selectable)
}

// activate plays the selected song, or retries what failed.
func (m *model) activate() bool {
	p := m.page()
	if p.retry != nil {
		p.retry()
		return true
	}
	it, ok := p.selected()
	if !ok {
		return false
	}
	switch it.kind {
	case itemSong:
		m.ctl.Play([]string{it.song.ID}, 0, []catalog.Song{it.song})
		if p.list == &m.queue {
			m.queueFollows = true
		}
	default:
		return false
	}
	return true
}
