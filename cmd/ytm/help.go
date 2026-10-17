package main

import (
	"fmt"
	"io"
	"strings"
	"yt-music-player/locales"
)

// usage is a command's synopsis: names and arguments, never translated.
type usage struct {
	command string
	about   func() string
}

// usages are the commands help lists, in order.
var usages = []usage{
	{"", locales.CliHelpTUI},
	{synopses["status"], locales.CliHelpStatus},
	{"play | pause | toggle", locales.CliHelpPlay},
	{"next | prev", locales.CliHelpNext},
	{synopses["seek"], locales.CliHelpSeek},
	{synopses["volume"], locales.CliHelpVolume},
	{"login", locales.CliHelpLogin},
	{"logout", locales.CliHelpLogout},
	{synopses["daemon"], locales.CliHelpDaemon},
	{synopses["autostart"], locales.CliHelpAutostart},
	{"logs", locales.CliHelpLogs},
	{"version", locales.CliHelpVersion},
	{"help", locales.CliHelpHelp},
}

// synopses are the commands that take arguments; the others' is their
// name.
var synopses = map[string]string{
	"status":    "status [--json]",
	"seek":      "seek <sec|+sec|-sec>",
	"volume":    "volume <0-100|+n|-n>",
	"daemon":    "daemon start|stop|restart|status",
	"autostart": "autostart on|off|status",
}

// help writes the list of commands.
func (e *env) help() {
	width := 0
	for _, u := range usages {
		width = max(width, len("ytm ")+len(u.command))
	}
	var b strings.Builder
	b.WriteString(locales.CliHelpTitle())
	b.WriteString("\n\n")
	for _, u := range usages {
		line := strings.TrimSpace("ytm " + u.command)
		fmt.Fprintf(&b, "  %-*s  %s\n", width, line, u.about())
	}
	_, _ = io.WriteString(e.stdout, b.String())
}

// usage tells how command is used and returns exitUsage.
func (e *env) usage(command string) int {
	synopsis, ok := synopses[command]
	if !ok {
		synopsis = command
	}
	writeLine(e.stderr, locales.CliUsage("ytm "+synopsis))
	return exitUsage
}
