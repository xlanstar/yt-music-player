package main

import "path/filepath"

// ytmCommand is the name of the command line the app ships beside it, and
// of the command Linux packages put on PATH (mygo.json's linux.command).
const ytmCommand = "ytm"

// ytmTarget returns the ytm to run instead of this app when it was started
// as arg0, its real executable being exe: MyGo's linux.command can only
// link a command, /usr/bin/ytm or ~/.local/bin/ytm, to the app itself.
// Started under any other name, or ytm itself, it runs no other.
func ytmTarget(arg0, exe string) (string, bool) {
	if filepath.Base(arg0) != ytmCommand || filepath.Base(exe) == ytmCommand {
		return "", false
	}
	return filepath.Join(filepath.Dir(exe), ytmCommand), true
}
