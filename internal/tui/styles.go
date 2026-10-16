package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// The styles use the terminal's own sixteen colors, so they follow its
// theme, light or dark; Bubble Tea drops what a terminal cannot show.
var (
	styleBold     = paintOf(lipgloss.NewStyle().Bold(true))
	styleMuted    = paintOf(lipgloss.NewStyle().Foreground(lipgloss.BrightBlack))
	styleAccent   = paintOf(lipgloss.NewStyle().Foreground(lipgloss.BrightRed))
	styleAccentB  = paintOf(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.BrightRed))
	styleSelected = paintOf(lipgloss.NewStyle().Reverse(true))
	styleWarn     = paintOf(lipgloss.NewStyle().Foreground(lipgloss.Yellow).Bold(true))
	styleKey      = paintOf(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Cyan))
	styleHeading  = paintOf(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.BrightBlack))
)

// paint is a Lip Gloss style as the escape sequences around its text,
// rendered once: a screen paints hundreds of cells, and rendering a
// style parses it anew each time. Text painted is one line, without
// styles of its own.
type paint struct{ pre, post string }

// paintOf renders s around a placeholder, and keeps what comes before and
// after it.
func paintOf(s lipgloss.Style) paint {
	const mark = "\uE000"
	pre, post, _ := strings.Cut(s.Render(mark), mark)
	return paint{pre: pre, post: post}
}

// Render returns text in the paint's style.
func (p paint) Render(text string) string {
	if text == "" {
		return ""
	}
	return p.pre + text + p.post
}

// write writes text in the paint's style to b.
func (p paint) write(b *strings.Builder, text string) {
	if text == "" {
		return
	}
	b.WriteString(p.pre)
	b.WriteString(text)
	b.WriteString(p.post)
}
