package ipc

import (
	"net"
	"os"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

// fakeDaemonEnv makes the test binary a daemon listening on its value's
// socket (TestDialOrSpawnStarts).
const fakeDaemonEnv = "IPC_TEST_DAEMON"

func TestMain(m *testing.M) {
	if socket := os.Getenv(fakeDaemonEnv); socket != "" {
		os.Exit(fakeDaemon(socket))
	}
	// Only main sets the level: tests would otherwise print every failure
	// they provoke.
	zerolog.SetGlobalLevel(zerolog.Disabled)
	os.Exit(m.Run())
}

// fakeDaemon starts as ytmd does, takes one connection and exits; or exits
// after a while, should no one come.
func fakeDaemon(socket string) int {
	lock, err := TryLock(socket + ".lock")
	if err != nil {
		return 0
	}
	defer lock.Unlock() //nolint:errcheck // exiting releases it anyway
	l, err := Listen(socket)
	if err != nil {
		return 1
	}
	defer l.Close() //nolint:errcheck // exiting
	_ = l.SetDeadline(time.Now().Add(10 * time.Second))
	c, err := l.Accept()
	if err != nil {
		return 1
	}
	_ = c.Close()
	return 0
}

// shortDir is a temporary directory short enough for a socket's path:
// t.TempDir, unless the test's name makes it too long.
func shortDir(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	if len(dir) < 80 {
		return dir
	}
	dir, err := os.MkdirTemp("", "ipc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// listen listens on a socket in a short temporary directory.
func listen(t testing.TB) (*net.UnixListener, string) {
	t.Helper()
	path := shortDir(t) + string(os.PathSeparator) + "s.sock"
	l, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l, path
}
