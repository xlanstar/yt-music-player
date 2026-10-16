package main

import "golang.org/x/term"

// isTerminal reports whether fd is a terminal.
func isTerminal(fd int) bool { return term.IsTerminal(fd) }

// rawMode puts the terminal fd in raw mode, without echo, until restore.
func rawMode(fd int) (restore func(), err error) {
	old, err := term.MakeRaw(fd)
	if err != nil {
		return nil, err
	}
	return func() { _ = term.Restore(fd, old) }, nil
}
