package main

import (
	"fmt"
	"io"
	"strings"
	"yt-music-player/locales"
)

// helpLine is a line of help: commands and their arguments, never
// translated, and what they do.
type helpLine struct {
	commands string
	about    func() string
}

// helpLines are help's lines: the TUI's, then the commands', a command
// without its own joining the line before.
func helpLines() []helpLine {
	lines := []helpLine{{"", locales.CliHelpTUI}}
	for _, c := range commands() {
		if c.about == nil {
			lines[len(lines)-1].commands += " | " + c.name
			continue
		}
		lines = append(lines, helpLine{c.synopsisOf(), c.about})
	}
	return lines
}

// help writes the list of commands.
func (e *env) help() {
	lines := helpLines()
	width := 0
	for _, l := range lines {
		width = max(width, len("ytm ")+len(l.commands))
	}
	var b strings.Builder
	b.WriteString(locales.CliHelpTitle())
	b.WriteString("\n\n")
	for _, l := range lines {
		line := strings.TrimSpace("ytm " + l.commands)
		fmt.Fprintf(&b, "  %-*s  %s\n", width, line, l.about())
	}
	_, _ = io.WriteString(e.stdout, b.String())
}

// usage tells how command is used and returns exitUsage.
func (e *env) usage(command string) int {
	synopsis := command
	if c, ok := findCommand(command); ok {
		synopsis = c.synopsisOf()
	}
	writeLine(e.stderr, locales.CliUsage("ytm "+synopsis))
	return exitUsage
}
