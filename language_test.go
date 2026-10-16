package main

import "testing"

// The view follows the app's language as it changes, the text it keeps
// between builds too.
func TestLanguageChanges(t *testing.T) {
	a := idleApp()
	tt := newTester(a)
	wantTexts(t, tt, "Home", "Liked music", "Paper Moons by Ivy Calder")

	inLanguage(t, "zh-Hant")
	tt.Frame()
	// The sidebar, Home's chips and the player bar are said anew; the
	// sidebar's Liked music and the rows' names are kept between builds.
	wantTexts(t, tt, "首頁", "探索", "媒體庫", "喜歡的音樂", "自動播放清單", "全部", "播放進度")
	for _, english := range []string{"Home", "Liked music", "Auto playlist", "Seek"} {
		if tt.HasText(english) {
			t.Errorf("%q still shows in Chinese", english)
		}
	}
	if !tt.HasText("Ivy Calder 的〈Paper Moons〉") {
		t.Errorf("rows kept their English names, texts %q", tt.Texts())
	}
}
