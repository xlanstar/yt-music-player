//go:build !darwin && !windows

package main

// systemLanguages returns nothing: Linux has no list of languages besides
// the locale variables.
func systemLanguages() []string { return nil }
