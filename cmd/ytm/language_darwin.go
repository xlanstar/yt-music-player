package main

import (
	"bytes"
	"fmt"

	"github.com/ebitengine/purego"
	"github.com/rs/zerolog/log"
)

// systemLanguages returns the user's preferred languages, in the order of
// System Settings (CFLocaleCopyPreferredLanguages), through purego to keep
// the build free of cgo.
func systemLanguages() (langs []string) {
	// RegisterLibFunc panics on a missing symbol; the variables decide.
	defer func() {
		if r := recover(); r != nil {
			log.Debug().Str("panic", fmt.Sprint(r)).Msg("ytm: preferred languages")
			langs = nil
		}
	}()
	cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	if err != nil {
		log.Debug().Err(err).Msg("ytm: preferred languages")
		return nil
	}
	var (
		copyPreferred func() uintptr
		count         func(array uintptr) int
		valueAt       func(array uintptr, i int) uintptr
		cString       func(s uintptr, buf []byte, size int, encoding uint32) bool
		release       func(ref uintptr)
	)
	purego.RegisterLibFunc(&copyPreferred, cf, "CFLocaleCopyPreferredLanguages")
	purego.RegisterLibFunc(&count, cf, "CFArrayGetCount")
	purego.RegisterLibFunc(&valueAt, cf, "CFArrayGetValueAtIndex")
	purego.RegisterLibFunc(&cString, cf, "CFStringGetCString")
	purego.RegisterLibFunc(&release, cf, "CFRelease")

	const utf8 = 0x08000100 // kCFStringEncodingUTF8
	array := copyPreferred()
	if array == 0 {
		return nil
	}
	defer release(array)
	n := count(array)
	langs = make([]string, 0, n)
	// Tags are short: "zh-Hant-TW", "sr-Latn-RS".
	buf := make([]byte, 64)
	for i := range n {
		if !cString(valueAt(array, i), buf, len(buf), utf8) {
			continue
		}
		if end := bytes.IndexByte(buf, 0); end > 0 {
			langs = append(langs, string(buf[:end]))
		}
	}
	return langs
}
