package main

import (
	"testing"

	"github.com/rs/zerolog"
)

func TestLogLevel(t *testing.T) {
	for _, tc := range []struct {
		env     string
		want    zerolog.Level
		wantErr bool
	}{
		{"", zerolog.WarnLevel, false},
		{"debug", zerolog.DebugLevel, false},
		{" INFO ", zerolog.InfoLevel, false},
		{"error", zerolog.ErrorLevel, false},
		{"loud", zerolog.WarnLevel, true},
	} {
		got, err := logLevel(tc.env)
		if (err != nil) != tc.wantErr {
			t.Errorf("YT_LOG=%q: err %v", tc.env, err)
		}
		if got != tc.want {
			t.Errorf("YT_LOG=%q: level %v, want %v", tc.env, got, tc.want)
		}
	}
}
