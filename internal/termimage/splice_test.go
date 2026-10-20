package termimage

import "testing"

func TestMark(t *testing.T) {
	if got, want := Mark(42, 1, 0), "\x1b^ytm;42;1;0\x1b\\"; got != want {
		t.Errorf("Mark = %q, want %q", got, want)
	}
	if gen, ok := FrameGen([]byte("ab\x1b^ytm;x\x1b\\" + Mark(7, 0, 1) + Mark(8, 1, 0))); !ok || gen != 7 {
		t.Errorf("FrameGen = %d, %t; want the first mark's 7", gen, ok)
	}
	if _, ok := FrameGen([]byte("\x1b[?2026h\x1b^other\x1b\\text\x1b[?2026l")); ok {
		t.Error("FrameGen found a mark in a frame without")
	}
}

func TestNewTable(t *testing.T) {
	places := []Placement{{Seq: "A", X: 1, Y: 2, Cols: 4, Rows: 2}}
	tab := NewTable(3, places)
	places[0].Seq = "B"
	if tab.Gen() != 3 || tab.places[0].Seq != "A" {
		t.Errorf("table = %+v, want gen 3 kept apart from its slice", tab)
	}
	defer func() {
		if recover() == nil {
			t.Error("a table of 65 slots made")
		}
	}()
	NewTable(1, make([]Placement, maxSlots+1))
}

func TestSplice(t *testing.T) {
	tab := NewTable(5, []Placement{
		{Seq: "BAR", X: 1, Y: 21, Cols: 4, Rows: 2},
		{Seq: "HEAD", X: 1, Y: 2, Cols: 4, Rows: 2},
		{},
	})
	bar := "\x1b7\x1b[22;2HBAR\x1b8"
	head := "\x1b7\x1b[3;2HHEAD\x1b8"
	tests := []struct {
		name, frame string
		t           *Table
		w, h        int
		want        string
	}{
		{"no marks", "\x1b[Hhello", tab, 80, 24, "\x1b[Hhello"},
		{"at the end", "a" + Mark(5, 0, 0) + "⠀b", tab, 80, 24, "a⠀b" + bar},
		{
			"before the synchronized output's end",
			"\x1b[?2026h" + Mark(5, 0, 1) + "⠀\x1b[?2026l", tab, 80, 24,
			"\x1b[?2026h⠀" + bar + "\x1b[?2026l",
		},
		{
			"before showing the cursor",
			Mark(5, 1, 0) + "⠀x\x1b[?25h", tab, 80, 24,
			"⠀x" + head + "\x1b[?25h",
		},
		{
			"a slot once, in slot order",
			Mark(5, 1, 0) + "⠀" + Mark(5, 0, 0) + "⠀" + Mark(5, 1, 1) + "⠀\x1b[?25h\x1b[?2026l", tab, 80, 24,
			"⠀⠀⠀\x1b[?25h" + bar + head + "\x1b[?2026l",
		},
		{"another generation", Mark(4, 0, 0) + "⠀", tab, 80, 24, "⠀"},
		{"no table", Mark(5, 0, 0) + "⠀", nil, 80, 24, "⠀"},
		{"an empty slot", Mark(5, 2, 0) + "⠀", tab, 80, 24, "⠀"},
		{"a slot past the table", Mark(5, 9, 0) + "⠀", tab, 80, 24, "⠀"},
		{"too narrow", Mark(5, 0, 0) + "⠀", tab, 4, 24, "⠀"},
		{"on the last line", Mark(5, 0, 0) + "⠀", tab, 80, 23, "⠀"},
		{"below the screen", Mark(5, 0, 0) + "⠀", tab, 80, 10, "⠀"},
		{"just fits", Mark(5, 0, 0) + "⠀", tab, 5, 24, "⠀" + bar},
		{"other PMs", "\x1b^ytm;x\x1b\\\x1b^ytm;1;2\x1b\\" + Mark(5, 0, 0), tab, 80, 24, "\x1b^ytm;x\x1b\\\x1b^ytm;1;2\x1b\\" + bar},
		{"a slot too large to keep", "\x1b^ytm;5;64;0\x1b\\", tab, 80, 24, "\x1b^ytm;5;64;0\x1b\\"},
		{"a mark cut short", "a\x1b^ytm;5;0;0", tab, 80, 24, "a\x1b^ytm;5;0;0"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prefix := []byte("kept")
			got := Splice(prefix, []byte(tc.frame), tc.t, tc.w, tc.h)
			if want := "kept" + tc.want; string(got) != want {
				t.Errorf("Splice = %q, want %q", got, want)
			}
		})
	}
}

func TestAppendOverlay(t *testing.T) {
	if got, want := string(appendOverlay(nil, "IMG", 1, 20)), "\x1b7\x1b[21;2HIMG\x1b8"; got != want {
		t.Errorf("appendOverlay = %q, want %q", got, want)
	}
}
