package termimage

import (
	"slices"
	"strconv"
	"strings"
)

// ProbeID is the image id of Probe's Kitty query, which its reply names.
const ProbeID = 22868

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
	// Done is set once the device attributes came, which a terminal sends
	// after its other answers.
	Done bool
}

// Attributes keeps the primary device attributes, the probe's last answer.
func (r *Replies) Attributes(attrs []int) {
	r.Sixel = slices.Contains(attrs, 4)
	r.Done = true
}

// Probe asks for Kitty graphics, the cell's pixels, the terminal's name and,
// last, its device attributes; a 1×1 query stores nothing.
func Probe(tmux bool) string {
	query := "\x1b_Gi=" + strconv.Itoa(ProbeID) + ",s=1,v=1,a=q,t=d,f=24;AAAA\x1b\\"
	if tmux {
		query = Passthrough(query)
	}
	return query + "\x1b[16t\x1b[>0q\x1b[c"
}

// ParseOverride returns the protocol YTM_IMAGES names; ok is false for
// auto, or a name it does not know.
func ParseOverride(s string) (p Protocol, ok bool) {
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
	c := Caps{CellW: r.CellW, CellH: r.CellH, Tmux: e.Tmux}
	if p, ok := ParseOverride(e.Override); ok {
		c.Protocol = p
		return c
	}
	version := strings.ToLower(r.Version)
	switch {
	case e.Term == "dumb" || e.Term == "linux":
	case r.Kitty && hasPlaceholders(e, version), e.Tmux && (e.Kitty || e.Ghostty):
		// tmux keeps the placeholders, text, but answers the query itself.
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

// hasPlaceholders reports Kitty's Unicode placeholders, which some
// terminals speaking its graphics lack (WezTerm, Konsole).
func hasPlaceholders(e Env, version string) bool {
	return strings.HasPrefix(version, "kitty") || strings.HasPrefix(version, "ghostty") ||
		e.Kitty || e.Ghostty || e.Term == "xterm-kitty" || e.Term == "xterm-ghostty"
}

func isITerm2(e Env, version string) bool {
	return strings.HasPrefix(version, "iterm2") || strings.HasPrefix(version, "wezterm") ||
		e.TermProgram == "iTerm.app" || e.TermProgram == "WezTerm" || e.LCTerminal == "iTerm2"
}
