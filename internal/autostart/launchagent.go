package autostart

import (
	"bytes"
	"encoding/xml"
	"path/filepath"
	"strconv"
	"strings"
)

// appBundleID is the app's bundle identifier, which Login Items shows the
// LaunchAgent under (AssociatedBundleIdentifiers, macOS 13+) instead of sh.
const appBundleID = "dev.lanstar.ytmusicplayer"

// launchAgent is macOS's entry: a plist in ~/Library/LaunchAgents, which
// launchd loads at the next login. No KeepAlive, or launchd would start the
// daemon again after `ytm daemon stop`.
//
// It starts ytmd through sh, which removes the plist and disables the label
// instead when ytmd is gone: deleting the app, by brew, the Trash or
// anything, then turns starting at login off by itself at the next login.
type launchAgent struct {
	fileEntry
}

// newLaunchAgent returns the LaunchAgent under home for the user uid.
func newLaunchAgent(home string, uid int, run runner) launchAgent {
	target := "gui/" + strconv.Itoa(uid) + "/" + Label
	path := filepath.Join(home, "Library", "LaunchAgents", Label+".plist")
	return launchAgent{fileEntry{
		path:   path,
		render: func(ytmd string) []byte { return renderPlist(path, ytmd) },
		run:    run,
		// Not bootstrap and bootout, which would start or stop the daemon
		// now.
		on:  []string{"launchctl", "enable", target},
		off: []string{"launchctl", "disable", target},
	}}
}

func (a launchAgent) status() (bool, string, error) {
	data, err := a.read()
	if err != nil || data == nil {
		return false, "", err
	}
	return true, plistProgram(data), nil
}

// The plist's ProgramArguments are sh, -c, the script, then ytmd, which sh
// gives the script as $0: the path is never parsed as shell, whatever it
// holds. The script names the plist by the absolute path this package
// writes, single-quoted, rather than by ~: it removes that very file
// whatever HOME launchd gives sh, and a home moved is a plist Repair
// rewrites. launchctl and id are found on launchd's default PATH.
const (
	plistHead = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + Label + `</string>
	<key>AssociatedBundleIdentifiers</key>
	<array>
		<string>` + appBundleID + `</string>
	</array>
	<key>ProgramArguments</key>
	<array>
		<string>` + plistShell + `</string>
		<string>-c</string>
		<string>[ -x "$0" ] &amp;&amp; exec "$0"; rm -f '`
	plistScriptTail = `'; launchctl disable gui/$(id -u)/` + Label + `</string>
		<string>`
	plistTail = `</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>LimitLoadToSessionType</key>
	<string>Aqua</string>
	<key>ProcessType</key>
	<string>Interactive</string>
</dict>
</plist>
`
	plistShell = "/bin/sh"
)

// renderPlist is the LaunchAgent at path that starts ytmd.
func renderPlist(path, ytmd string) []byte {
	// Between single quotes, which keep everything but a single quote: it
	// closes them, is escaped and opens them again.
	quoted := strings.ReplaceAll(path, "'", `'\''`)
	var b bytes.Buffer
	b.Grow(len(plistHead) + len(quoted) + len(plistScriptTail) + len(ytmd) + len(plistTail))
	b.WriteString(plistHead)
	// Writes to a bytes.Buffer do not fail.
	_ = xml.EscapeText(&b, []byte(quoted))
	b.WriteString(plistScriptTail)
	_ = xml.EscapeText(&b, []byte(ytmd))
	b.WriteString(plistTail)
	return b.Bytes()
}

// plistProgram returns the program a LaunchAgent starts, or "" when it
// names none: the argument after sh's script in what renderPlist writes,
// the first of ProgramArguments in a plist of the versions before, which
// started ytmd directly.
func plistProgram(data []byte) string {
	args := plistArgs(data)
	switch {
	case len(args) == 4 && args[0] == plistShell && args[1] == "-c":
		return args[3]
	case len(args) > 0 && args[0] != plistShell:
		return args[0]
	}
	return ""
}

// plistArgs returns a plist's ProgramArguments, or nil when it has none.
func plistArgs(data []byte) []string {
	d := xml.NewDecoder(bytes.NewReader(data))
	var isArgs bool
	for {
		tok, err := d.Token()
		if err != nil {
			return nil
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch start.Name.Local {
		case "key":
			var key string
			if d.DecodeElement(&key, &start) != nil {
				return nil
			}
			isArgs = key == "ProgramArguments"
		case "array":
			if !isArgs {
				continue
			}
			var array struct {
				Strings []string `xml:"string"`
			}
			if d.DecodeElement(&array, &start) != nil {
				return nil
			}
			return array.Strings
		default:
			// A key's value that is not ProgramArguments' array, such
			// as AssociatedBundleIdentifiers' strings.
			isArgs = false
		}
	}
}
