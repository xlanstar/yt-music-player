package main

import (
	"testing"

	"github.com/egoist/mygo/i18n"
)

func TestLanguage(t *testing.T) {
	for _, tc := range []struct {
		name   string
		env    map[string]string
		system []string
		want   string
	}{
		{"nothing", nil, nil, "en"},
		{"LC_ALL", map[string]string{"LC_ALL": "zh_TW.UTF-8", "LANG": "en_US.UTF-8"}, []string{"en-US"}, "zh-Hant"},
		{"LC_ALL C wins", map[string]string{"LC_ALL": "C"}, []string{"zh-Hant-TW"}, "en"},
		{"LC_MESSAGES", map[string]string{"LC_MESSAGES": "zh_HK.UTF-8"}, []string{"en-US"}, "zh-Hant"},
		{"LC_ALL before LC_MESSAGES", map[string]string{"LC_ALL": "en_US.UTF-8", "LC_MESSAGES": "zh_TW"}, nil, "en"},
		{"system", map[string]string{"LANG": "en_TW.UTF-8"}, []string{"zh-Hant-TW", "en-TW"}, "zh-Hant"},
		{"system in order", nil, []string{"en-TW", "zh-Hant-TW"}, "en"},
		{"system without a catalog", nil, []string{"ja-JP", "zh-Hant-TW"}, "zh-Hant"},
		{"system before LANG", map[string]string{"LANG": "zh_TW.UTF-8"}, []string{"fr-FR"}, "en"},
		{"LANG", map[string]string{"LANG": "zh_TW.UTF-8"}, nil, "zh-Hant"},
		{"Simplified", map[string]string{"LANG": "zh_CN.UTF-8"}, nil, "en"},
		{"POSIX", map[string]string{"LANG": "POSIX"}, nil, "en"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			getenv := func(k string) string { return tc.env[k] }
			system := func() []string { return tc.system }
			if got := language(getenv, system); got != tc.want {
				t.Errorf("language = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestUseLanguage(t *testing.T) {
	t.Cleanup(func() { i18n.SetLocaleFunc(func() string { return "" }) })
	useLanguage(func(k string) string {
		if k == "LC_ALL" {
			return "zh_TW.UTF-8"
		}
		return ""
	}, func() []string { return nil })
	w := newWorld(nil)
	w.run(t, "daemon", "status")
	if got, want := w.stdout.String(), "背景播放器沒有在執行。\n"; got != want {
		t.Errorf("stdout %q, want %q", got, want)
	}
}

// TestSystemLanguages calls the OS: it must not fail, whatever it returns.
func TestSystemLanguages(t *testing.T) {
	for _, l := range systemLanguages() {
		if l == "" {
			t.Error("an empty language")
		}
	}
}
