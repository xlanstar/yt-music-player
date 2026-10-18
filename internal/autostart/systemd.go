package autostart

import (
	"bytes"
	"path/filepath"
	"strings"
)

// newSystemdUnit is Linux's entry: a user unit in config/systemd/user
// (XDG_CONFIG_HOME) that default.target wants, started at the next login.
// Restart=no, or systemd would restart ytmd after `ytm daemon stop`;
// ConditionPathExists skips it, without failing, once ytmd is gone.
func newSystemdUnit(config string, run runner) fileEntry {
	return fileEntry{
		path:   filepath.Join(config, "systemd", "user", unitName),
		render: renderUnit,
		run:    run,
		// Without --now, which would start or stop the daemon now.
		on:  []string{"systemctl", "--user", "enable", unitName},
		off: []string{"systemctl", "--user", "disable", unitName},
	}
}

const (
	unitHead = `[Unit]
Description=YT Music Player background player
ConditionPathExists=`
	unitMid = `

[Service]
ExecStart=`
	unitTail = `
Restart=no

[Install]
WantedBy=default.target
`
)

func renderUnit(ytmd string) []byte {
	var b bytes.Buffer
	b.Grow(len(unitHead) + len(ytmd) + len(unitMid) + len(ytmd) + 2 + len(unitTail))
	b.WriteString(unitHead)
	writeConditionPath(&b, ytmd)
	b.WriteString(unitMid)
	writeExecPath(&b, ytmd)
	b.WriteString(unitTail)
	return b.Bytes()
}

// writeConditionPath writes p as ConditionPathExists' path, which systemd
// takes as it is but for %-specifiers: no quotes, escapes or $VARIABLES, so
// only % is doubled. p is absolute, so never starts with the ! or | that
// negate or trigger.
func writeConditionPath(b *bytes.Buffer, p string) {
	for _, r := range p {
		if r == '%' {
			b.WriteByte('%')
		}
		b.WriteRune(r)
	}
}

// writeExecPath writes p as ExecStart's program. systemd splits the line at
// whitespace and unescapes backslashes in and out of quotes, so a path with
// either is quoted with its backslashes and quotes escaped; it expands
// %-specifiers and $VARIABLES, so % and $ are doubled.
func writeExecPath(b *bytes.Buffer, p string) {
	isQuoted := strings.ContainsAny(p, " \t\"'\\")
	if isQuoted {
		b.WriteByte('"')
	}
	for _, r := range p {
		switch r {
		case '\\', '"':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '%', '$':
			b.WriteRune(r)
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	if isQuoted {
		b.WriteByte('"')
	}
}
