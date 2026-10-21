//go:build darwin

package auth

import (
	"errors"
	"fmt"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// The Keychain is reached through Security.framework with purego to keep
// the build free of cgo; the security CLI would take the secret on its
// command line, where any process can read it.

// NewStore returns the platform's store: the login Keychain on macOS, a
// file only the user can read elsewhere.
func NewStore() (Store, error) {
	if err := loadSecurity(); err != nil {
		return nil, err
	}
	return &keychainStore{service: appName, account: "YouTube Music"}, nil
}

// keychainStore keeps credentials as a generic password in the login Keychain.
type keychainStore struct {
	service, account string
}

// OSStatus codes, under Security.framework's names.
const (
	errSecSuccess               = 0
	errSecUserCanceled          = -128
	errSecAuthFailed            = -25293
	errSecItemNotFound          = -25300
	errSecInteractionNotAllowed = -25308
)

const kCFStringEncodingUTF8 = 0x08000100

var (
	cfStringCreateWithCString func(alloc uintptr, s string, encoding uint32) uintptr
	cfDataCreate              func(alloc uintptr, b []byte, n int) uintptr
	cfDataGetLength           func(data uintptr) int
	cfDataGetBytePtr          func(data uintptr) uintptr
	cfDictionaryCreate        func(alloc uintptr, keys, values []uintptr, n int, keyCallbacks, valueCallbacks uintptr) uintptr
	cfRelease                 func(ref uintptr)

	secItemAdd          func(attributes, result uintptr) int32
	secItemCopyMatching func(query uintptr, result *uintptr) int32
	secItemUpdate       func(query, attributes uintptr) int32
	secItemDelete       func(query uintptr) int32

	// Read from the frameworks' globals at load.
	kCFBooleanTrue, kSecClass, kSecClassGenericPassword, kSecAttrService, kSecAttrAccount,
	kSecAttrLabel, kSecValueData, kSecReturnData, kSecMatchLimit, kSecMatchLimitOne uintptr
	// Addresses of structs, not CFTypeRefs.
	kCFTypeDictionaryKeyCallBacks, kCFTypeDictionaryValueCallBacks uintptr
)

var loadSecurity = sync.OnceValue(func() (err error) {
	// RegisterLibFunc panics on a missing symbol; report it instead.
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("auth: keychain: %v", r)
		}
	}()
	const mode = purego.RTLD_LAZY | purego.RTLD_GLOBAL
	cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", mode)
	if err != nil {
		return fmt.Errorf("auth: keychain: %w", err)
	}
	sec, err := purego.Dlopen("/System/Library/Frameworks/Security.framework/Security", mode)
	if err != nil {
		return fmt.Errorf("auth: keychain: %w", err)
	}
	purego.RegisterLibFunc(&cfStringCreateWithCString, cf, "CFStringCreateWithCString")
	purego.RegisterLibFunc(&cfDataCreate, cf, "CFDataCreate")
	purego.RegisterLibFunc(&cfDataGetLength, cf, "CFDataGetLength")
	purego.RegisterLibFunc(&cfDataGetBytePtr, cf, "CFDataGetBytePtr")
	purego.RegisterLibFunc(&cfDictionaryCreate, cf, "CFDictionaryCreate")
	purego.RegisterLibFunc(&cfRelease, cf, "CFRelease")
	purego.RegisterLibFunc(&secItemAdd, sec, "SecItemAdd")
	purego.RegisterLibFunc(&secItemCopyMatching, sec, "SecItemCopyMatching")
	purego.RegisterLibFunc(&secItemUpdate, sec, "SecItemUpdate")
	purego.RegisterLibFunc(&secItemDelete, sec, "SecItemDelete")

	for _, global := range []struct {
		lib   uintptr
		name  string
		out   *uintptr
		deref bool
	}{
		{lib: cf, name: "kCFBooleanTrue", out: &kCFBooleanTrue, deref: true},
		{lib: cf, name: "kCFTypeDictionaryKeyCallBacks", out: &kCFTypeDictionaryKeyCallBacks, deref: false},
		{lib: cf, name: "kCFTypeDictionaryValueCallBacks", out: &kCFTypeDictionaryValueCallBacks, deref: false},
		{lib: sec, name: "kSecClass", out: &kSecClass, deref: true},
		{lib: sec, name: "kSecClassGenericPassword", out: &kSecClassGenericPassword, deref: true},
		{lib: sec, name: "kSecAttrService", out: &kSecAttrService, deref: true},
		{lib: sec, name: "kSecAttrAccount", out: &kSecAttrAccount, deref: true},
		{lib: sec, name: "kSecAttrLabel", out: &kSecAttrLabel, deref: true},
		{lib: sec, name: "kSecValueData", out: &kSecValueData, deref: true},
		{lib: sec, name: "kSecReturnData", out: &kSecReturnData, deref: true},
		{lib: sec, name: "kSecMatchLimit", out: &kSecMatchLimit, deref: true},
		{lib: sec, name: "kSecMatchLimitOne", out: &kSecMatchLimitOne, deref: true},
	} {
		addr, err := purego.Dlsym(global.lib, global.name)
		if err != nil || addr == 0 {
			return fmt.Errorf("auth: keychain: no %s", global.name)
		}
		if global.deref {
			// The symbol is a variable holding the CFTypeRef; the double
			// indirection keeps vet from flagging a uintptr conversion.
			addr = **(**uintptr)(unsafe.Pointer(&addr))
		}
		*global.out = addr
	}
	return nil
})

// cfDict builds a CF dictionary; the caller releases it. The strings and
// data it creates are released at build, since the dictionary retains them.
type cfDict struct {
	keys, values []uintptr
	owned        []uintptr
}

func (d *cfDict) set(key, value uintptr) {
	d.keys = append(d.keys, key)
	d.values = append(d.values, value)
}

func (d *cfDict) setString(key uintptr, s string) {
	ref := cfStringCreateWithCString(0, s, kCFStringEncodingUTF8)
	d.owned = append(d.owned, ref)
	d.set(key, ref)
}

func (d *cfDict) setData(key uintptr, b []byte) {
	ref := cfDataCreate(0, b, len(b))
	d.owned = append(d.owned, ref)
	d.set(key, ref)
}

func (d *cfDict) build() uintptr {
	dict := cfDictionaryCreate(
		0,
		d.keys,
		d.values,
		len(d.keys),
		kCFTypeDictionaryKeyCallBacks,
		kCFTypeDictionaryValueCallBacks,
	)
	for _, ref := range d.owned {
		if ref != 0 {
			cfRelease(ref)
		}
	}
	d.owned = nil
	return dict
}

func (k *keychainStore) query() *cfDict {
	d := &cfDict{}
	d.set(kSecClass, kSecClassGenericPassword)
	d.setString(kSecAttrService, k.service)
	d.setString(kSecAttrAccount, k.account)
	return d
}

// withDict builds d, passes it to f and releases it after; -1 when CF
// could not build it.
func withDict(d *cfDict, f func(dict uintptr) int32) int32 {
	dict := d.build()
	if dict == 0 {
		return -1
	}
	defer cfRelease(dict)
	return f(dict)
}

func (k *keychainStore) Load() (Credentials, error) {
	if err := loadSecurity(); err != nil {
		return Credentials{}, err
	}
	query := k.query()
	query.set(kSecReturnData, kCFBooleanTrue)
	query.set(kSecMatchLimit, kSecMatchLimitOne)
	var data uintptr
	status := withDict(query, func(dict uintptr) int32 { return secItemCopyMatching(dict, &data) })
	if status == errSecItemNotFound {
		return Credentials{}, ErrNotStored
	}
	if status != errSecSuccess {
		return Credentials{}, statusError("load", status)
	}
	if data == 0 {
		return Credentials{}, ErrNotStored
	}
	defer cfRelease(data)
	size := cfDataGetLength(data)
	ptr := cfDataGetBytePtr(data)
	if size <= 0 || ptr == 0 {
		return Credentials{}, ErrNotStored
	}
	secret := string(unsafe.Slice(*(**byte)(unsafe.Pointer(&ptr)), size))
	c, err := Parse(secret)
	if err != nil {
		return Credentials{}, fmt.Errorf("auth: keychain: stored item: %w", err)
	}
	return c, nil
}

func (k *keychainStore) Save(c Credentials) error {
	if c.IsZero() {
		return k.Clear()
	}
	if err := loadSecurity(); err != nil {
		return err
	}
	secret := []byte(c.Secret())
	defer clear(secret)
	update := &cfDict{}
	update.setData(kSecValueData, secret)
	status := withDict(k.query(), func(query uintptr) int32 {
		return withDict(update, func(attrs uintptr) int32 { return secItemUpdate(query, attrs) })
	})
	if status == errSecItemNotFound {
		add := k.query()
		add.setString(kSecAttrLabel, "YT Music Player: YouTube Music sign-in")
		add.setData(kSecValueData, secret)
		status = withDict(add, func(attrs uintptr) int32 { return secItemAdd(attrs, 0) })
	}
	if status != errSecSuccess {
		return statusError("save", status)
	}
	return nil
}

func (k *keychainStore) Clear() error {
	if err := loadSecurity(); err != nil {
		return err
	}
	status := withDict(k.query(), secItemDelete)
	if status != errSecSuccess && status != errSecItemNotFound {
		return statusError("clear", status)
	}
	return nil
}

var errKeychain = errors.New("auth: keychain")

func statusError(op string, status int32) error {
	var why string
	switch status {
	case errSecUserCanceled:
		why = " (the user canceled)"
	case errSecAuthFailed:
		why = " (authorization failed)"
	case errSecInteractionNotAllowed:
		why = " (the Keychain is locked and cannot prompt)"
	}
	return fmt.Errorf("%w: %s: OSStatus %d%s", errKeychain, op, status, why)
}
