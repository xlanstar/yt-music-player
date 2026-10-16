//go:build memprof && darwin

package main

import (
	"github.com/ebitengine/purego/objc"
	"github.com/egoist/mygo"
)

// yieldFocus makes the app give up the focus: since macOS 14 another app
// may only take it when this one yields. Window.Blur only orders the
// window back, and the app keeps the keyboard.
func yieldFocus() {
	mygo.RunOnMain(func() {
		app := objc.ID(objc.GetClass("NSApplication")).Send(objc.RegisterName("sharedApplication"))
		app.Send(objc.RegisterName("deactivate"))
	})
}
