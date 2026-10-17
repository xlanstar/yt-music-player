package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// runYtm becomes the ytm beside this app when the app was started as ytm,
// before MyGo or the app start anything: MyGo's init only locks the main
// thread.
func runYtm() {
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		return
	}
	path := ytmTarget(os.Args[0], exe)
	if path == "" {
		return
	}
	err = syscall.Exec(path, os.Args, os.Environ()) //nolint:gosec // G204: the ytm installed beside this app
	fmt.Fprintf(os.Stderr, "ytm: %s: %v\n", path, err)
	os.Exit(1)
}
