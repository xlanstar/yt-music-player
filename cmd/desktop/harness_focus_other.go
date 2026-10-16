//go:build memprof && !darwin

package main

// yieldFocus is a no-op: footprint(1), and so the footprint harness, is macOS only.
func yieldFocus() {}
