package tui

import (
	"fmt"
	"strings"
	"testing"
	"yt-music-player/internal/remote"
)

// TestStyles holds what the stripped goldens cannot: the selected row
// reversed across the whole line, the current song's row and the bar
// playing it, the warning banner. Lip Gloss renders the same escapes on
// every terminal, Bubble Tea downsamples them after; covers are off.
func TestStyles(t *testing.T) {
	inLanguage(t, "en")
	var got strings.Builder
	for _, tc := range []struct {
		name    string
		prepare func(h *harness)
	}{
		{"selected", func(h *harness) { h.press("down", "down") }},
		{"playing", func(h *harness) {
			h.mirror.state.Playing = true
			h.press("down")
		}},
		{"banner", func(h *harness) { h.m.setLink(remote.Link{Health: remote.Unresponsive, Connection: 1}) }},
	} {
		h := placeholderHarness(t)
		tc.prepare(h)
		h.resize(64, 10)
		fmt.Fprintf(&got, "=== %s\n%s", tc.name, h.styled())
	}
	goldenText(t, "styles", got.String())
}

// styled is the screen with its styles, ESC shown as \e, its lines'
// trailing spaces cut.
func (h *harness) styled() string {
	var b strings.Builder
	for line := range strings.SplitSeq(h.m.View().Content, "\n") {
		b.WriteString(strings.TrimRight(strings.ReplaceAll(line, "\x1b", `\e`), " "))
		b.WriteByte('\n')
	}
	return b.String()
}
