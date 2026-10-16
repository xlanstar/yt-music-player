package daemon

import (
	"errors"
	"sync"

	"github.com/ebitengine/purego"
	"github.com/rs/zerolog/log"
)

// RunMain runs the main thread's run loop, which serves the main dispatch
// queue that package audio's Now Playing and media keys use, and never
// returns: exit the process to stop. Call it from main, on the main
// thread, locked to it from an init (runtime.LockOSThread). start runs
// once the run loop runs, so that the engine, made after it, finds the
// queue served: made before, it waits on the main queue forever.
func RunMain(start func()) {
	cf, err := loadRunLoop()
	if err != nil {
		log.Warn().Err(err).Msg("no run loop, so no media keys")
		start()
		select {}
	}
	started := purego.NewCallback(func(uintptr) { start() })
	purego.SyscallN(cf.dispatchAsyncF, cf.mainQueue, 0, started)
	for {
		// It returns only when the loop has nothing to serve, which the
		// main queue, once used, always has.
		purego.SyscallN(cf.run)
	}
}

type runLoop struct{ run, dispatchAsyncF, mainQueue uintptr }

var loadRunLoop = sync.OnceValues(func() (runLoop, error) {
	const path = "/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation"
	lib, err := purego.Dlopen(path, purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	if err != nil {
		return runLoop{}, err
	}
	sym := func(lib uintptr, name string) uintptr {
		ptr, symErr := purego.Dlsym(lib, name)
		err = errors.Join(err, symErr)
		return ptr
	}
	cf := runLoop{
		run:            sym(lib, "CFRunLoopRun"),
		dispatchAsyncF: sym(purego.RTLD_DEFAULT, "dispatch_async_f"),
		// dispatch_get_main_queue() is this symbol's address.
		mainQueue: sym(purego.RTLD_DEFAULT, "_dispatch_main_q"),
	}
	return cf, err
})
