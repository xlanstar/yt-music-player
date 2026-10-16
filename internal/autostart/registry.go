package autostart

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
)

// hive is the part of HKEY_CURRENT_USER this package reads and writes;
// the real one is Windows', tests fake it. A value or key that is absent
// reads as fs.ErrNotExist.
type hive interface {
	getString(key, name string) (value string, kind uint32, err error)
	setString(key, name, value string, kind uint32) error
	deleteValue(key, name string) error
}

// The registry's string types, as Windows numbers them.
const (
	kindString       uint32 = 1 // REG_SZ
	kindExpandString uint32 = 2 // REG_EXPAND_SZ
)

const (
	runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`
	envKeyPath = `Environment`
	pathValue  = "Path"
)

// runKey is Windows' entry: a value under HKCU's Run key, which Explorer
// starts at the next login.
type runKey struct {
	reg hive
}

// enable quotes the path, or Windows would read a path with spaces as a
// program and its arguments.
func (r runKey) enable(ytmd string) error {
	if err := r.reg.setString(runKeyPath, RunValue, `"`+ytmd+`"`, kindString); err != nil {
		return fmt.Errorf("autostart: %w", err)
	}
	return nil
}

func (r runKey) disable() error {
	if err := r.reg.deleteValue(runKeyPath, RunValue); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("autostart: %w", err)
	}
	return nil
}

func (r runKey) status() (bool, string, error) {
	v, _, err := r.reg.getString(runKeyPath, RunValue)
	if errors.Is(err, fs.ErrNotExist) {
		return false, "", nil
	}
	if err != nil {
		return false, "", fmt.Errorf("autostart: %w", err)
	}
	if unquoted, ok := strings.CutPrefix(v, `"`); ok {
		v, _, _ = strings.Cut(unquoted, `"`)
	}
	return true, v, nil
}

func (r runKey) repair(ytmd string) error {
	v, _, err := r.reg.getString(runKeyPath, RunValue)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("autostart: %w", err)
	}
	if v == `"`+ytmd+`"` {
		return nil
	}
	return r.enable(ytmd)
}

// userPath is the user's Path in HKCU\Environment. Each change is announced
// to running programs (notify), or only programs started after the next
// login would see it.
type userPath struct {
	reg    hive
	notify func()
}

// EnablePath adds dir, an absolute directory, to the user's Path, unless it
// is there already. Windows only.
func EnablePath(dir string) error {
	return editPath(dir, func(list string) string { return addPathEntry(list, dir) })
}

// DisablePath removes dir from the user's Path, and nothing else. Windows
// only.
func DisablePath(dir string) error {
	return editPath(dir, func(list string) string { return removePathEntry(list, dir) })
}

// RepairPath moves the user's Path from old, the directory EnablePath was
// given before, to dir, at old's place in the list; the caller keeps old.
// With old not on the Path any more, it adds dir. Windows only.
func RepairPath(old, dir string) error {
	if err := checkPath(old); err != nil {
		return err
	}
	return editPath(dir, func(list string) string { return replacePathEntry(list, old, dir) })
}

// PathEnabled reports whether dir is on the user's Path. Windows only.
func PathEnabled(dir string) (bool, error) {
	if err := checkPathEntry(dir); err != nil {
		return false, err
	}
	p, err := currentPath()
	if err != nil {
		return false, err
	}
	return p.has(dir)
}

func editPath(dir string, edit func(list string) string) error {
	if err := checkPathEntry(dir); err != nil {
		return err
	}
	p, err := currentPath()
	if err != nil {
		return err
	}
	return p.edit(edit)
}

// checkPathEntry refuses a directory a Path cannot hold as one entry.
func checkPathEntry(dir string) error {
	if err := checkPath(dir); err != nil {
		return err
	}
	if strings.ContainsAny(dir, `;"`) {
		return fmt.Errorf("autostart: %q has a ; or \" a Path cannot hold", dir)
	}
	return nil
}

func (p userPath) has(dir string) (bool, error) {
	list, _, err := p.read()
	if err != nil {
		return false, err
	}
	return hasPathEntry(list, dir), nil
}

// read returns the Path and its type; an absent one is empty, of the type
// Windows gives a new user's.
func (p userPath) read() (string, uint32, error) {
	list, kind, err := p.reg.getString(envKeyPath, pathValue)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", kindExpandString, nil
	case err != nil:
		return "", 0, fmt.Errorf("autostart: %w", err)
	case kind != kindString && kind != kindExpandString:
		return "", 0, fmt.Errorf("autostart: the user's Path has registry type %d, not a string", kind)
	}
	return list, kind, nil
}

// edit rewrites the Path through edit, keeping its type: a REG_EXPAND_SZ
// Path written back as REG_SZ would stop expanding its %VARIABLES%.
func (p userPath) edit(edit func(list string) string) error {
	list, kind, err := p.read()
	if err != nil {
		return err
	}
	next := edit(list)
	if next == list {
		return nil
	}
	if next == "" {
		err = p.reg.deleteValue(envKeyPath, pathValue)
	} else {
		err = p.reg.setString(envKeyPath, pathValue, next, kind)
	}
	if err != nil {
		return fmt.Errorf("autostart: %w", err)
	}
	p.notify()
	return nil
}

// samePathEntry reports whether a Path entry names dir, as Windows reads
// them: case aside, quotes and trailing separators aside.
func samePathEntry(entry, dir string) bool {
	norm := func(s string) string {
		return strings.TrimRight(strings.Trim(strings.TrimSpace(s), `"`), `\/`)
	}
	e := norm(entry)
	return e != "" && strings.EqualFold(e, norm(dir))
}

func hasPathEntry(list, dir string) bool {
	for entry := range strings.SplitSeq(list, ";") {
		if samePathEntry(entry, dir) {
			return true
		}
	}
	return false
}

// addPathEntry appends dir to list unless it is there.
func addPathEntry(list, dir string) string {
	switch {
	case hasPathEntry(list, dir):
		return list
	case list == "" || strings.HasSuffix(list, ";"):
		return list + dir
	}
	return list + ";" + dir
}

// removePathEntry drops every entry that names dir, leaving the others as
// they are, empty ones too.
func removePathEntry(list, dir string) string {
	if !hasPathEntry(list, dir) {
		return list
	}
	var kept []string
	for entry := range strings.SplitSeq(list, ";") {
		if !samePathEntry(entry, dir) {
			kept = append(kept, entry)
		}
	}
	return strings.Join(kept, ";")
}

// replacePathEntry puts dir where old or dir first is, dropping the other
// entries of either; with neither there, it appends dir.
func replacePathEntry(list, old, dir string) string {
	if !hasPathEntry(list, old) {
		return addPathEntry(list, dir)
	}
	var kept []string
	isPlaced := false
	for entry := range strings.SplitSeq(list, ";") {
		if !samePathEntry(entry, old) && !samePathEntry(entry, dir) {
			kept = append(kept, entry)
			continue
		}
		if !isPlaced {
			kept = append(kept, dir)
			isPlaced = true
		}
	}
	return strings.Join(kept, ";")
}
