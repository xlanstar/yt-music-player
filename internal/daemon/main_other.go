//go:build !darwin

package daemon

// RunMain runs start and never returns: only macOS needs the main thread.
func RunMain(start func()) {
	start()
	select {}
}
