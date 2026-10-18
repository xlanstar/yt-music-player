package main

import (
	"os"

	"github.com/egoist/mygo/i18n"
)

// fallbackLanguage is what ytm shows when none of the user's languages is
// one of the catalogs': the source language.
const fallbackLanguage = "en"

// useLanguage sets the language of every message, once at start: ytm
// links no MyGo App, which would.
func useLanguage() {
	lang := language(os.Getenv, systemLanguages)
	i18n.SetLocaleFunc(func() string { return lang })
}

// language picks the catalogs' language for the first source naming any:
// $LC_ALL, $LC_MESSAGES, the system's, then $LANG, which macOS terminals set
// from the region. One without a catalog ("zh_CN", "ja", "C") falls back to
// English, not to the next source.
func language(getenv func(string) string, system func() []string) string {
	preferred := userLanguages(getenv, system)
	if lang := i18n.Match(preferred, i18n.Languages()); lang != "" {
		return lang
	}
	return fallbackLanguage
}

// userLanguages returns the languages of the first source that names
// any, normalized to BCP 47.
func userLanguages(getenv func(string) string, system func() []string) []string {
	if lang, ok := envLanguage(getenv("LC_ALL")); ok {
		return lang
	}
	if lang, ok := envLanguage(getenv("LC_MESSAGES")); ok {
		return lang
	}
	var langs []string
	for _, l := range system() {
		if tag := i18n.Normalize(l); tag != "" {
			langs = append(langs, tag)
		}
	}
	if len(langs) > 0 {
		return langs
	}
	lang, _ := envLanguage(getenv("LANG"))
	return lang
}

// envLanguage reads a locale variable: unset or empty is no answer, so
// the next source decides; "C" or "POSIX" is one, a language of none.
func envLanguage(v string) ([]string, bool) {
	if v == "" {
		return nil, false
	}
	if tag := i18n.Normalize(v); tag != "" {
		return []string{tag}, true
	}
	return nil, true
}
