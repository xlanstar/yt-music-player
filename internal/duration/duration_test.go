package duration

import "testing"

func TestClock(t *testing.T) {
	for in, want := range map[float64]string{0: "0:00", -3: "0:00", 9.9: "0:09", 81.5: "1:21", 600: "10:00", 3600: "1:00:00", 36061: "10:01:01"} {
		if got := Clock(in); got != want {
			t.Errorf("Clock(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestMinutes(t *testing.T) {
	for in, want := range map[int]int{0: 0, 29: 0, 30: 1, 89: 1, 90: 2, 4512: 75} {
		if got := Minutes(in); got != want {
			t.Errorf("Minutes(%d) = %d, want %d", in, got, want)
		}
	}
}
