// Command ytmd is the background player that Desktop and ytm control over
// a local socket (internal/daemon, docs/daemon.md). A second one exits at
// once, as the lock is held. It logs to <cache>/logs/ytmd.log at YT_LOG's
// level and stops on SIGTERM, SIGINT or a Shutdown request.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"syscall"
	"yt-music-player/internal/daemon"
	"yt-music-player/internal/ipc"
	"yt-music-player/internal/logfile"
	"yt-music-player/internal/version"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// gcPercent is ytmd's GOGC: while a song plays, MyGo's audio output leaves
// garbage on every buffer that internal/quiet never frees, so the heap sits
// at its goal. Half Go's default costs no measurable CPU (docs/performance.md).
const gcPercent = 50

// maxProcs caps ytmd's GOMAXPROCS: on 16 cores, 12 more Ps and their
// threads cost ~0.7 MB of footprint and save no CPU; 4 keep the audio fed
// under 8 busy goroutines, where 2 underrun (docs/performance.md).
const maxProcs = 4

func main() {
	// One set in the environment wins, to compare.
	if os.Getenv("GOGC") == "" {
		debug.SetGCPercent(gcPercent)
	}
	if n := procs(os.Getenv("GOMAXPROCS"), runtime.GOMAXPROCS(0)); n > 0 {
		runtime.GOMAXPROCS(n)
	}
	paths, err := ipc.DefaultPaths(version.IsDev(version.Version))
	if err != nil {
		fmt.Fprintln(os.Stderr, "ytmd:", err)
		os.Exit(1)
	}
	logs := startLog(paths)
	serveProfiles()
	svc, err := daemon.Open(paths, options())
	if errors.Is(err, ipc.ErrLocked) {
		// Another ytmd serves already; whoever started this one finds it.
		os.Exit(0)
	}
	if err != nil {
		log.Error().Err(err).Msg("start")
		fmt.Fprintln(os.Stderr, "ytmd:", err)
		os.Exit(1)
	}
	go func() {
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
		select {
		case <-signals:
			svc.Shutdown()
		case <-svc.Done():
		}
		_ = logs.Close()
		os.Exit(0)
	}()
	daemon.RunMain(svc.Start)
}

// procs is the GOMAXPROCS ytmd sets, 0 for none: setting one ends the
// runtime's updates to its default, so only to lower it, unless the
// environment set one.
func procs(env string, current int) int {
	if env != "" || current <= maxProcs {
		return 0
	}
	return maxProcs
}

// startLog sends the global logger to the log file: started by a
// front-end, ytmd has no terminal.
func startLog(paths ipc.Paths) *logfile.Writer {
	logs := logfile.New(paths.Log("ytmd"))
	log.Logger = zerolog.New(logs).With().Timestamp().Logger() //nolint:forbidigo // the one place the global logger is pointed at the daemon's file
	level, err := logfile.Level(os.Getenv("YT_LOG"))
	zerolog.SetGlobalLevel(level)
	if err != nil {
		log.Warn().Err(err).Msg("YT_LOG")
	}
	return logs
}
