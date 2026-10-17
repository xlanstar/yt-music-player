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

// newLaunchAgent is macOS's entry for the user uid: a plist in
// home/Library/LaunchAgents, which launchd loads at the next login. No
// KeepAlive, or launchd would start the daemon again after `ytm daemon
// stop`.
//
// It starts ytmd through sh, which removes the plist and disables the label
// instead when ytmd is gone: deleting the app, by brew, the Trash or
// anything, then turns starting at login off by itself at the next login.
func newLaunchAgent(home string, uid int, run runner) fileEntry {
	target := "gui/" + strconv.Itoa(uid) + "/" + label
	path := filepath.Join(home, "Library", "LaunchAgents", label+".plist")
	return fileEntry{
		path:   path,
		render: func(ytmd string) []byte { return renderPlist(path, ytmd) },
		run:    run,
		// Not bootstrap and bootout, which would start or stop the daemon
		// now.
		on:  []string{"launchctl", "enable", target},
		off: []string{"launchctl", "disable", target},
	}
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
	<string>` + label + `</string>
	<key>AssociatedBundleIdentifiers</key>
	<array>
		<string>` + appBundleID + `</string>
	</array>
	<key>ProgramArguments</key>
	<array>
		<string>/bin/sh</string>
		<string>-c</string>
		<string>[ -x "$0" ] &amp;&amp; exec "$0"; rm -f '`
	plistScriptTail = `'; launchctl disable gui/$(id -u)/` + label + `</string>
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
