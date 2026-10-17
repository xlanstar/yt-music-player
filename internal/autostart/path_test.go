package autostart

import (
	"errors"
	"testing"
)

const installDir = `C:\Users\me\AppData\Local\Programs\YT Music Player`

func TestPathEntries(t *testing.T) {
	for _, tc := range []struct {
		name, list    string
		has           bool
		added, remove string
	}{
		{name: "empty", list: "", added: installDir, remove: ""},
		{
			name:   "others",
			list:   `C:\Windows;%USERPROFILE%\bin`,
			added:  `C:\Windows;%USERPROFILE%\bin;` + installDir,
			remove: `C:\Windows;%USERPROFILE%\bin`,
		},
		{
			name:   "trailing separator",
			list:   `C:\Windows;`,
			added:  `C:\Windows;` + installDir,
			remove: `C:\Windows;`,
		},
		{
			name:   "ours alone",
			list:   installDir,
			has:    true,
			added:  installDir,
			remove: "",
		},
		{
			name:   "ours in the middle",
			list:   `C:\a;` + installDir + `;C:\b`,
			has:    true,
			added:  `C:\a;` + installDir + `;C:\b`,
			remove: `C:\a;C:\b`,
		},
		{
			name:   "ours in another case with a backslash",
			list:   `C:\a;c:\users\ME\appdata\local\programs\yt music player\;C:\b`,
			has:    true,
			added:  `C:\a;c:\users\ME\appdata\local\programs\yt music player\;C:\b`,
			remove: `C:\a;C:\b`,
		},
		{
			name:   "ours quoted, twice",
			list:   `"` + installDir + `";C:\a;` + installDir,
			has:    true,
			added:  `"` + installDir + `";C:\a;` + installDir,
			remove: `C:\a`,
		},
		{
			name:   "a longer path that starts like ours",
			list:   installDir + `\bin;` + installDir + ` Beta`,
			added:  installDir + `\bin;` + installDir + ` Beta;` + installDir,
			remove: installDir + `\bin;` + installDir + ` Beta`,
		},
		{
			name:   "empty entries stay",
			list:   `C:\a;;` + installDir + `;`,
			has:    true,
			added:  `C:\a;;` + installDir + `;`,
			remove: `C:\a;;`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasPathEntry(tc.list, installDir); got != tc.has {
				t.Errorf("has = %v, want %v", got, tc.has)
			}
			if got := addPathEntry(tc.list, installDir); got != tc.added {
				t.Errorf("add = %q, want %q", got, tc.added)
			}
			if got := removePathEntry(tc.list, installDir); got != tc.remove {
				t.Errorf("remove = %q, want %q", got, tc.remove)
			}
			if got := addPathEntry(addPathEntry(tc.list, installDir), installDir); got != tc.added {
				t.Errorf("adding twice = %q, want %q", got, tc.added)
			}
		})
	}
}

func TestReplacePathEntry(t *testing.T) {
	old, dir := `C:\Old\YT Music Player`, `D:\New\YT Music Player`
	for _, tc := range []struct{ name, list, want string }{
		{"moved in place", `C:\a;` + old + `;C:\b`, `C:\a;` + dir + `;C:\b`},
		{"old gone", `C:\a`, `C:\a;` + dir},
		{"both there", `C:\a;` + dir + `;` + old, `C:\a;` + dir},
		{"old twice", old + `;C:\a;` + old + `\`, dir + `;C:\a`},
		{"empty", "", dir},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := replacePathEntry(tc.list, old, dir); got != tc.want {
				t.Errorf("replace = %q, want %q", got, tc.want)
			}
		})
	}
	if got := replacePathEntry(`C:\a;`+dir, dir, dir); got != `C:\a;`+dir {
		t.Errorf("replace with itself = %q", got)
	}
}

// pathOf returns the user's Path over h, counting announcements.
func pathOf(h *fakeHive, notified *int) userPath {
	return userPath{reg: h, notify: func() { *notified++ }}
}

func TestUserPathKeepsType(t *testing.T) {
	for _, kind := range []uint32{kindString, kindExpandString} {
		h := newFakeHive()
		h.values[envKeyPath+`\`+pathValue] = fakeValue{data: `%USERPROFILE%\bin`, kind: kind}
		var notified int
		p := pathOf(h, &notified)

		if err := p.edit(func(l string) string { return addPathEntry(l, installDir) }); err != nil {
			t.Fatal(err)
		}
		if got, want := h.path(), (fakeValue{data: `%USERPROFILE%\bin;` + installDir, kind: kind}); got != want {
			t.Errorf("Path = %+v, want %+v", got, want)
		}
		if err := p.edit(func(l string) string { return removePathEntry(l, installDir) }); err != nil {
			t.Fatal(err)
		}
		if got, want := h.path(), (fakeValue{data: `%USERPROFILE%\bin`, kind: kind}); got != want {
			t.Errorf("Path = %+v, want %+v", got, want)
		}
		if notified != 2 {
			t.Errorf("announced %d changes, want 2", notified)
		}
	}
}

func TestUserPathAbsent(t *testing.T) {
	h := newFakeHive()
	var notified int
	p := pathOf(h, &notified)

	// A new Path takes the type Windows gives one.
	if err := p.edit(func(l string) string { return addPathEntry(l, installDir) }); err != nil {
		t.Fatal(err)
	}
	if got, want := h.path(), (fakeValue{data: installDir, kind: kindExpandString}); got != want {
		t.Errorf("Path = %+v, want %+v", got, want)
	}

	// Removing the only entry removes the value.
	if err := p.edit(func(l string) string { return removePathEntry(l, installDir) }); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.values[envKeyPath+`\`+pathValue]; ok {
		t.Errorf("Path left as %+v", h.path())
	}
	if notified != 2 {
		t.Errorf("announced %d changes, want 2", notified)
	}
}

func TestUserPathUnchanged(t *testing.T) {
	h := newFakeHive()
	h.values[envKeyPath+`\`+pathValue] = fakeValue{data: `C:\a;` + installDir, kind: kindExpandString}
	var notified int
	p := pathOf(h, &notified)
	for _, edit := range []func(string) string{
		func(l string) string { return addPathEntry(l, installDir+`\`) },
		func(l string) string { return removePathEntry(l, `C:\Other`) },
		func(l string) string { return replacePathEntry(l, installDir, installDir) },
	} {
		if err := p.edit(edit); err != nil {
			t.Fatal(err)
		}
	}
	if h.writes != 0 || notified != 0 {
		t.Errorf("%d writes and %d announcements of no change", h.writes, notified)
	}
}

func TestUserPathErrors(t *testing.T) {
	h := newFakeHive()
	h.values[envKeyPath+`\`+pathValue] = fakeValue{data: "x", kind: 7}
	var notified int
	p := pathOf(h, &notified)
	if err := p.edit(func(l string) string { return addPathEntry(l, installDir) }); err == nil {
		t.Error("edited a Path that is not a string")
	}
	errRead := errors.New("access denied")
	h.err = errRead
	if err := p.edit(func(l string) string { return addPathEntry(l, installDir) }); !errors.Is(err, errRead) {
		t.Errorf("edit = %v, want the registry's error", err)
	}
	if h.writes != 0 || notified != 0 {
		t.Errorf("%d writes and %d announcements after errors", h.writes, notified)
	}
}
