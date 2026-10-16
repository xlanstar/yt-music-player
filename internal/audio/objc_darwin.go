package audio

import (
	"errors"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// MediaPlayer is driven through the Objective-C runtime with purego, as
// MyGo drives AppKit: no cgo. All calls happen on the main thread, which is
// MyGo's UI thread; the engine's goroutines and system callbacks hop there
// through the main dispatch queue.

var (
	poolPush, poolPop, mainNP, dispatchAsyncF, dispatchSyncF, mainQueue uintptr

	// Typed objc_msgSend for float arguments and results, registered
	// once: objc.Send registers on every call.
	msgF64     func(obj objc.ID, sel objc.SEL) float64
	msgNumber  func(cls objc.ID, sel objc.SEL, v float64) objc.ID
	msgFromStr func(obj objc.ID, sel objc.SEL, b *byte, n uint, enc uint) objc.ID

	// runTask is the one C callback the main queue runs; its context is a
	// key into tasks, as purego callbacks are limited and never freed.
	runTask    uintptr
	taskMu     sync.Mutex
	tasks      = map[uintptr]func(){}
	lastTaskID uintptr
)

var loadObjC = sync.OnceValue(func() error {
	objcLib, err := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	if err != nil {
		return err
	}
	sym := func(lib uintptr, name string) uintptr {
		ptr, symErr := purego.Dlsym(lib, name)
		err = errors.Join(err, symErr)
		return ptr
	}
	msgSend := sym(objcLib, "objc_msgSend")
	poolPush, poolPop = sym(objcLib, "objc_autoreleasePoolPush"), sym(objcLib, "objc_autoreleasePoolPop")
	mainNP = sym(purego.RTLD_DEFAULT, "pthread_main_np")
	dispatchAsyncF = sym(purego.RTLD_DEFAULT, "dispatch_async_f")
	dispatchSyncF = sym(purego.RTLD_DEFAULT, "dispatch_sync_f")
	// dispatch_get_main_queue() is this symbol's address.
	mainQueue = sym(purego.RTLD_DEFAULT, "_dispatch_main_q")
	if err != nil {
		return err
	}
	for _, fn := range []any{&msgF64, &msgNumber, &msgFromStr} {
		purego.RegisterFunc(fn, msgSend)
	}
	runTask = purego.NewCallback(func(ctx uintptr) {
		taskMu.Lock()
		fn := tasks[ctx]
		delete(tasks, ctx)
		taskMu.Unlock()
		if fn != nil {
			withPool(fn)
		}
	})
	return nil
})

func task(fn func()) uintptr {
	taskMu.Lock()
	defer taskMu.Unlock()
	lastTaskID++
	tasks[lastTaskID] = fn
	return lastTaskID
}

func isMain() bool {
	r, _, _ := purego.SyscallN(mainNP)
	return r != 0
}

// onMain runs fn on the main thread and waits for it.
func onMain(fn func()) {
	if isMain() {
		withPool(fn)
		return
	}
	purego.SyscallN(dispatchSyncF, mainQueue, task(fn), runTask)
}

// later queues fn on the main thread: calls keep their order, and no
// caller waits on the main thread, which may wait on the engine.
func later(fn func()) { purego.SyscallN(dispatchAsyncF, mainQueue, task(fn), runTask) }

// withPool drains what fn autoreleases. Only called on the main thread,
// so push and pop happen on the same thread.
func withPool(fn func()) {
	p, _, _ := purego.SyscallN(poolPush)
	defer purego.SyscallN(poolPop, p)
	fn()
}

var selCache sync.Map // string → objc.SEL

func sel(name string) objc.SEL {
	if s, ok := selCache.Load(name); ok {
		return s.(objc.SEL)
	}
	s := objc.RegisterName(name)
	selCache.Store(name, s)
	return s
}

func class(name string) objc.ID { return objc.ID(objc.GetClass(name)) }

func send(obj objc.ID, selector string, args ...any) objc.ID {
	return obj.Send(sel(selector), args...)
}

// nsString returns an autoreleased NSString.
func nsString(s string) objc.ID {
	str := msgFromStr(send(class("NSString"), "alloc"), sel("initWithBytes:length:encoding:"),
		unsafe.StringData(s), uint(len(s)), 4 /* UTF-8 */)
	return send(str, "autorelease")
}

// constant returns the NSString a framework exports as name, or 0.
func constant(lib uintptr, name string) objc.ID {
	ptr, err := purego.Dlsym(lib, name)
	if err != nil || ptr == 0 {
		return 0
	}
	return **(**objc.ID)(unsafe.Pointer(&ptr))
}

func nsNumber(v float64) objc.ID { return msgNumber(class("NSNumber"), sel("numberWithDouble:"), v) }
