package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strconv"
	"strings"
	"unicode/utf8"
	"yt-music-player/internal/api"
	"yt-music-player/internal/catalog"
	"yt-music-player/locales"

	"github.com/rs/zerolog/log"
)

// maxCredentials caps what login reads: a Cookie header is a few KiB.
const maxCredentials = 64 << 10

// Why login read no credentials. None holds what was read.
var (
	errTooLong     = errors.New("ytm: credentials longer than 64 KiB")
	errNoInput     = errors.New("ytm: no credentials")
	errInterrupted = errors.New("ytm: interrupted")
)

// devtoolsKeys open a browser's developer tools, as Desktop says them.
var devtoolsKeys = func() string {
	if runtime.GOOS == "darwin" {
		return "⌥⌘I"
	}
	return "Ctrl+Shift+I"
}()

// login is `ytm login`: the daemon signs in with a Cookie header read
// from stdin, hidden on a terminal. No flag or variable takes it, which
// shell history, ps or the environment would keep; no error or log holds
// it.
func (e *env) login(ctx context.Context) int {
	what := locales.NoticeSignInFailed()
	credentials, err := e.readCredentials()
	if err != nil {
		log.Debug().Err(err).Msg("ytm: reading credentials")
		switch {
		case errors.Is(err, errTooLong):
			writeLine(e.stderr, locales.CliFailed(what, locales.CliLoginTooLong()))
		case errors.Is(err, errNoInput):
			writeLine(e.stderr, locales.CliFailed(what, locales.CliLoginEmpty()))
		case errors.Is(err, errInterrupted):
			writeLine(e.stderr, locales.CliFailed(what, locales.CliWhyCanceled()))
		default:
			// A read error says nothing of what was read.
			writeLine(e.stderr, locales.CliFailed(what, err.Error()))
		}
		return exitFailed
	}
	c, ok := e.open(ctx, what)
	if !ok {
		return exitFailed
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(ctx, signInTimeout)
	defer cancel()
	account, err := c.SignIn(ctx, credentials)
	switch {
	case errors.Is(err, catalog.ErrInvalidCredentials):
		log.Debug().Err(err).Msg("ytm: sign-in refused")
		if re, ok := errors.AsType[*api.Error](err); ok && re.Detail != "" {
			// Detail is the daemon's, safe to show.
			writeLine(e.stderr, locales.SignInFailedReason(re.Detail))
		}
		writeLine(e.stderr, locales.CliLoginInvalid())
		return exitFailed
	case err != nil:
		return e.fail(what, err)
	}
	writeLine(e.stdout, locales.SignedInAs(accountName(account.Name, account.Handle)))
	return exitOK
}

// readCredentials reads the Cookie header: on a terminal after the
// steps to copy it, a line without echo; from a pipe, all of it.
func (e *env) readCredentials() (string, error) {
	if !e.isTerminal() {
		return readAll(e.stdin, maxCredentials)
	}
	e.signInSteps()
	restore, err := e.rawInput()
	if err != nil {
		return "", err
	}
	s, err := readHidden(e.stdin, maxCredentials)
	restore()
	// The Enter that ended the line, unechoed.
	writeLine(e.stderr, "")
	return s, err
}

// signInSteps says how to copy the Cookie header, as Desktop's sign-in
// sheet does.
func (e *env) signInSteps() {
	var b strings.Builder
	b.WriteString(locales.SignInTitle())
	b.WriteString("\n")
	b.WriteString(locales.SignInIntro())
	b.WriteString("\n\n")
	for i, step := range []string{
		locales.SignInOpenBefore() + "music.youtube.com" + locales.SignInOpenAfter(),
		locales.SignInDevtools(devtoolsKeys),
		locales.SignInPickRequest(),
		locales.SignInPaste(),
	} {
		b.WriteString("  ")
		b.WriteString(strconv.Itoa(i + 1))
		b.WriteString(". ")
		b.WriteString(step)
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(locales.SignInPrivacy())
	b.WriteString("\n")
	b.WriteString(locales.CliCookiePrompt())
	_, _ = io.WriteString(e.stderr, b.String())
}

// readAll reads r to its end, at most limit bytes.
func readAll(r io.Reader, limit int) (string, error) {
	data, err := io.ReadAll(io.LimitReader(r, int64(limit)+1))
	switch {
	case err != nil:
		return "", fmt.Errorf("ytm: reading stdin: %w", err)
	case len(data) > limit:
		return "", errTooLong
	case strings.TrimSpace(string(data)) == "":
		return "", errNoInput
	}
	return string(data), nil
}

// The keys readHidden reads in raw mode.
const (
	keyInterrupt = 0x03 // Ctrl+C
	keyEOF       = 0x04 // Ctrl+D
	keyBackspace = 0x08
	keyDelete    = 0x7f // what Backspace sends on most terminals
)

// readHidden reads one line of at most limit bytes from a terminal in raw
// mode: a cooked one cuts lines at 1 KiB on macOS, shorter than most Cookie
// headers. A longer line is read to its end, kept from the shell, and refused.
func readHidden(r io.Reader, limit int) (string, error) {
	br := bufio.NewReader(r)
	line := make([]byte, 0, 4096)
	isLong := false
	for {
		c, err := br.ReadByte()
		if errors.Is(err, io.EOF) {
			c, err = keyEOF, nil
		}
		if err != nil {
			return "", fmt.Errorf("ytm: reading stdin: %w", err)
		}
		switch c {
		case '\r', '\n', keyEOF:
			switch {
			case isLong:
				return "", errTooLong
			case strings.TrimSpace(string(line)) == "":
				return "", errNoInput
			}
			return string(line), nil
		case keyInterrupt:
			return "", errInterrupted
		case keyBackspace, keyDelete:
			if _, size := utf8.DecodeLastRune(line); size > 0 {
				line = line[:len(line)-size]
			}
		default:
			if len(line) == limit {
				isLong = true
				continue
			}
			line = append(line, c)
		}
	}
}

// logout is `ytm logout`.
func (e *env) logout(ctx context.Context) int {
	what := locales.NoticeSignOutFailed()
	c, ok := e.open(ctx, what)
	if !ok {
		return exitFailed
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	if _, err := c.Call(ctx, api.Call{Method: api.MethodSignOut}); err != nil {
		return e.fail(what, err)
	}
	writeLine(e.stdout, locales.NoticeSignedOut())
	return exitOK
}
