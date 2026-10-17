package autostart

import (
	"bufio"
	"bytes"
	"path/filepath"
	"strings"
)

// systemdUnit is Linux's entry: a user unit in <config>/systemd/user that
// default.target wants, so the user's manager starts it at the next login.
// Restart=no, or systemd would start the daemon again after `ytm daemon
// stop`. ConditionPathExists has systemd skip the unit, which is no
// failure, once ytmd is gone with the app.
type systemdUnit struct {
	fileEntry
}

// newSystemdUnit returns the unit under config, the user's config
// directory (XDG_CONFIG_HOME).
func newSystemdUnit(config string, run runner) systemdUnit {
	return systemdUnit{fileEntry{
		path:   filepath.Join(config, "systemd", "user", UnitName),
		render: renderUnit,
		run:    run,
		// Without --now, which would start or stop the daemon now.
		on:  []string{"systemctl", "--user", "enable", UnitName},
		off: []string{"systemctl", "--user", "disable", UnitName},
	}}
}

func (u systemdUnit) status() (bool, string, error) {
	data, err := u.read()
	if err != nil || data == nil {
		return false, "", err
	}
	return true, unitProgram(data), nil
}

// Unit returns the user unit that starts ytmd, an absolute path: what
// Enable writes on Linux, and the template packages ship beside ytmd.
func Unit(ytmd string) ([]byte, error) {
	if err := checkPath(ytmd); err != nil {
		return nil, err
	}
	return renderUnit(ytmd), nil
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

// unitProgram returns the program a unit's ExecStart names, undoing
// writeExecPath, or "" when it has none.
func unitProgram(data []byte) string {
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "ExecStart="); ok {
			return readExecPath(v)
		}
	}
	return ""
}

func readExecPath(v string) string {
	var b strings.Builder
	isQuoted := strings.HasPrefix(v, `"`)
	if isQuoted {
		v = v[1:]
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case isQuoted && c == '"', !isQuoted && (c == ' ' || c == '\t'):
			return b.String()
		case c == '\\' && i+1 < len(v):
			i++
			b.WriteByte(v[i])
		case (c == '%' || c == '$') && i+1 < len(v) && v[i+1] == c:
			i++
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}
