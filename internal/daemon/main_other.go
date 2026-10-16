//go:build !darwin

package daemon

// RunMain runs start and never returns: exit the process to stop. Only
// macOS needs the main thread (see its RunMain); elsewhere it just waits.
func RunMain(start func()) {
	start()
	select {}
}
