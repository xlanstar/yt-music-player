package main

import "runtime"

// init keeps main on the main thread, whose run loop the media controls
// need (daemon.RunMain).
func init() { runtime.LockOSThread() }
