package stream

import (
	"encoding/json/v2"
	"net/url"
	"reflect"
	"testing"
	"time"
)

func TestPickFormat(t *testing.T) {
	const baseURL = "https://rr1---sn-fixture.googlevideo.com/videoplayback?expire=1&itag="
	opus := func(itag, bitrate int) format {
		return format{Itag: itag, URL: baseURL + "x", MimeType: `audio/webm; codecs="opus"`, Bitrate: bitrate}
	}
	mp4 := func(itag, bitrate int) format {
		return format{Itag: itag, URL: baseURL + "x", MimeType: `audio/mp4; codecs="mp4a.40.2"`, Bitrate: bitrate}
	}
	with := func(f format, edit func(*format)) format { edit(&f); return f }
	dub := &struct {
		AudioIsDefault bool `json:"audioIsDefault"`
	}{}
	drc := func(f *format) { f.IsDrc = true }
	tests := []struct {
		name     string
		formats  []format
		wantItag int // 0: none
		wantDrc  bool
	}{
		{"the highest bitrate", []format{opus(250, 80000), opus(251, 150000), opus(249, 60000)}, 251, false},
		{"256k where there is", []format{opus(251, 150000), opus(774, 260000)}, 774, false},
		{"not AAC", []format{mp4(140, 130000), opus(249, 60000)}, 249, false},
		{"AAC only", []format{mp4(140, 130000)}, 0, false},
		{"not WebM video", []format{{Itag: 248, URL: baseURL + "248", MimeType: `video/webm; codecs="vp9"`}}, 0, false},
		{"no drc when plain is there", []format{with(opus(251, 150000), drc), opus(251, 150000)}, 251, false},
		{"drc rather than nothing", []format{with(opus(251, 150000), drc)}, 251, true},
		{"default track over a dub", []format{
			with(opus(251, 150000), func(f *format) { f.AudioTrack = dub }),
			opus(249, 50000),
		}, 249, false},
		{"skips signatureCipher", []format{with(opus(251, 0), func(f *format) { f.URL, f.SignatureCipher = "", "s=x&url=y" })}, 0, false},
		{"skips an n challenge", []format{with(opus(251, 0), func(f *format) { f.URL += "&n=abcdef" })}, 0, false},
		{"skips plain http", []format{with(opus(251, 0), func(f *format) { f.URL = "http://x/videoplayback" })}, 0, false},
		{"skips DRM", []format{with(opus(251, 0), func(f *format) { f.DrmFamilies = []any{"WIDEVINE"} })}, 0, false},
		{"skips OTF", []format{with(opus(251, 0), func(f *format) { f.Type = "FORMAT_STREAM_TYPE_OTF" })}, 0, false},
		{"skips live", []format{with(opus(251, 0), func(f *format) { f.TargetDurationSec = 5 })}, 0, false},
		{"none", nil, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, ok := pickFormat(tt.formats)
			if tt.wantItag == 0 {
				if ok {
					t.Errorf("picked %d, want none", f.Itag)
				}
				return
			}
			if !ok || f.Itag != tt.wantItag || f.IsDrc != tt.wantDrc {
				t.Errorf("picked %d (drc %v, ok %v), want %d (drc %v)", f.Itag, f.IsDrc, ok, tt.wantItag, tt.wantDrc)
			}
		})
	}
}

func TestExpiry(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	tests := []struct {
		name, url string
		in        int64
		want      time.Time
	}{
		{"expire parameter", "https://x/videoplayback?expire=1791988454&itag=140", 21540, time.Unix(1791988454, 0)},
		{"expiresInSeconds", "https://x/videoplayback?itag=140", 21540, now.Add(21540 * time.Second)},
		{"bad expire", "https://x/videoplayback?expire=soon", 60, now.Add(time.Minute)},
		{"unknown", "https://x/videoplayback", 0, time.Time{}},
	}
	for _, tt := range tests {
		if got := expiry(tt.url, tt.in, now); !got.Equal(tt.want) {
			t.Errorf("%s: expiry = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// FuzzQueryValue holds queryValue to url.Values' Get and Has.
func FuzzQueryValue(f *testing.F) {
	for _, q := range []string{
		"", "n=1", "expire=1700000000&n=abc", "a=1&&n", "n;x=1&n=2", "%6E=3", "n=%zz&n=4", "e+x=%20&expire=%31", "x=1&n=",
	} {
		f.Add(q, "n")
		f.Add(q, "expire")
	}
	f.Fuzz(func(t *testing.T, rawQuery, key string) {
		want, _ := url.ParseQuery(rawQuery)
		if got, ok := queryValue(rawQuery, key); got != want.Get(key) || ok != want.Has(key) {
			t.Errorf("queryValue(%q, %q) = %q, %v; want %q, %v", rawQuery, key, got, ok, want.Get(key), want.Has(key))
		}
	})
}

// TestPlayerBody holds the body to the maps it was once built from.
func TestPlayerBody(t *testing.T) {
	for _, c := range []client{visionOS, {Name: "BARE", Version: "1"}} {
		for _, visitor := range []string{"", "CgtWSVNJVE9SXzEyMw%3D%3D"} {
			client := map[string]any{"clientName": c.Name, "clientVersion": c.Version, "hl": "en"}
			for k, v := range map[string]string{
				"userAgent": c.UserAgent, "deviceMake": c.DeviceMake, "deviceModel": c.DeviceModel,
				"osName": c.OSName, "osVersion": c.OSVersion, "visitorData": visitor,
			} {
				if v != "" {
					client[k] = v
				}
			}
			want := map[string]any{
				"context": map[string]any{"client": client},
				"videoId": okID,
				"playbackContext": map[string]any{
					"contentPlaybackContext": map[string]any{"html5Preference": "HTML5_PREF_WANTS"},
				},
				"contentCheckOk": true,
				"racyCheckOk":    true,
			}
			data, err := playerBody(c, okID, visitor)
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("playerBody(%s, %q) = %s, want %v", c.Name, visitor, data, want)
			}
		}
	}
}
