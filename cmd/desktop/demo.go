//go:build !placeholder

package main

// demo is a constant so that the release build links no placeholder
// catalog: demo_placeholder.go starts with the offline fake's music, which
// a placeholder ytmd plays (-tags placeholder).
const demo = false
