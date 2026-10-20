package termimage

import (
	"slices"
	"strconv"
	"strings"
)

// ProbeID is the image id of Probe's Kitty query, which its reply names.
const ProbeID = 22868

// CellSizeQuery asks for a cell's pixels, which a font's size changes.
const CellSizeQuery = "\x1b[16t"

// Env is what the environment says of the terminal.
type Env struct {
	// Override is YTM_IMAGES: auto (or empty), off, blocks, sixel, iterm2
	// or kitty.
	Override                      string
	Term, TermProgram, LCTerminal string
	// Tmux is set inside tmux; Kitty and Ghostty when started from one,
	// which tmux keeps.
	Tmux, Kitty, Ghostty bool
	// Colors reports 256 colors or more, as Bubble Tea's profile does.
	Colors bool
}

// EnvFrom reads Env, but Colors, through getenv.
func EnvFrom(getenv func(string) string) Env {
	return Env{
		Override:    getenv("YTM_IMAGES"),
		Term:        getenv("TERM"),
		TermProgram: getenv("TERM_PROGRAM"),
		LCTerminal:  getenv("LC_TERMINAL"),
		Tmux:        getenv("TMUX") != "",
		Kitty:       getenv("KITTY_WINDOW_ID") != "",
		Ghostty:     getenv("GHOSTTY_RESOURCES_DIR") != "",
	}
}

// Replies are the terminal's answers to Probe.
type Replies struct {
	// Kitty is set when the graphics query was answered OK.
	Kitty bool
	// Sixel is set when the primary device attributes list 4.
	Sixel bool
	// Version is XTVERSION's, as "kitty(0.42.1)" or "iTerm2 3.5.14".
	Version      string
	CellW, CellH int
}

// Probe asks for Kitty graphics, the cell's pixels, the terminal's name and,
// last, its device attributes; a 1×1 query stores nothing.
func Probe(tmux bool) string {
	query := "\x1b_Gi=" + strconv.Itoa(ProbeID) + ",s=1,v=1,a=q,t=d,f=24;AAAA\x1b\\"
	if tmux {
		query = passthrough(query)
	}
	return query + CellSizeQuery + "\x1b[>0q\x1b[c"
}

// NeedsProbe reports whether Detect needs Probe's replies: not when
// YTM_IMAGES names a protocol, nor on a terminal without images, whose
// Linux console would print the probe.
func NeedsProbe(e Env) bool {
	_, ok := parseOverride(e.Override)
	return !ok && !isPlain(e.Term)
}

// parseOverride returns the protocol YTM_IMAGES names; ok is false for
// auto, or a name it does not know.
func parseOverride(s string) (p Protocol, ok bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "off", "none", "0", "false":
		return None, true
	case "blocks":
		return Blocks, true
	case "sixel":
		return Sixel, true
	case "iterm2":
		return ITerm2, true
	case "kitty":
		return Kitty, true
	}
	return None, false
}

// Detect picks the best protocol the terminal shows images with.
func Detect(e Env, r Replies) Caps {
	c := Caps{CellW: r.CellW, CellH: r.CellH}
	if p, ok := parseOverride(e.Override); ok {
		c.Protocol = p
		return c
	}
	version := strings.ToLower(r.Version)
	// tmux keeps the placeholders, text, but answers the query itself.
	hasKitty := r.Kitty && hasPlaceholders(e, version) || e.Tmux && (e.Kitty || e.Ghostty)
	switch {
	case isPlain(e.Term):
	case hasKitty && e.Colors:
		// The placeholders' color, the image id, is one of 256.
		c.Protocol = Kitty
	case !e.Tmux && isITerm2(e, version):
		c.Protocol = ITerm2
	case r.Sixel && r.CellW > 0 && r.CellH > 0:
		c.Protocol = Sixel
	case e.Colors:
		c.Protocol = Blocks
	}
	return c
}

// isPlain reports a terminal that shows text only.
func isPlain(term string) bool { return term == "dumb" || term == "linux" }

// hasPlaceholders reports Kitty's Unicode placeholders, which some
// terminals speaking its graphics lack (WezTerm, Konsole).
func hasPlaceholders(e Env, version string) bool {
	return strings.HasPrefix(version, "kitty") || strings.HasPrefix(version, "ghostty") ||
		e.Kitty || e.Ghostty || e.Term == "xterm-kitty" || e.Term == "xterm-ghostty" ||
		hasITerm2Placeholders(version)
}

// minITerm2 is the first iTerm2 whose placeholders show the image sent
// again under their id, as covers do when they reuse one; older ones keep
// the first.
var minITerm2 = [3]int{3, 7, 5}

// minITerm2Nightly is the first nightly with minITerm2's fix, whose patch
// is its date.
const minITerm2Nightly = 20260919

// hasITerm2Placeholders reports XTVERSION's iTerm2 at least minITerm2:
// "iterm2 3.7.10", a later release's beta ("iterm2 3.7.7beta1") or nightly;
// minITerm2's own betas, and versions that do not parse, are older.
func hasITerm2Placeholders(version string) bool {
	rest, ok := strings.CutPrefix(version, "iterm2")
	if !ok {
		return false
	}
	got, rest, ok := parseVersion(strings.TrimSpace(rest))
	if !ok {
		return false
	}
	if got[2] >= 10_000_000 && got[0] == minITerm2[0] && got[1] == minITerm2[1] {
		return got[2] >= minITerm2Nightly
	}
	switch slices.Compare(got[:], minITerm2[:]) {
	case 1:
		return true
	case 0:
		return rest == ""
	}
	return false
}

// parseVersion reads "major.minor.patch" off s's start, a missing minor or
// patch 0, and returns what follows it.
func parseVersion(s string) (v [3]int, rest string, ok bool) {
	for i := range v {
		if i > 0 {
			if s, ok = strings.CutPrefix(s, "."); !ok {
				return v, s, true
			}
		}
		n := 0
		for n < len(s) && '0' <= s[n] && s[n] <= '9' {
			n++
		}
		x, err := strconv.Atoi(s[:n])
		if err != nil {
			return v, s, false
		}
		v[i], s = x, s[n:]
	}
	return v, s, true
}

func isITerm2(e Env, version string) bool {
	return strings.HasPrefix(version, "iterm2") || strings.HasPrefix(version, "wezterm") ||
		e.TermProgram == "iTerm.app" || e.TermProgram == "WezTerm" || e.LCTerminal == "iTerm2"
}
