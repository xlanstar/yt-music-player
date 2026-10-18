package termimage

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"regexp"
	"strings"
	"testing"
)

// cover is a deterministic square cover of n pixels: a gradient.
func cover(n int) *image.RGBA {
	m := image.NewRGBA(image.Rect(0, 0, n, n))
	for y := range n {
		for x := range n {
			m.SetRGBA(x, y, color.RGBA{uint8(x * 255 / n), uint8(y * 255 / n), uint8((x + y) * 127 / n), 255})
		}
	}
	return m
}

func solid(w, h int, c color.RGBA) *image.RGBA {
	m := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			m.SetRGBA(x, y, c)
		}
	}
	return m
}

func TestDetect(t *testing.T) {
	cell := Replies{CellW: 9, CellH: 18, Done: true}
	tests := []struct {
		name string
		env  Env
		r    Replies
		want Protocol
	}{
		{"kitty", Env{Term: "xterm-kitty"}, Replies{Kitty: true, Version: "kitty(0.42.1)", Done: true}, Kitty},
		{"ghostty", Env{TermProgram: "ghostty"}, Replies{Kitty: true, Version: "ghostty 1.2.0", Done: true}, Kitty},
		{"wezterm speaks Kitty without placeholders", Env{TermProgram: "WezTerm"}, Replies{Kitty: true, Sixel: true, Version: "WezTerm 20240203", Done: true}, ITerm2},
		{"iterm2", Env{TermProgram: "iTerm.app"}, Replies{Version: "iTerm2 3.5.14", Done: true}, ITerm2},
		{"iterm2 over ssh", Env{LCTerminal: "iTerm2"}, Replies{Done: true}, ITerm2},
		{"sixel", Env{Term: "foot"}, Replies{Sixel: true, CellW: 9, CellH: 18, Done: true}, Sixel},
		{"sixel without its cell", Env{Term: "foot", Colors: true}, Replies{Sixel: true, Done: true}, Blocks},
		{"tmux in kitty", Env{Tmux: true, Kitty: true}, Replies{Sixel: true, Version: "tmux 3.5", Done: true}, Kitty},
		{"tmux in iterm2", Env{Tmux: true, TermProgram: "tmux", LCTerminal: "iTerm2", Colors: true}, cell, Blocks},
		{"tmux with sixel", Env{Tmux: true}, Replies{Sixel: true, CellW: 9, CellH: 18, Done: true}, Sixel},
		{"colors only", Env{Term: "xterm-256color", Colors: true}, Replies{Done: true}, Blocks},
		{"sixteen colors", Env{Term: "xterm"}, Replies{Done: true}, None},
		{"dumb", Env{Term: "dumb", Colors: true}, Replies{Kitty: true, Done: true}, None},
		{"override", Env{Override: "Sixel", Term: "xterm-kitty"}, Replies{Kitty: true, Done: true}, Sixel},
		{"override off", Env{Override: "off", Term: "xterm-kitty"}, Replies{Kitty: true, Done: true}, None},
		{"override unknown is auto", Env{Override: "png", Colors: true}, Replies{Done: true}, Blocks},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Detect(tc.env, tc.r)
			if got.Protocol != tc.want {
				t.Errorf("Detect = %v, want %v", got.Protocol, tc.want)
			}
			if got.CellW != tc.r.CellW || got.Tmux != tc.env.Tmux {
				t.Errorf("Detect = %+v, lost the replies or the env", got)
			}
		})
	}
}

func TestEnvFrom(t *testing.T) {
	env := map[string]string{"YTM_IMAGES": "kitty", "TERM": "xterm-kitty", "TMUX": "/tmp/tmux", "KITTY_WINDOW_ID": "1"}
	got := EnvFrom(func(k string) string { return env[k] })
	want := Env{Override: "kitty", Term: "xterm-kitty", Tmux: true, Kitty: true}
	if got != want {
		t.Errorf("EnvFrom = %+v, want %+v", got, want)
	}
}

func TestAttributes(t *testing.T) {
	var r Replies
	r.Attributes([]int{62, 4, 22})
	if !r.Sixel || !r.Done {
		t.Errorf("Attributes(62;4;22) = %+v, want Sixel and Done", r)
	}
	r.Attributes([]int{62, 22})
	if r.Sixel {
		t.Error("Attributes(62;22) kept Sixel")
	}
}

func TestProbe(t *testing.T) {
	p := Probe(false)
	if !strings.HasPrefix(p, "\x1b_Gi=22868,") || !strings.HasSuffix(p, "\x1b[c") {
		t.Errorf("Probe = %q: the query first, the device attributes last", p)
	}
	if got := Probe(true); !strings.HasPrefix(got, "\x1bPtmux;\x1b\x1b_G") {
		t.Errorf("Probe in tmux = %q, want the Kitty query passed through", got)
	}
}

func TestFit(t *testing.T) {
	tests := []struct {
		caps       Caps
		rows, want int
	}{
		{Caps{}, 2, 4},
		{Caps{CellW: 8, CellH: 17}, 2, 4},
		{Caps{CellW: 10, CellH: 30}, 2, 6},
		{Caps{CellW: 20, CellH: 20}, 2, 2},
		{Caps{CellW: 1, CellH: 50}, 2, 8},
	}
	for _, tc := range tests {
		if got := tc.caps.Fit(tc.rows); got != tc.want {
			t.Errorf("%+v.Fit(%d) = %d, want %d", tc.caps, tc.rows, got, tc.want)
		}
	}
}

func TestPixels(t *testing.T) {
	tests := []struct {
		caps Caps
		w, h int
	}{
		{Caps{Protocol: Blocks}, 4, 4},
		{Caps{Protocol: Kitty, CellW: 20, CellH: 40}, 80, 80},
		{Caps{Protocol: Kitty, CellW: 100, CellH: 200}, 256, 256},
		{Caps{Protocol: ITerm2}, 40, 40},
		{Caps{Protocol: Sixel, CellW: 100, CellH: 200}, 400, 400},
	}
	for _, tc := range tests {
		if w, h := tc.caps.Pixels(4, 2, 256); w != tc.w || h != tc.h {
			t.Errorf("%+v.Pixels(4, 2) = %d×%d, want %d×%d", tc.caps, w, h, tc.w, tc.h)
		}
	}
}

func TestFill(t *testing.T) {
	red := color.RGBA{200, 10, 20, 255}
	if got := Fill(solid(64, 64, red), 7, 5).RGBAAt(6, 4); got != red {
		t.Errorf("Fill of a solid cover = %v, want %v", got, red)
	}
	// A wide box crops the cover's top and bottom.
	src := solid(10, 10, color.RGBA{0, 0, 0, 255})
	for x := range 10 {
		src.SetRGBA(x, 0, color.RGBA{255, 255, 255, 255})
	}
	if got := Fill(src, 10, 5).RGBAAt(5, 0); got.R != 0 {
		t.Errorf("Fill to 10×5 kept the top row: %v", got)
	}
	// Each pixel is the mean of the four it covers.
	quad := solid(2, 2, color.RGBA{0, 0, 0, 255})
	quad.SetRGBA(0, 0, color.RGBA{255, 255, 255, 255})
	if got := Fill(quad, 1, 1).RGBAAt(0, 0); got.R != 64 {
		t.Errorf("Fill to 1×1 = %v, want the mean, 64", got)
	}
	// Up, each pixel repeats.
	if got := Fill(solid(1, 1, red), 3, 3).RGBAAt(2, 2); got != red {
		t.Errorf("Fill up = %v, want %v", got, red)
	}
}

func TestBlocks(t *testing.T) {
	m := solid(2, 4, color.RGBA{1, 2, 3, 255})
	m.SetRGBA(1, 1, color.RGBA{255, 0, 128, 255})
	got := BlockLines(m)
	want := []string{
		"\x1b[38;2;1;2;3;48;2;1;2;3m▀\x1b[38;2;1;2;3;48;2;255;0;128m▀\x1b[m",
		"\x1b[38;2;1;2;3;48;2;1;2;3m▀\x1b[38;2;1;2;3;48;2;1;2;3m▀\x1b[m",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("Blocks =\n%q\nwant\n%q", got, want)
	}
}

var kittyCommand = regexp.MustCompile("\x1b_G([^;]*);([^\x1b]*)\x1b\\\\")

func TestKittyUpload(t *testing.T) {
	m := cover(64) // 12288 bytes of RGB: 16384 of base64, 4 chunks
	seq := KittyUpload(m, 7, 4, 2, false)
	cmds := kittyCommand.FindAllStringSubmatch(seq, -1)
	if len(cmds) != 4 || strings.Join(kittyCommand.FindAllString(seq, -1), "") != seq {
		t.Fatalf("KittyUpload sent %d commands, want 4 and nothing else", len(cmds))
	}
	if want := "a=T,U=1,q=2,f=24,s=64,v=64,i=7,c=4,r=2,m=1"; cmds[0][1] != want {
		t.Errorf("first command's keys = %q, want %q", cmds[0][1], want)
	}
	var data []byte
	for i, c := range cmds {
		more := "m=1"
		if i == len(cmds)-1 {
			more = "m=0"
		}
		if !strings.HasSuffix(c[1], more) {
			t.Errorf("command %d's keys %q lack %s", i, c[1], more)
		}
		b, err := base64.StdEncoding.DecodeString(c[2])
		if err != nil {
			t.Fatalf("command %d: %v", i, err)
		}
		data = append(data, b...)
	}
	if len(data) != 64*64*3 {
		t.Fatalf("sent %d bytes, want %d", len(data), 64*64*3)
	}
	for i := range 64 * 64 {
		p := m.Pix[4*i:]
		if !bytes.Equal(data[3*i:3*i+3], p[:3]) {
			t.Fatalf("pixel %d = %v, want %v", i, data[3*i:3*i+3], p[:3])
		}
	}
	tmux := KittyUpload(m, 7, 4, 2, true)
	if n := strings.Count(tmux, "\x1bPtmux;"); n != 4 {
		t.Errorf("in tmux, %d commands passed through, want 4", n)
	}
	if got := strings.ReplaceAll(tmux, "\x1b\x1b", "\x1b"); !strings.Contains(got, cmds[1][0]) {
		t.Error("in tmux, the commands changed")
	}
}

func TestKittyCells(t *testing.T) {
	got := KittyCells(176, 2, 2)
	want := []string{
		"\x1b[38;5;176m\U0010EEEE\u0305\u0305\U0010EEEE\u0305\u030D\x1b[39m",
		"\x1b[38;5;176m\U0010EEEE\u030D\u0305\U0010EEEE\u030D\u030D\x1b[39m",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("KittyCells =\n%q\nwant\n%q", got, want)
	}
}

func TestKittyDelete(t *testing.T) {
	if got, want := KittyDelete(3, false), "\x1b_Ga=d,d=I,q=2,i=3\x1b\\"; got != want {
		t.Errorf("KittyDelete = %q, want %q", got, want)
	}
}

func TestITerm2(t *testing.T) {
	seq, err := ITerm2Image(cover(48), 4, 2, false)
	if err != nil {
		t.Fatal(err)
	}
	head, data, ok := strings.Cut(seq, ":")
	if !ok || !strings.HasSuffix(data, "\a") {
		t.Fatalf("ITerm2 = %.80q…, want OSC 1337 ending in BEL", seq)
	}
	if want := ";width=4;height=2;preserveAspectRatio=0;doNotMoveCursor=1"; !strings.HasSuffix(head, want) {
		t.Errorf("ITerm2's arguments %q lack %q", head, want)
	}
	b, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(data, "\a"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(b))
	if err != nil || cfg.Width != 48 || cfg.Height != 48 {
		t.Errorf("ITerm2 sent %d×%d (%v), want a 48×48 JPEG", cfg.Width, cfg.Height, err)
	}
}

func TestSixel(t *testing.T) {
	m := solid(5, 7, color.RGBA{255, 0, 0, 255})
	got := SixelImage(m)
	// Red is color 5·36: every pixel of the first band, the second band's
	// top row.
	want := "\x1bP0;1;0q\"1;1;5;7#180;2;100;0;0#180!5~-#180!5@-\x1b\\"
	if got != want {
		t.Errorf("Sixel(red 5×7) = %q, want %q", got, want)
	}
	gradient := SixelImage(cover(30))
	if n := strings.Count(gradient, "-"); n != 5 {
		t.Errorf("Sixel(30×30) has %d bands, want 5", n)
	}
	if !strings.HasPrefix(gradient, "\x1bP0;1;0q\"1;1;30;30#") || !strings.HasSuffix(gradient, "-\x1b\\") {
		t.Errorf("Sixel(30×30) = %.40q…%q", gradient, gradient[len(gradient)-4:])
	}
}

func TestLevel(t *testing.T) {
	for _, tc := range []struct{ v, want uint8 }{{0, 0}, {51, 1}, {255, 5}} {
		for th := range uint8(16) {
			if got := level(tc.v, th); got != tc.want {
				t.Errorf("level(%d, %d) = %d, want %d", tc.v, th, got, tc.want)
			}
		}
	}
	// Halfway between two steps, half the thresholds round up.
	up := 0
	for th := range uint8(16) {
		up += int(level(25, th))
	}
	if up != 8 {
		t.Errorf("level(25, ·) rounds up %d times of 16, want 8", up)
	}
}

func TestOverlay(t *testing.T) {
	if got, want := Overlay("IMG", 1, 20), "\x1b7\x1b[21;2HIMG\x1b8"; got != want {
		t.Errorf("Overlay = %q, want %q", got, want)
	}
	cells := OverlayCells(3, 2)
	if len(cells) != 2 || cells[1] != "\u2800\u2800\u2800" {
		t.Errorf("OverlayCells(3, 2) = %q", cells)
	}
}

func TestPassthrough(t *testing.T) {
	if got, want := Passthrough("\x1b_Gq\x1b\\"), "\x1bPtmux;\x1b\x1b_Gq\x1b\x1b\\\x1b\\"; got != want {
		t.Errorf("Passthrough = %q, want %q", got, want)
	}
}

func TestProtocolString(t *testing.T) {
	if Kitty.String() != "kitty" || Protocol(9).String() != "Protocol(9)" {
		t.Errorf("String = %q, %q", Kitty.String(), Protocol(9).String())
	}
	if !Sixel.IsOverlay() || Kitty.IsOverlay() {
		t.Error("IsOverlay: Sixel and iTerm2 only")
	}
}
