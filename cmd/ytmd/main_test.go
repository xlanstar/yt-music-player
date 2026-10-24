package main

import "testing"

func TestProcs(t *testing.T) {
	for _, tc := range []struct {
		name    string
		env     string
		current int
		want    int
	}{
		{"many cores", "", 16, maxProcs},
		{"one more", "", maxProcs + 1, maxProcs},
		{"as many", "", maxProcs, 0},
		{"fewer, never raised", "", 2, 0},
		{"environment wins", "16", 16, 0},
		{"environment lower", "2", 2, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := procs(tc.env, tc.current); got != tc.want {
				t.Errorf("procs(%q, %d) = %d, want %d", tc.env, tc.current, got, tc.want)
			}
		})
	}
}
