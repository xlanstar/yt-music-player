package main

import (
	"golang.org/x/sys/windows"

	"github.com/rs/zerolog/log"
)

// systemLanguages returns the user's display languages, in the order of
// Settings > Time & language.
func systemLanguages() []string {
	langs, err := windows.GetUserPreferredUILanguages(windows.MUI_LANGUAGE_NAME)
	if err != nil {
		log.Debug().Err(err).Msg("ytm: preferred languages")
		return nil
	}
	return langs
}
