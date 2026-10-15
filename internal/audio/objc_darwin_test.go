package audio

import "testing"

func TestCMTimeSeconds(t *testing.T) {
	for _, c := range []struct {
		t    cmTime
		want float64
	}{
		{timeAt(12.345), 12.345},
		{timeAt(-1), 0},
		{cmTime{Value: 9397248, Timescale: 44100, Flags: timeValid}, 213.0895238095238},
		{cmTime{Flags: timeValid | 16}, 0},  // indefinite, as a live stream's
		{cmTime{Value: 5, Timescale: 1}, 0}, // not valid
	} {
		if got := seconds(c.t); got != c.want {
			t.Errorf("seconds(%+v) = %v, want %v", c.t, got, c.want)
		}
	}
}
