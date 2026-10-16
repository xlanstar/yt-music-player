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
	{"status [--json]", locales.CliHelpStatus},
	{"play | pause | toggle", locales.CliHelpPlay},
	{"next | prev", locales.CliHelpNext},
	{"seek <sec|+sec|-sec>", locales.CliHelpSeek},
	{"volume <0-100|+n|-n>", locales.CliHelpVolume},
	{"login", locales.CliHelpLogin},
	{"logout", locales.CliHelpLogout},
	{"daemon start|stop|restart|status", locales.CliHelpDaemon},
	{"autostart on|off|status", locales.CliHelpAutostart},
	{"logs", locales.CliHelpLogs},
	{"version", locales.CliHelpVersion},
	{"help", locales.CliHelpHelp},
}

// synopses are the arguments each command takes, for its usage error.
var synopses = map[string]string{
	"status":    "status [--json]",
	"play":      "play",
	"pause":     "pause",
	"toggle":    "toggle",
	"next":      "next",
	"prev":      "prev",
	"seek":      "seek <sec|+sec|-sec>",
	"volume":    "volume <0-100|+n|-n>",
	"login":     "login",
	"logout":    "logout",
	"daemon":    "daemon start|stop|restart|status",
	"autostart": "autostart on|off|status",
	"logs":      "logs",
	"version":   "version",
}

// help writes the list of commands.
func (e *env) help(w io.Writer) {
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
	_, _ = io.WriteString(w, b.String())
}

// usage tells how command is used and returns exitUsage.
func (e *env) usage(command string) int {
	writeLine(e.stderr, locales.CliUsage("ytm "+synopses[command]))
	return exitUsage
}
