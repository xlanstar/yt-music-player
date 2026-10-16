//go:build unix

package ipc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepare(t *testing.T) {
	root := t.TempDir()
	r := Roots{
		Config: filepath.Join(root, "config"), Cache: filepath.Join(root, "cache"),
		Runtime: filepath.Join(root, "run"), Temp: filepath.Join(root, "tmp"), UID: os.Getuid(),
	}
	if err := os.Mkdir(r.Runtime, 0o700); err != nil {
		t.Fatal(err)
	}
	p, err := resolve(r, "linux", "")
	if err != nil {
		t.Fatal(err)
	}
	// One made too open is closed.
	sockDir := filepath.Dir(p.Socket)
	if err := os.Mkdir(sockDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := p.Prepare(); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{sockDir, filepath.Dir(p.State), p.Logs} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Errorf("%s is %v, want 0700", dir, info.Mode().Perm())
		}
	}
}

func TestPrivateDirRefuses(t *testing.T) {
	root := t.TempDir()
	// Someone else's: the UID is not the owner's.
	theirs := filepath.Join(root, "theirs")
	if err := privateDir(theirs, os.Getuid()+1, true); err == nil || !strings.Contains(err.Error(), "another user") {
		t.Errorf("another user's directory: err %v", err)
	}
	// A symlink in a shared directory, maybe to someone else's.
	link := filepath.Join(root, "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	if err := privateDir(link, os.Getuid(), true); err == nil {
		t.Error("a symlink in a shared directory is taken")
	}
	// A file.
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := privateDir(file, os.Getuid(), false); err == nil {
		t.Error("a file is taken for a directory")
	}
}
