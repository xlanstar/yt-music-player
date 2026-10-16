package tui

// listState is a list's cursor and how far it scrolled, both in items.
type listState struct {
	cursor, offset int
}

// moveBy moves the cursor by delta of n items, past those not
// selectable (headings), and stops at the ends.
func (l *listState) moveBy(delta, n int, selectable func(int) bool) {
	if n == 0 {
		return
	}
	step := 1
	if delta < 0 {
		step = -1
	}
	to := min(max(l.cursor+delta, 0), n-1)
	for i := to; i >= 0 && i < n; i += step {
		if selectable(i) {
			l.cursor = i
			return
		}
	}
	// Nothing selectable that way: try back toward the cursor.
	for i := to; i >= 0 && i < n; i -= step {
		if selectable(i) {
			l.cursor = i
			return
		}
	}
}

// fit keeps the cursor on one of n items, a selectable one if it can,
// and scrolls the list so that it shows in rows lines.
func (l *listState) fit(n, rows int, selectable func(int) bool) {
	if n == 0 {
		*l = listState{}
		return
	}
	l.cursor = min(max(l.cursor, 0), n-1)
	if !selectable(l.cursor) {
		l.moveBy(1, n, selectable)
		if !selectable(l.cursor) {
			l.moveBy(-1, n, selectable)
		}
	}
	rows = max(rows, 1)
	if l.cursor < l.offset {
		l.offset = l.cursor
	}
	if l.cursor >= l.offset+rows {
		l.offset = l.cursor - rows + 1
	}
	l.offset = min(l.offset, max(n-rows, 0))
	// A heading above the first item shows with it.
	if l.offset == 1 && l.cursor < rows {
		l.offset = 0
	}
	l.offset = max(l.offset, 0)
}
