package main

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
		{name: "phrase", size: unsafe.Sizeof(phrase{}), want: 104},
		{name: "songRowOpts", size: unsafe.Sizeof(songRowOpts{}), want: 24},
		{name: "startupState", size: unsafe.Sizeof(startupState{}), want: 128},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.size != tc.want {
				t.Errorf("%d bytes, want %d; keep small fields together", tc.size, tc.want)
			}
		})
	}
}
