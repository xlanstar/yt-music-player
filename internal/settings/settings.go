// Package settings keeps the player's preferences between runs as JSON in
// the user's config directory, beside the user's own themes.
// The app hands it the directory, so tests can use a temporary one.
package settings

import (
	"bytes"
	"cmp"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"yt-music-player/internal/theme"
)

// Settings are the preferences kept. The zero value is the defaults.
type Settings struct {
	// Theme is a preset's ID or a user theme's file name ("dusk.json"):
	// the extension tells them apart. Empty is Default.
	Theme string `json:"theme,omitzero"`
	// Layout overrides the theme's layout.
	Layout theme.Arrangement `json:"layout,omitzero"`
	// TerminalPath is the folder the app put on the user's Path for ytm
	// (Windows), kept to move the entry when the app moves.
	TerminalPath string `json:"terminalPath,omitzero"`
}

const (
	fileName  = "settings.json"
	themesDir = "themes"
	themeExt  = ".json"
	// maxThemes bounds the themes listed, since each is read in full.
	maxThemes = 100
)

// Dir keeps the settings and, in its themes directory, the user's themes.
// An empty Dir keeps nothing.
type Dir string

// DefaultDir is the player's directory in the user's config.
func DefaultDir() (Dir, error) {
	dir, err := os.UserConfigDir()
	return Dir(filepath.Join(dir, "yt-music-player")), err
}

// ThemesDir is where the user's themes are.
func (d Dir) ThemesDir() string { return filepath.Join(string(d), themesDir) }

// Load returns the saved settings, or the defaults without a file. An
// unreadable file gives the defaults too, with the error, so the player
// always starts.
func (d Dir) Load() (Settings, error) {
	if d == "" {
		return Settings{}, nil
	}
	data, err := os.ReadFile(filepath.Join(string(d), fileName))
	if errors.Is(err, fs.ErrNotExist) {
		return Settings{}, nil
	}
	if err != nil {
		return Settings{}, err
	}
	// Unknown fields are a later version's, not mistakes.
	var prefs Settings
	if err := json.Unmarshal(data, &prefs); err != nil {
		return Settings{}, fmt.Errorf("%s: %w", fileName, err)
	}
	return prefs, nil
}

// Save writes prefs whole or not at all: a temporary file renamed over the
// old one, so a crash midway leaves the old settings. Only the user may
// read either.
func (d Dir) Save(prefs Settings) error {
	if d == "" {
		return nil
	}
	data, err := json.Marshal(prefs, jsontext.WithIndent("  "))
	if err != nil {
		return err
	}
	return replaceFile(string(d), fileName, append(data, '\n'))
}

// replaceFile relies on CreateTemp making the file 0600.
func replaceFile(dir, name string, data []byte) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+name+"-*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Sync()
	}
	err = cmp.Or(err, tmp.Close())
	if err == nil {
		err = os.Rename(tmp.Name(), filepath.Join(dir, name))
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
	}
	return err
}

// UserTheme is a theme file of the user's.
type UserTheme struct {
	// File is its file name, as Settings.Theme names it.
	File string
	// Name is the name it gives itself, else its file name's stem.
	Name  string
	Theme theme.Theme
	Err   error
}

// Themes reads the user's themes, sorted by file name. A theme that does
// not parse carries its Err instead of failing the others.
func (d Dir) Themes() ([]UserTheme, error) {
	if d == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(d.ThemesDir())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// Sized at once: a UserTheme holds a whole Theme, which growing would
	// copy. One buffer reads every file, grown to the largest.
	themes := make([]UserTheme, 0, min(len(entries), maxThemes))
	dir := d.ThemesDir()
	var buf bytes.Buffer
	for _, entry := range entries {
		file := entry.Name()
		isHidden := strings.HasPrefix(file, ".")
		if entry.IsDir() || isHidden || !isUserTheme(file) {
			continue
		}
		if len(themes) == maxThemes {
			break
		}
		name, t, err := readTheme(filepath.Join(dir, file), &buf)
		themes = append(themes, UserTheme{
			File:  file,
			Name:  cmp.Or(name, strings.TrimSuffix(file, filepath.Ext(file))),
			Theme: t,
			Err:   err,
		})
	}
	return themes, nil
}

// readTheme reads the theme at path through buf, whose bytes Parse
// copies what it keeps of.
func readTheme(path string, buf *bytes.Buffer) (string, theme.Theme, error) {
	f, err := os.Open(path) //nolint:gosec // a file listed in ThemesDir
	if err != nil {
		return "", theme.Theme{}, err
	}
	buf.Reset()
	// One byte past the limit lets Parse tell the file is too large.
	_, err = buf.ReadFrom(io.LimitReader(f, theme.MaxFileSize+1))
	_ = f.Close()
	if err != nil {
		return "", theme.Theme{}, err
	}
	name, parsed, err := theme.Parse(buf.Bytes())
	if err != nil {
		// The file name shown beside the error already says it is a theme.
		err = errors.New(strings.TrimPrefix(err.Error(), "theme: "))
	}
	return name, parsed, err
}

// isUserTheme reports whether id, a Settings.Theme, names a user's file
// rather than a preset.
func isUserTheme(id string) bool { return strings.EqualFold(filepath.Ext(id), themeExt) }

// Resolve returns the theme prefs chooses among the presets and users,
// with prefs' layout over it. themeErr reports a missing or broken theme
// (Default is used instead); layoutErr an unreadable arrangement (the
// theme's own layout is used instead).
func Resolve(prefs Settings, users []UserTheme) (t theme.Theme, themeErr, layoutErr error) {
	if t, themeErr = find(cmp.Or(prefs.Theme, theme.DefaultID), users); themeErr != nil {
		t = theme.Default()
	}
	layout, err := prefs.Layout.Apply(t.Layout)
	if err != nil {
		layout, layoutErr = t.Layout.Normalized(), fmt.Errorf("layout: %w", err)
	}
	t.Layout = layout
	return t, themeErr, layoutErr
}

func find(id string, users []UserTheme) (theme.Theme, error) {
	if !isUserTheme(id) {
		preset, ok := theme.PresetByID(id)
		if !ok {
			return theme.Theme{}, errors.New("no such preset")
		}
		return preset.Theme(), nil
	}
	i := slices.IndexFunc(users, func(u UserTheme) bool { return u.File == id })
	if i < 0 {
		return theme.Theme{}, errors.New("no such theme file")
	}
	return users[i].Theme, users[i].Err
}

// Saver saves settings off the UI thread, one write at a time, latest
// last: a newer Save replaces a waiting one, so quick changes cost at most
// one write each.
type Saver struct {
	dir Dir
	// failed runs on the saving goroutine.
	failed func(error)

	mu        sync.Mutex
	pending   *Settings
	isRunning bool
	idle      sync.WaitGroup
}

// NewSaver returns a Saver writing to dir. failed may be nil for an empty
// dir, which never fails.
func NewSaver(dir Dir, failed func(error)) *Saver { return &Saver{dir: dir, failed: failed} }

// Save writes prefs soon, on a goroutine.
func (s *Saver) Save(prefs Settings) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = &prefs
	if s.isRunning {
		return
	}
	s.isRunning = true
	s.idle.Go(s.write)
}

func (s *Saver) write() {
	for {
		s.mu.Lock()
		next := s.pending
		s.pending, s.isRunning = nil, next != nil
		s.mu.Unlock()
		if next == nil {
			return
		}
		if err := s.dir.Save(*next); err != nil {
			s.failed(err)
		}
	}
}

// Wait returns once every Save so far is written, for when the window
// closes or a test reads them.
func (s *Saver) Wait() { s.idle.Wait() }
