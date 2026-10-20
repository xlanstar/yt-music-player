package tui

import (
	"testing"
	"unsafe"
)

func TestBudgetLayouts(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("64-bit layouts")
	}
	for _, tc := range []struct {
		name       string
		size, want uintptr
	}{
		{name: "item", size: unsafe.Sizeof(item{}), want: 304},
		{name: "volumeMemo", size: unsafe.Sizeof(volumeMemo{}), want: 32},
		{name: "gfx", size: unsafe.Sizeof(gfx{}), want: 1624},
		{name: "model", size: unsafe.Sizeof(model{}), want: 2360},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.size != tc.want {
				t.Errorf("%d bytes, want %d; keep small fields together", tc.size, tc.want)
			}
		})
	}
}
