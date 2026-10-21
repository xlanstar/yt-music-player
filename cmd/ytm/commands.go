package main

import (
	"context"
	"slices"
	"yt-music-player/internal/api"
	"yt-music-player/locales"
)

// command is one of ytm's commands; run dispatches by it, help lists it
// and usage says it.
type command struct {
	name    string
	aliases []string
	// synopsis is the name and arguments as help and usage say them,
	// never translated; "" says the name alone.
	synopsis string
	// about says what it does in help; nil joins the help line before
	// (play | pause | toggle).
	about func() string
	// hasArgs hands run the arguments; without it, any argument is told
	// usage.
	hasArgs bool
	// isRepairSkipped: autostart switches the login entry itself.
	isRepairSkipped bool
	run             func(e *env, ctx context.Context, args []string) int
}

// commands are ytm's commands, in help's order. A func, not a var: usage
// reads it, and the commands' runs call usage.
func commands() []command {
	return []command{
		{
			name: "status", synopsis: "status [--json]", about: locales.CliHelpStatus,
			hasArgs: true, run: (*env).statusCommand,
		},
		{name: "play", about: locales.CliHelpPlay, run: control(api.MethodResume)},
		{name: "pause", run: control(api.MethodPause)},
		{name: "toggle", run: control(api.MethodToggle)},
		{name: "next", about: locales.CliHelpNext, run: control(api.MethodNext)},
		{name: "prev", run: control(api.MethodPrev)},
		{
			name: "seek", synopsis: "seek <sec|+sec|-sec>", about: locales.CliHelpSeek,
			hasArgs: true, run: (*env).seekCommand,
		},
		{
			name: "volume", synopsis: "volume <0-100|+n|-n>", about: locales.CliHelpVolume,
			hasArgs: true, run: (*env).volumeCommand,
		},
		{name: "login", about: locales.CliHelpLogin, run: withoutArgs((*env).login)},
		{name: "logout", about: locales.CliHelpLogout, run: withoutArgs((*env).logout)},
		{
			name: "daemon", synopsis: "daemon start|stop|restart|status", about: locales.CliHelpDaemon,
			hasArgs: true, run: (*env).daemonCommand,
		},
		{
			name: "autostart", synopsis: "autostart on|off|status", about: locales.CliHelpAutostart,
			hasArgs: true, isRepairSkipped: true,
			run: func(e *env, _ context.Context, args []string) int { return e.autostartCommand(args) },
		},
		{
			name: "logs", about: locales.CliHelpLogs,
			run: func(e *env, _ context.Context, _ []string) int { return e.logs() },
		},
		{name: "version", about: locales.CliHelpVersion, run: withoutArgs((*env).versionCommand)},
		// help says the same whatever follows.
		{
			name: "help", aliases: []string{"-h", "--help"}, about: locales.CliHelpHelp, hasArgs: true,
			run: func(e *env, _ context.Context, _ []string) int { e.help(); return exitOK },
		},
	}
}

// findCommand returns the command called name, by its name or an alias.
func findCommand(name string) (command, bool) {
	for _, c := range commands() {
		if c.name == name || slices.Contains(c.aliases, name) {
			return c, true
		}
	}
	return command{}, false
}

// control runs a command that calls method and takes no arguments.
func control(method api.Method) func(*env, context.Context, []string) int {
	return func(e *env, ctx context.Context, _ []string) int { return e.call(ctx, api.Call{Method: method}) }
}

// withoutArgs runs a command that reads no arguments.
func withoutArgs(run func(*env, context.Context) int) func(*env, context.Context, []string) int {
	return func(e *env, ctx context.Context, _ []string) int { return run(e, ctx) }
}

// synopsisOf is how c is used, after "ytm ".
func (c command) synopsisOf() string {
	if c.synopsis == "" {
		return c.name
	}
	return c.synopsis
}
