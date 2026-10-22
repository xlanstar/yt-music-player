package catalog

// Placeholder returns a fictional catalog for offline use. Every name in it
// is made up.
func Placeholder() *Catalog {
	library := placeholderPlaylists()
	c := New(placeholderSongs(), placeholderAlbums(), append(library, placeholderMixes()...))
	ids := make([]string, len(library))
	for i, playlist := range library {
		ids[i] = playlist.ID
	}
	c.SetLibrary(ids)
	// Original lines, not a real song's.
	c.lyrics = map[string][]string{"midnight-arcade": {
		"Coins in my pocket, lights on the ceiling",
		"We were the high score nobody was keeping",
		"Neon on your face like a slow-motion sunrise",
		"Every game over felt like a new life",
		"So stay till the screen fades out",
		"Stay till the city hums the sound",
		"Of a midnight arcade",
		"Where nobody grows old",
		"Pixels in the rain, we were brave in the glow",
		"Press start again, I'm not ready to go",
	}}
	return c
}

// placeholderMixes' Owner holds the subtitle shown under each, as on
// YouTube Music.
func placeholderMixes() []Playlist {
	return []Playlist{
		{"supermix", "My Supermix", "Lumen Drive, Ivy Calder, Orbit Kids and more", "velvet-static", []string{
			"cold-coffee", "midnight-arcade", "paper-moons", "gravity-well", "radio-sunset", "northern-lines", "glass-garden",
		}},
		{"discover", "Discover Mix", "New music picked from what you play", "blue-cassette", []string{
			"rewind", "copperhead", "tape-hiss-lullaby", "ember", "glass-garden", "pale-blue-hour",
		}},
		{"new-release", "New Release Mix", "Fresh drops from artists you follow", "saffron-hours", []string{
			"golden-hour-drive", "glass-garden", "satellite-hearts", "weightless", "slow-burn-summer",
		}},
		{"replay", "Replay Mix", "The songs you can't stop playing", "neon-tide", []string{
			"midnight-arcade", "3-am-thoughts", "radio-sunset", "gravity-well", "static-bloom",
		}},
	}
}

func placeholderMoods() []Mood {
	return []Mood{
		{Name: "Chill", Tint: Tint{0.72, 0.12, 200}, ID: "mood-chill"},
		{Name: "Commute", Tint: Tint{0.75, 0.14, 80}, ID: "mood-commute"},
		{Name: "Energize", Tint: Tint{0.68, 0.2, 30}, ID: "mood-energize"},
		{Name: "Feel good", Tint: Tint{0.8, 0.15, 110}, ID: "mood-feel-good"},
		{Name: "Focus", Tint: Tint{0.65, 0.14, 260}, ID: "mood-focus"},
		{Name: "Party", Tint: Tint{0.65, 0.22, 350}, ID: "mood-party"},
		{Name: "Romance", Tint: Tint{0.7, 0.16, 10}, ID: "mood-romance"},
		{Name: "Sleep", Tint: Tint{0.55, 0.12, 290}, ID: "mood-sleep"},
		{Name: "Workout", Tint: Tint{0.7, 0.18, 50}, ID: "mood-workout"},
		{Name: "Indie", Tint: Tint{0.72, 0.13, 150}, ID: "genre-indie"},
		{Name: "Jazz", Tint: Tint{0.68, 0.12, 70}, ID: "genre-jazz"},
		{Name: "Electronic", Tint: Tint{0.7, 0.16, 300}, ID: "genre-electronic"},
	}
}

func placeholderTrending() []string {
	return []string{
		"3-am-thoughts", "midnight-arcade", "radio-sunset", "gravity-well",
		"ember", "paper-moons", "northern-lines", "static-bloom",
	}
}

// PlaceholderLikes returns the IDs of the placeholder user's liked songs.
func PlaceholderLikes() []string {
	return []string{"midnight-arcade", "gravity-well", "radio-sunset", "3-am-thoughts"}
}

func placeholderSongs() []Song {
	return []Song{
		{"midnight-arcade", "Midnight Arcade", "Lumen Drive", "Neon Tide", "neon-tide", 227, "neon-tide"},
		{"satellite-hearts", "Satellite Hearts", "Lumen Drive", "Neon Tide", "neon-tide", 245, "neon-tide"},
		{"paper-moons", "Paper Moons", "Ivy Calder", "Paper Moons", "paper-moons", 192, "paper-moons"},
		{"slow-burn-summer", "Slow Burn Summer", "Ivy Calder", "Paper Moons", "paper-moons", 214, "paper-moons"},
		{"gravity-well", "Gravity Well", "Orbit Kids", "Low Orbit", "low-orbit", 268, "low-orbit"},
		{"weightless", "Weightless", "Orbit Kids", "Low Orbit", "low-orbit", 201, "low-orbit"},
		{"cold-coffee", "Cold Coffee", "The Velvet Hours", "Velvet Static", "velvet-static", 176, "velvet-static"},
		{"static-bloom", "Static Bloom", "The Velvet Hours", "Velvet Static", "velvet-static", 233, "velvet-static"},
		{"glass-garden", "Glass Garden", "Fern & Folly", "Glasshouse", "glasshouse", 208, "glasshouse"},
		{"radio-sunset", "Radio Sunset", "Coastline", "Afterglow FM", "afterglow-fm", 239, "afterglow-fm"},
		{"northern-lines", "Northern Lines", "Mara Holt", "Northbound", "northbound", 284, "northbound"},
		{"golden-hour-drive", "Golden Hour Drive", "Saffron", "Saffron Hours", "saffron-hours", 197, "saffron-hours"},
		{"3-am-thoughts", "3 AM Thoughts", "Nightjar", "Small Hours", "small-hours", 221, "small-hours"},
		{"copperhead", "Copperhead", "Rust Belt Radio", "Copper Lines", "", 186, "copper-lines"},
		{"rewind", "Rewind", "Kasette", "Blue Cassette", "blue-cassette", 204, "blue-cassette"},
		{"ember", "Ember", "Ash Wilder", "Ember & Ash", "", 252, "ember-ash"},
		{"pale-blue-hour", "Pale Blue Hour", "Mara Holt", "Northbound", "northbound", 236, "northbound"},
		{"tape-hiss-lullaby", "Tape Hiss Lullaby", "Kasette", "Blue Cassette", "blue-cassette", 189, "blue-cassette"},
	}
}

func placeholderAlbums() []Album {
	return []Album{
		{"neon-tide", "Neon Tide", "Lumen Drive", 2025, ReleaseAlbum, "neon-tide"},
		{"paper-moons", "Paper Moons", "Ivy Calder", 2024, ReleaseAlbum, "paper-moons"},
		{"low-orbit", "Low Orbit", "Orbit Kids", 2025, ReleaseEP, "low-orbit"},
		{"velvet-static", "Velvet Static", "The Velvet Hours", 2023, ReleaseAlbum, "velvet-static"},
		{"glasshouse", "Glasshouse", "Fern & Folly", 2025, ReleaseSingle, "glasshouse"},
		{"afterglow-fm", "Afterglow FM", "Coastline", 2024, ReleaseAlbum, "afterglow-fm"},
		{"northbound", "Northbound", "Mara Holt", 2022, ReleaseAlbum, "northbound"},
		{"saffron-hours", "Saffron Hours", "Saffron", 2025, ReleaseEP, "saffron-hours"},
		{"small-hours", "Small Hours", "Nightjar", 2024, ReleaseAlbum, "small-hours"},
		{"blue-cassette", "Blue Cassette", "Kasette", 2023, ReleaseAlbum, "blue-cassette"},
	}
}

func placeholderPlaylists() []Playlist {
	return []Playlist{
		{"late-night-drive", "Late Night Drive", "Lan", "neon-tide", []string{
			"midnight-arcade", "radio-sunset", "golden-hour-drive", "rewind", "satellite-hearts", "3-am-thoughts", "pale-blue-hour",
		}},
		{"deep-focus", "Deep Focus", "Lan", "low-orbit", []string{
			"gravity-well", "weightless", "northern-lines", "pale-blue-hour", "tape-hiss-lullaby", "glass-garden",
		}},
		{"sunday-slow", "Sunday Slow", "Lan", "glasshouse", []string{
			"glass-garden", "paper-moons", "slow-burn-summer", "tape-hiss-lullaby", "cold-coffee",
		}},
		{"gym-rotation", "Gym Rotation", "Lan", "ember-ash", []string{
			"ember", "copperhead", "static-bloom", "satellite-hearts", "midnight-arcade",
		}},
		{"rainy-window", "Rainy Window", "Lan", "small-hours", []string{
			"3-am-thoughts", "cold-coffee", "northern-lines", "pale-blue-hour", "weightless",
		}},
		{"summer-24", "Summer '24", "Lan", "afterglow-fm", []string{
			"radio-sunset", "golden-hour-drive", "slow-burn-summer", "paper-moons", "rewind",
		}},
		{"indie-discoveries", "Indie Discoveries", "YT Music", "copper-lines", []string{
			"copperhead", "static-bloom", "gravity-well", "glass-garden", "ember", "paper-moons",
		}},
	}
}
