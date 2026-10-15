package audio

import (
	"errors"
	"structs"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// AVFoundation and MediaPlayer are driven through the Objective-C runtime
// with purego, as MyGo drives AppKit: no cgo. All calls happen on the main
// thread, which is MyGo's UI thread; system callbacks arrive on other
// threads and hop there through the main dispatch queue.

var (
	poolPush, poolPop, mainNP, dispatchAsyncF, dispatchSyncF, mainQueue, libAV uintptr

	// Typed objc_msgSend for float and struct arguments and results,
	// registered once: objc.Send registers on every call.
	msgSetF32  func(obj objc.ID, sel objc.SEL, v float32)
	msgF64     func(obj objc.ID, sel objc.SEL) float64
	msgTime    func(obj objc.ID, sel objc.SEL) cmTime
	msgSetTime func(obj objc.ID, sel objc.SEL, t cmTime)
	msgSeek    func(obj objc.ID, sel objc.SEL, t, before, after cmTime, done objc.Block)
	msgNumber  func(cls objc.ID, sel objc.SEL, v float64) objc.ID
	msgFromStr func(obj objc.ID, sel objc.SEL, b *byte, n uint, enc uint) objc.ID

	// runTask is the one C callback the main queue runs; its context is a
	// key into tasks, as purego callbacks are limited and never freed.
	runTask    uintptr
	taskMu     sync.Mutex
	tasks      = map[uintptr]func(){}
	lastTaskID uintptr
)

// cmTime mirrors Core Media's CMTime.
type cmTime struct {
	_         structs.HostLayout
	Value     int64
	Timescale int32
	Flags     uint32
	Epoch     int64
}

const (
	timeValid      = 1
	timeNonNumeric = 4 | 8 | 16 // infinite or indefinite
)

func seconds(t cmTime) float64 {
	if t.Flags&(timeValid|timeNonNumeric) != timeValid || t.Timescale <= 0 {
		return 0
	}
	return float64(t.Value) / float64(t.Timescale)
}

// timeAt has millisecond precision.
func timeAt(sec float64) cmTime {
	return cmTime{Value: int64(max(sec, 0) * 1000), Timescale: 1000, Flags: timeValid}
}

var loadAV = sync.OnceValue(func() error {
	const avFoundation = "/System/Library/Frameworks/AVFoundation.framework/AVFoundation"
	var err error
	if libAV, err = purego.Dlopen(avFoundation, purego.RTLD_LAZY|purego.RTLD_GLOBAL); err != nil {
		return err
	}
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
	for _, fn := range []any{&msgSetF32, &msgF64, &msgTime, &msgSetTime, &msgSeek, &msgNumber, &msgFromStr} {
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
	if objc.GetClass("AVPlayer") == 0 {
		return errors.New("audio: no AVPlayer")
	}
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

// later queues fn on the main thread, so system callbacks reach the app
// outside the engine's methods.
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

func goString(str objc.ID) string {
	if str == 0 {
		return ""
	}
	utf8 := send(str, "UTF8String")
	length := int(send(str, "lengthOfBytesUsingEncoding:", uint(4)))
	if utf8 == 0 || length == 0 {
		return ""
	}
	return string(unsafe.Slice(*(**byte)(unsafe.Pointer(&utf8)), length))
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

func nsError(nsErr objc.ID) error {
	if nsErr == 0 {
		return errors.New("audio: playback failed")
	}
	return errors.New(goString(send(nsErr, "localizedDescription")))
}
