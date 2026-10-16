package autostart

import (
	"bytes"
	"encoding/xml"
	"path/filepath"
	"strconv"
)

// launchAgent is macOS's entry: a plist in ~/Library/LaunchAgents, which
// launchd loads at the next login. No KeepAlive, or launchd would start the
// daemon again after `ytm daemon stop`.
type launchAgent struct {
	fileEntry
}

// newLaunchAgent returns the LaunchAgent under home for the user uid.
func newLaunchAgent(home string, uid int, run runner) launchAgent {
	target := "gui/" + strconv.Itoa(uid) + "/" + Label
	return launchAgent{fileEntry{
		path:   filepath.Join(home, "Library", "LaunchAgents", Label+".plist"),
		render: renderPlist,
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

const (
	plistHead = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + Label + `</string>
	<key>ProgramArguments</key>
	<array>
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

// renderPlist is the LaunchAgent that starts ytmd.
func renderPlist(ytmd string) []byte {
	var b bytes.Buffer
	b.Grow(len(plistHead) + len(ytmd) + len(plistTail))
	b.WriteString(plistHead)
	// Writes to a bytes.Buffer do not fail.
	_ = xml.EscapeText(&b, []byte(ytmd))
	b.WriteString(plistTail)
	return b.Bytes()
}

// plistProgram returns the program a LaunchAgent starts, the first string
// of its ProgramArguments, or "" when it has none.
func plistProgram(data []byte) string {
	d := xml.NewDecoder(bytes.NewReader(data))
	var isArgs bool
	for {
		tok, err := d.Token()
		if err != nil {
			return ""
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch start.Name.Local {
		case "key":
			var key string
			if d.DecodeElement(&key, &start) != nil {
				return ""
			}
			isArgs = key == "ProgramArguments"
		case "string":
			if !isArgs {
				continue
			}
			var program string
			if d.DecodeElement(&program, &start) != nil {
				return ""
			}
			return program
		}
	}
}
