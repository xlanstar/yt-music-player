// Command ytmd is the background player that Desktop and ytm control over
// a local socket (internal/daemon): it plays on when they close. A front-end
// starts it when none answers; a second one exits at once, as the lock is
// held. It logs to <cache>/logs/ytmd.log at YT_LOG's level and stops on
// SIGTERM, SIGINT or a Shutdown request, pausing and saving first.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"yt-music-player/internal/daemon"
	"yt-music-player/internal/ipc"
	"yt-music-player/internal/logfile"
	"yt-music-player/internal/version"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	paths, err := ipc.Default(version.IsDev(version.Version))
	if err != nil {
		fmt.Fprintln(os.Stderr, "ytmd:", err)
		os.Exit(1)
	}
	logs := startLog(paths)
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
	}()
	go func() {
		<-svc.Done()
		_ = logs.Close()
		os.Exit(0)
	}()
	daemon.RunMain(svc.Start)
}

// startLog sends the global logger to the log file: started by a
// front-end, ytmd has no terminal.
func startLog(paths ipc.Paths) *logfile.Writer {
	logs := logfile.New(paths.Logs, strings.TrimSuffix(filepath.Base(paths.Log("ytmd")), ".log"))
	log.Logger = zerolog.New(logs).With().Timestamp().Logger() //nolint:forbidigo // the one place the global logger is pointed at the daemon's file
	level, err := logfile.Level(os.Getenv("YT_LOG"))
	zerolog.SetGlobalLevel(level)
	if err != nil {
		log.Warn().Err(err).Msg("YT_LOG")
	}
	return logs
}
