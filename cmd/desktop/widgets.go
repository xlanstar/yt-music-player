package main

import (
	"math"
	"time"
	"yt-music-player/internal/catalog"
	"yt-music-player/locales"

	"github.com/egoist/mygo/ui"
)

// iconOpts are an iconButton's options. Disabled changes input, so
// iconButton applies every option before asking about hover or press.
type iconOpts struct {
	isOn       bool // accent tint and a dot, as for shuffle or an open panel
	isDisabled bool
	fg         ui.Color // resting icon color instead of Muted
	bg         ui.Color // resting fill, which hover and press tint
	// isToggle makes it a toggle button, pressed while checked, so assistive
	// technology reads its state rather than a changing label.
	isToggle, isChecked bool
	tip                 string // tooltip instead of the label, as "Unlike" for a pressed Like
	keys                string // shortcut shown in the tooltip
}

// iconButton keeps the keyboard focus where it was when clicked, so Space
// still plays and pauses; Tab still reaches it.
func iconButton(c *ui.Context, key any, icon *ui.SVG, label string, size, iconSize float32, opts iconOpts) ui.Element {
	b := ui.ButtonBase(c.Key(key)).Size(size, size).Shrink(0).Radius(size / 2).Center().
		Label(label).Cursor(ui.CursorPointer).KeepFocus().Disabled(opts.isDisabled)
	if opts.isToggle {
		b.Role(ui.RoleToggleButton).Checked(opts.isChecked)
	}
	text := label
	if opts.tip != "" {
		text = opts.tip
	}
	tip(c, b, text, opts.keys)
	hovered := b.Hovered()
	fg := th.Color.Muted
	switch {
	case b.IsDisabled():
		fg = th.Color.Faint
	case opts.isOn:
		fg = th.Color.Accent
	case opts.fg.A > 0:
		fg = opts.fg
	case hovered:
		fg = th.Color.Text
	}
	bg := opts.bg
	switch {
	case b.Pressed():
		bg = over(th.Color.GhostPressed, bg)
	case hovered:
		bg = over(th.Color.GhostHover, bg)
	}
	b.Background(bg).TextColor(fg).Transition(colorFade).Children(func() {
		ui.Icon(c, icon).Size(iconSize, iconSize)
		if opts.isOn {
			// A dot as well as the color, so the state doesn't rest on hue alone.
			ui.Box(c).Size(4, 4).Radius(2).Background(th.Color.Accent).Absolute().Bottom(size * 0.08).Left(size/2 - 2)
		}
	})
	return b
}

// likeButton's label stays Like, pressed while liked.
func (a *app) likeButton(c *ui.Context, id string, size float32) {
	liked := a.session.Liked(id)
	icon, opts := icHeart, iconOpts{isToggle: true, isChecked: liked}
	if liked {
		icon, opts.tip, opts.fg = icHeartFilled, locales.Unlike(), th.Color.Accent
	}
	if iconButton(c, "like", icon, locales.Like(), size, size*0.5, opts).Clicked() {
		a.session.SetLiked(id, !liked)
	}
}

func (a *app) shuffleButton(c *ui.Context, size, iconSize float32) {
	isShuffled := a.mirror.State().Shuffled
	opts := iconOpts{isOn: isShuffled, isToggle: true, isChecked: isShuffled}
	if iconButton(c, "shuffle", icShuffle, locales.Shuffle(), size, iconSize, opts).Clicked() {
		a.client.ToggleShuffle()
	}
}

type pillStyle uint8

const (
	pillLight pillStyle = iota
	pillGlass
	pillGhost
)

func pill(c *ui.Context, key any, icon *ui.SVG, label string, style pillStyle) ui.Element {
	b := ui.ButtonBase(c.Key(key)).Height(40).Padding(0, th.Space(5)).Gap(th.Space(2)).Radius(th.Radius.Pill).
		Center().Shrink(0).Cursor(ui.CursorPointer).KeepFocus()
	hovered, pressed := b.Hovered(), b.Pressed()
	col := &th.Color
	var bg, fg ui.Color
	switch style {
	case pillLight:
		bg, fg = col.Text, col.Base
		if hovered {
			bg = col.Light
		}
	case pillGlass:
		bg, fg = col.Glass, col.Light
		if hovered {
			bg = col.GlassHover
		}
	case pillGhost:
		bg, fg = ui.Transparent, col.Text
		b.Border(1, col.GhostBorder)
		if hovered {
			bg = col.GhostHover
		}
	}
	if pressed {
		b.Scale(0.97, 0.97)
	}
	b.Background(bg).TextColor(fg).Transition(colorFade)
	b.Children(func() {
		if icon != nil {
			ui.Icon(c, icon).Size(16, 16)
		}
		ui.Text(c, label).FontSize(th.Type.Label).FontWeight(600).SingleLine()
	})
	return b
}

// chip styles b, a segment or a tab, as a filter pill. The chosen one is
// inverted, not accented: the accent is reserved for playback.
func chip(c *ui.Context, b ui.Element, label string, chosen bool) {
	b.Height(34).Padding(0, th.Space(4)).Radius(th.Radius.Pill).Center().Shrink(0).Cursor(ui.CursorPointer).KeepFocus()
	switch {
	case chosen:
		b.Background(th.Color.Text).TextColor(th.Color.Base)
	case b.Hovered():
		b.Background(th.Color.Hover).TextColor(th.Color.Text)
	default:
		b.Background(th.Color.Field).TextColor(th.Color.Text)
	}
	b.Transition(colorFade)
	b.Children(func() { ui.Text(c, label).FontSize(th.Type.Detail).FontWeight(500).SingleLine() })
}

// seekBar thickens and shows its knob only while hovered, pressed or
// focused. Arrow keys and assistive technology move it by step; spoken is
// the value screen readers say.
func seekBar(
	c *ui.Context,
	paint *seekPaint,
	key any,
	pos *float64,
	maxPos float64,
	label, spoken string,
	step float64,
) ui.Element {
	slider := ui.SliderBase(c.Key(key), pos, 0, maxPos).Height(18).PaddingX(seekPad).Label(label).Value(spoken).
		FocusRing(false).Cursor(ui.CursorPointer)
	// Step snaps dragging too; leaving it out while pressed keeps dragging smooth.
	if !slider.Pressed() {
		slider.Step(step)
	}
	paint.pos, paint.maxPos = pos, maxPos
	paint.isFocused = slider.FocusVisible()
	isHot := slider.Hovered() || slider.Pressed() || paint.isFocused
	paint.emphasis = slider.Animate("hot", boolToFloat(isHot), 140*time.Millisecond)
	if paint.fn == nil {
		paint.fn = paint.draw
	}
	slider.Draw(paint.fn)
	return slider
}

const seekPad = 7

// seekPaint draws a seek bar with the values of its last build; each bar
// keeps one, so its draw func is made once.
type seekPaint struct {
	pos       *float64
	maxPos    float64
	isFocused bool
	emphasis  float32
	fn        func(*ui.Painter, ui.Rect)
}

func (s *seekPaint) draw(p *ui.Painter, r ui.Rect) {
	x, w := r.X+seekPad, r.W-2*seekPad
	frac := float32(min(max(*s.pos/s.maxPos, 0), 1))
	emphasis := s.emphasis
	h := 4 + emphasis
	y := r.Y + (r.H-h)/2
	if s.isFocused {
		p.FocusRing(ui.Rect{X: x, Y: y, W: w, H: h}, [4]float32{h / 2, h / 2, h / 2, h / 2})
	}
	p.Fill(ui.Rect{X: x, Y: y, W: w, H: h}, th.Color.Track, h/2)
	p.Fill(ui.Rect{X: x, Y: y, W: w * frac, H: h}, th.Color.Text.Mix(th.Color.Accent, emphasis), h/2)
	if emphasis > 0 {
		knobR := 6 * emphasis
		knob := ui.Rect{X: x + w*frac - knobR, Y: r.Y + r.H/2 - knobR, W: 2 * knobR, H: 2 * knobR}
		p.Shadow(knob, knobR, 0, 1, 4, 0, th.Color.Shadow.Alpha(0.4))
		p.Fill(knob, th.Color.Text, knobR)
	}
}

// eqPeriod is one equalizer cycle; eqStep is how often it moves. A small
// indicator reads as dancing at 30 steps a second, a quarter of the
// display's frames.
const (
	eqPeriod = 1400 * time.Millisecond
	eqStep   = 33 * time.Millisecond
)

// eqBars stand still when paused or when the desktop asks for reduced
// motion. They animate from their draw func, so their frames only paint
// and stop while out of view.
func eqBars(c *ui.Context, playing bool, col ui.Color, size float32) {
	key := eqKey{isDancing: playing && !c.Preferences().ReduceMotion, col: col}
	paint, ok := eqPainters[key]
	if !ok {
		paint = func(p *ui.Painter, r ui.Rect) { paintEq(p, r, key.isDancing, key.col) }
		// Colors change only with the theme; clearing bounds the map.
		if len(eqPainters) >= 64 {
			clear(eqPainters)
		}
		eqPainters[key] = paint
	}
	ui.Box(c).Size(size, size).Shrink(0).Draw(paint)
}

type eqKey struct {
	isDancing bool
	col       ui.Color
}

// eqPainters caches equalizers' draw funcs: a closure per build would
// allocate.
var eqPainters = map[eqKey]func(*ui.Painter, ui.Rect){}

func paintEq(p *ui.Painter, r ui.Rect, isDancing bool, col ui.Color) {
	var phase float64
	if isDancing {
		phase = float64(p.Now().UnixMilli()%eqPeriod.Milliseconds()) / float64(eqPeriod.Milliseconds())
		p.After(eqStep)
	}
	barW := r.W / 5
	rest := [3]float32{0.45, 0.85, 0.6}
	for i := range 3 {
		level := rest[i]
		if isDancing {
			// Whole-number frequencies keep the loop seamless at its wrap.
			freq := float64(i + 1)
			level = 0.3 + 0.7*float32(math.Abs(math.Sin(2*math.Pi*phase*freq+float64(i)*1.1)))
		}
		h := max(r.H*level, barW)
		p.Fill(ui.Rect{X: r.X + float32(i)*barW*2, Y: r.Y + r.H - h, W: barW, H: h}, col, barW/2)
	}
}

// eqDancing is false unless a song plays in a focused, shown window: 30
// frames a second would hold the Metal driver's ~250 MB for as long as a song
// plays in the background.
func (a *app) eqDancing() bool {
	return a.isPlaying() && a.progress.isWatched()
}

func (a *app) thumb(c *ui.Context, s catalog.Song, size, radius float32, hovered bool) {
	isCurrent := s.ID == a.currentID()
	coverArt(c, s.Art, size, radius).Children(func() {
		if !hovered && !isCurrent {
			return
		}
		ui.Box(c).Fill().Center().Radius(radius).Background(th.Color.Shadow.Alpha(0.45)).Children(func() {
			switch {
			case isCurrent && !hovered:
				eqBars(c, a.eqDancing(), th.Color.Light, size*0.34)
			case isCurrent && a.isPlaying():
				ui.Icon(c, icPause).Size(size*0.38, size*0.38).TextColor(th.Color.Light)
			default:
				ui.Icon(c, icPlay).Size(size*0.38, size*0.38).TextColor(th.Color.Light)
			}
		})
	})
}

func pageTitle(c *ui.Context, eyebrowText, title string) {
	ui.Column(c).Gap(th.Space(1.5)).Children(func() {
		eyebrow(c, eyebrowText)
		heading(c, title, th.Type.Page, 1).SingleLine()
	})
}

// sectionHeader reports whether its action was clicked.
func sectionHeader(c *ui.Context, key any, eyebrowText, title, action string) bool {
	clicked := false
	ui.Row(c).AlignItems(ui.End).Gap(th.Space(4)).Margin(0, 0, th.Space(4), 0).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(th.Space(1)).Children(func() {
			if eyebrowText != "" {
				eyebrow(c, eyebrowText)
			}
			heading(c, title, th.Type.Section, 2).SingleLine()
		})
		if action != "" {
			clicked = pill(c, key, nil, action, pillGhost).Height(32).Padding(0, th.Space(3.5)).Clicked()
		}
	})
	return clicked
}

// raise reports whether b is hovered.
func raise(b ui.Element) bool {
	hovered := b.Hovered()
	bg := th.Color.Raised
	if hovered {
		bg = th.Color.Hover
	}
	b.Background(bg)
	return hovered
}

// liftMotion moves hover badges as poses, which only paint: easing a value
// the view reads would rebuild the whole view every frame of the motion.
var liftMotion = ui.Animation{Duration: 180 * time.Millisecond}

// playBadge is the accent play button that rises into view while shown.
// It is built hidden too, so its pose can fade it out.
func playBadge(c *ui.Context, size float32, shown bool) ui.Element {
	hide := 1 - boolToFloat(shown)
	return ui.Box(c).Size(size, size).Radius(size/2).Center().Background(th.Color.Accent).
		Shadow(0, size/6, size*0.4, 0, th.Color.Shadow.Alpha(0.4)).
		AnimatePose(ui.Pose{Y: 8 * hide, Fade: hide}, liftMotion).
		Children(func() {
			ui.Icon(c, icPlay).Size(size*0.4, size*0.4).TextColor(th.Color.AccentText)
		})
}

// mediaCard's size 0 stretches it to its grid cell.
func mediaCard(c *ui.Context, key any, art catalog.Art, title, sub string, size float32, isRound bool) ui.Element {
	card := ui.ButtonBase(c.Key(key)).Column().Gap(th.Space(3)).Justify(ui.Start).AlignItems(ui.Stretch).Shrink(0).
		Cursor(ui.CursorPointer).Label(title).KeepFocus()
	lift := card.Animate("lift", boolToFloat(card.Hovered()), liftMotion.Duration)
	if size > 0 {
		card.Width(size)
	}
	radius := th.Radius.Card
	if isRound {
		radius = th.Radius.Pill
	}
	card.Children(func() {
		// The cover rises in the layout, not as a pose: a cover clips its
		// motif, and MyGo draws a moved clip through a mask texture it keeps.
		artBox := coverArt(c, art, size, radius).Top(-4*lift).
			Shadow(0, 8+10*lift, 28, -4, th.Color.Shadow.Alpha(0.35+0.2*lift))
		artBox.Children(func() {
			play := playBadge(c, 44, card.Hovered())
			if isRound {
				play.Attach(ui.AnchorCenter, ui.AnchorCenter)
			} else {
				play.Attach(ui.AnchorBottomRight, ui.AnchorBottomRight).Right(th.Space(2.5)).Bottom(th.Space(2.5))
			}
		})
		info := ui.Column(c).Gap(th.Space(0.75))
		if isRound {
			info.AlignItems(ui.Center)
		}
		info.Children(func() {
			ui.Text(c, title).FontSize(th.Type.Title).FontWeight(600).SingleLine()
			if sub != "" {
				ui.Text(c, sub).FontSize(th.Type.Sub).TextColor(th.Color.Muted).MaxLines(2)
			}
		})
	})
	return card
}

// shelf is a horizontally scrolling row that bleeds to the panel edges.
// It is one Tab stop; the arrow keys move through its cards.
func shelf(c *ui.Context, key any, items func()) {
	page := th.Layout.Page
	ui.ScrollHorizontal(c.Key(key)).Margin(0, -page).Padding(th.Space(1), page, th.Space(3)).Gap(th.Space(5)).
		FocusGroup(ui.Horizontal).Children(items)
}

func columnsFor(w, minCell float32, minCols, maxCols int) int {
	return min(max(int(w/minCell), minCols), maxCols)
}

// bone is one skeleton placeholder shape; width 0 stretches it.
// Skeletons stay still: a shimmer would draw frames, and hold GPU memory,
// for as long as the network takes.
func bone(c *ui.Context, width, height, radius float32) {
	b := ui.Box(c).Height(height).Shrink(0).Radius(radius).Background(th.Color.Raised)
	if width > 0 {
		b.Width(width)
	}
}

func boneLines(c *ui.Context, titleWidth, subWidth float32) {
	ui.Column(c).Grow(1).Gap(th.Space(2)).Children(func() {
		bone(c, titleWidth, 14, 7)
		bone(c, subWidth, 12, 6)
	})
}

// boneCard stands in for a card: a cover (width 0 stretches it), a title
// line, and a subtitle line subRatio times the cover's height wide.
func boneCard(c *ui.Context, width, height, subRatio float32) {
	ui.Column(c).Gap(th.Space(2.5)).Shrink(0).Children(func() {
		bone(c, width, height, th.Radius.Card)
		bone(c, height*0.7, 14, 7)
		bone(c, height*subRatio, 12, 6)
	})
}

// loadingStatus tells assistive technology that a skeleton is loading.
func loadingStatus(c *ui.Context) ui.Element {
	return ui.Text(c, locales.Loading()).TextColor(th.Color.Muted).Role(ui.RoleStatus)
}
