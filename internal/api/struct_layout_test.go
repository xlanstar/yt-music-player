package api

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
		{name: "Frame", size: unsafe.Sizeof(Frame{}), want: 40},
		{name: "Hello", size: unsafe.Sizeof(Hello{}), want: 32},
		{name: "State", size: unsafe.Sizeof(State{}), want: 56},
		{name: "Status", size: unsafe.Sizeof(Status{}), want: 240},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.size != tc.want {
				t.Errorf("%d bytes, want %d; keep small fields together", tc.size, tc.want)
			}
		})
	}
}
