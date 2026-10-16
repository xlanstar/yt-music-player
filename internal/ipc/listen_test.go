package ipc

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestListenRemovesStaleSocket(t *testing.T) {
	path := filepath.Join(shortDir(t), "s.sock")
	// A daemon that crashed leaves its socket behind.
	crashed, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	crashed.SetUnlinkOnClose(false)
	if err := crashed.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = Dial(t.Context(), path)
	if err == nil {
		t.Fatal("dialed a stale socket")
	}
	if runtime.GOOS != "windows" && dialFailure(err) != "stale socket" {
		t.Errorf("dialFailure(%v) = %q", err, dialFailure(err))
	}

	l, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Dial(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	if info, err := os.Stat(path); err != nil {
		t.Fatal(err)
	} else if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("socket is %v, want 0600", info.Mode().Perm())
	}

	// Closing the listener removes the socket.
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Errorf("socket left after Close: %v", err)
	}
	_, err = Dial(context.Background(), path)
	if runtime.GOOS != "windows" && dialFailure(err) != "no socket" {
		t.Errorf("dialFailure(%v) = %q", err, dialFailure(err))
	}
}

// Anything but a socket stays, for Listen to fail on.
func TestListenLeavesOtherFiles(t *testing.T) {
	path := filepath.Join(shortDir(t), "s.sock")
	if err := os.WriteFile(path, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	if l, err := Listen(path); err == nil {
		_ = l.Close()
		t.Fatal("listened over a file")
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "mine" {
		t.Errorf("file now %q, %v", got, err)
	}
}
