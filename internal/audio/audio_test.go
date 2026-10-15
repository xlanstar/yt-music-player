package audio

import (
	"errors"
	"testing"
)

func TestFakeNotesAndReports(t *testing.T) {
	var got []Event
	f := NewFake(func(e Event) { got = append(got, e) })
	f.Load(Media{URL: "u"}, 12.5)
	f.Play()
	f.Emit(Ready)
	f.Fail(errors.New("x"))
	f.Command(CommandSeek, 3)
	if calls := f.TakeCalls(); len(calls) != 2 || calls[0] != "load u at 12.5" || calls[1] != "play" {
		t.Errorf("calls %q", calls)
	}
	if len(got) != 3 {
		t.Fatalf("events %+v, want 3", got)
	}
	isLoadNumbered := got[0].Load == 1
	isFailure := got[1].Kind == Failed && got[1].Err != nil
	isSeek := got[2].IsCommand() && got[2].At == 3
	if !isLoadNumbered || !isFailure || !isSeek {
		t.Errorf("events %+v", got)
	}
	if got[0].IsCommand() || got[1].IsCommand() {
		t.Error("news of the stream taken for commands")
	}
}

func TestHalvedEnd(t *testing.T) {
	for _, tc := range []struct{ duration, length, want float64 }{
		{426.2, 213.1, 213.1}, // counted twice
		{427, 213.1, 213.5},   // within the slack
		{213.1, 213.1, 0},     // counted right
		{426.2, 0, 0},         // the length unknown
		{0, 213.1, 0},         // the duration not known yet
	} {
		if got := halvedEnd(tc.duration, tc.length); got != tc.want {
			t.Errorf("halvedEnd(%v, %v) = %v, want %v", tc.duration, tc.length, got, tc.want)
		}
	}
}
