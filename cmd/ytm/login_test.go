package main

import (
	"errors"
	"strings"
	"testing"
	"yt-music-player/internal/api"
	"yt-music-player/locales"
)

// cookie is credentials no output nor log may hold.
const cookie = "SID=s3cr3t-sid-value; SAPISID=s3cr3t-sapisid; __Secure-3PAPISID=s3cr3t-3p"

func TestLoginPiped(t *testing.T) {
	d := newDaemon("1.0.0", 7)
	w := newWorld(d)
	w.env.stdin = strings.NewReader(cookie + "\n")
	if code := w.run(t, "login"); code != exitOK {
		t.Fatalf("exit %d; stderr %q", code, w.stderr)
	}
	call := d.last(t)
	if call.Method != api.MethodSignIn || call.SignIn.Credentials != cookie+"\n" {
		t.Errorf("sent %v", call.Method)
	}
	if got, want := w.stdout.String(), "Signed in as Lan\n"; got != want {
		t.Errorf("stdout %q, want %q", got, want)
	}
	if w.stderr.Len() > 0 {
		t.Errorf("stderr %q: no steps for a pipe", w.stderr)
	}
}

func TestLoginLimit(t *testing.T) {
	t.Run("at", func(t *testing.T) {
		d := newDaemon("1.0.0", 7)
		w := newWorld(d)
		w.env.stdin = strings.NewReader(strings.Repeat("a", maxCredentials))
		if code := w.run(t, "login"); code != exitOK {
			t.Fatalf("exit %d; stderr %q", code, w.stderr)
		}
		if got := len(d.last(t).SignIn.Credentials); got != maxCredentials {
			t.Errorf("sent %d bytes", got)
		}
	})
	t.Run("over", func(t *testing.T) {
		logged := logAtDebug(t)
		input := strings.Repeat("a", maxCredentials-len(cookie)) + cookie + "x"
		w := newWorld(nil, newDaemon("1.0.0", 7))
		w.env.stdin = strings.NewReader(input)
		if code := w.run(t, "login"); code != exitFailed {
			t.Errorf("exit %d, want %d", code, exitFailed)
		}
		if w.dialer.spawned() != 0 {
			t.Error("connected for input refused")
		}
		if !strings.Contains(w.stderr.String(), "64 KiB") {
			t.Errorf("stderr %q", w.stderr)
		}
		noCookie(t, w, logged())
	})
	t.Run("empty", func(t *testing.T) {
		w := newWorld(nil, newDaemon("1.0.0", 7))
		w.env.stdin = strings.NewReader(" \n")
		if code := w.run(t, "login"); code != exitFailed {
			t.Errorf("exit %d, want %d", code, exitFailed)
		}
		if w.dialer.spawned() != 0 {
			t.Error("connected without input")
		}
	})
}

// TestLoginInvalid checks the refused credentials: told in the user's
// words, and nowhere in the output or the log.
func TestLoginInvalid(t *testing.T) {
	logged := logAtDebug(t)
	d := newDaemon("1.0.0", 7)
	d.answer = func(c *api.Call) *api.Reply {
		if c.Method != api.MethodSignIn {
			return nil
		}
		return &api.Reply{Err: &api.Error{Code: api.CodeInvalidCredentials, Detail: "no SAPISID cookie"}}
	}
	w := newWorld(d)
	w.env.stdin = strings.NewReader(cookie)
	if code := w.run(t, "login"); code != exitFailed {
		t.Errorf("exit %d, want %d", code, exitFailed)
	}
	for _, want := range []string{locales.CliLoginInvalid(), "no SAPISID cookie"} {
		if !strings.Contains(w.stderr.String(), want) {
			t.Errorf("stderr %q lacks %q", w.stderr, want)
		}
	}
	if !strings.Contains(logged(), "sign-in refused") {
		t.Errorf("log %q", logged())
	}
	noCookie(t, w, logged())
}

// TestLoginTerminal checks a sign-in typed into a terminal: the steps,
// the input read raw, and never echoed.
func TestLoginTerminal(t *testing.T) {
	logged := logAtDebug(t)
	d := newDaemon("1.0.0", 7)
	w := newWorld(d)
	w.env.stdin = strings.NewReader(cookie + "\r")
	w.env.isTerminal = func() bool { return true }
	isRaw := false
	w.env.rawInput = func() (func(), error) {
		isRaw = true
		return func() { isRaw = false }, nil
	}
	if code := w.run(t, "login"); code != exitOK {
		t.Fatalf("exit %d; stderr %q", code, w.stderr)
	}
	if isRaw {
		t.Error("the terminal stays raw")
	}
	if got := d.last(t).SignIn.Credentials; got != cookie {
		t.Errorf("sent %d bytes, want the line", len(got))
	}
	for _, want := range []string{locales.SignInTitle(), locales.SignInPickRequest(), locales.CliCookiePrompt()} {
		if !strings.Contains(w.stderr.String(), want) {
			t.Errorf("stderr lacks %q", want)
		}
	}
	noCookie(t, w, logged())
}

func TestLoginTerminalFails(t *testing.T) {
	w := newWorld(nil, newDaemon("1.0.0", 7))
	w.env.isTerminal = func() bool { return true }
	w.env.rawInput = func() (func(), error) { return nil, errors.New("inappropriate ioctl") }
	if code := w.run(t, "login"); code != exitFailed {
		t.Errorf("exit %d, want %d", code, exitFailed)
	}
}

// noCookie fails t when the cookie, or a part of it, is in w's output or
// logged.
func noCookie(t *testing.T, w *world, logged string) {
	t.Helper()
	for name, text := range map[string]string{"stdout": w.stdout.String(), "stderr": w.stderr.String(), "log": logged} {
		if strings.Contains(text, "s3cr3t") {
			t.Errorf("%s holds the cookie: %q", name, text)
		}
	}
}

func TestReadHidden(t *testing.T) {
	for _, tc := range []struct {
		name, in, want string
		err            error
	}{
		{"enter", "SID=a\rrest", "SID=a", nil},
		{"newline", "SID=a\n", "SID=a", nil},
		{"eof", "SID=a", "SID=a", nil},
		{"ctrl-d", "SID=a\x04", "SID=a", nil},
		{"backspace", "SID=ab\x7f\x7fa\r", "SID=a", nil},
		{"backspace a rune", "é\x08x\r", "x", nil},
		{"backspace nothing", "\x7fa\r", "a", nil},
		{"ctrl-c", "SID=a\x03\r", "", errInterrupted},
		{"empty", "\r", "", errNoInput},
		{"spaces", "  \r", "", errNoInput},
		{"over", strings.Repeat("a", 9) + "\rnext", "", errTooLong},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := readHidden(strings.NewReader(tc.in), 8)
			if got != tc.want || !errors.Is(err, tc.err) {
				t.Errorf("readHidden = %q, %v; want %q, %v", got, err, tc.want, tc.err)
			}
		})
	}
}

func TestReadAll(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
		err  error
	}{
		{"SID=a\n", "SID=a\n", nil},
		{"12345678", "12345678", nil},
		{"123456789", "", errTooLong},
		{"\n\t", "", errNoInput},
	} {
		got, err := readAll(strings.NewReader(tc.in), 8)
		if got != tc.want || !errors.Is(err, tc.err) {
			t.Errorf("readAll(%q) = %q, %v; want %q, %v", tc.in, got, err, tc.want, tc.err)
		}
	}
}

func TestLogout(t *testing.T) {
	d := newDaemon("1.0.0", 7)
	w := newWorld(d)
	if code := w.run(t, "logout"); code != exitOK {
		t.Fatalf("exit %d", code)
	}
	if got, want := w.stdout.String(), locales.NoticeSignedOut()+"\n"; got != want {
		t.Errorf("stdout %q, want %q", got, want)
	}
}
