package autostart

import (
	"errors"
	"fmt"
	"unsafe"

	"github.com/rs/zerolog/log"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func current() (item, error) { return runKey{reg: hkcu{}}, nil }

func currentPath() (userPath, error) {
	return userPath{reg: hkcu{}, notify: broadcastEnvironment}, nil
}

// hkcu is HKEY_CURRENT_USER.
type hkcu struct{}

func (hkcu) getString(key, name string) (string, uint32, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, key, registry.QUERY_VALUE)
	if err != nil {
		return "", 0, err
	}
	v, kind, err := k.GetStringValue(name)
	return v, kind, errors.Join(err, k.Close())
}

func (hkcu) setString(key, name, value string, kind uint32) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, key, registry.SET_VALUE)
	if err != nil {
		return err
	}
	if kind == kindExpandString {
		err = k.SetExpandStringValue(name, value)
	} else {
		err = k.SetStringValue(name, value)
	}
	return errors.Join(err, k.Close())
}

func (hkcu) deleteValue(key, name string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, key, registry.SET_VALUE)
	if err != nil {
		return err
	}
	return errors.Join(k.DeleteValue(name), k.Close())
}

var sendMessageTimeout = windows.NewLazySystemDLL("user32.dll").NewProc("SendMessageTimeoutW")

const (
	hwndBroadcast      = 0xffff
	wmSettingChange    = 0x001a
	smtoAbortIfHung    = 0x0002
	broadcastTimeoutMs = 5000
)

// broadcastEnvironment tells running programs, Explorer above all, that the
// environment changed, so the terminals it starts next see the new Path.
// The Path is written by then: a failure only delays that to the next
// login, so it is logged, not returned.
func broadcastEnvironment() {
	env, err := windows.UTF16PtrFromString("Environment")
	if err != nil {
		return
	}
	var result uintptr
	ok, _, err := sendMessageTimeout.Call(
		hwndBroadcast,
		wmSettingChange,
		0,
		uintptr(unsafe.Pointer(env)), //nolint:gosec // G103: the call takes the string's address
		smtoAbortIfHung,
		broadcastTimeoutMs,
		uintptr(unsafe.Pointer(&result)), //nolint:gosec // G103: the call writes the result there
	)
	if ok == 0 {
		log.Debug().Err(fmt.Errorf("autostart: broadcast WM_SETTINGCHANGE: %w", err)).Msg("path change not announced")
	}
}
