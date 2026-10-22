package main

import (
	"cmp"
	"context"
	"slices"
	"yt-music-player/internal/job"
	"yt-music-player/internal/notice"
	"yt-music-player/internal/settings"
	"yt-music-player/internal/theme"
	"yt-music-player/locales"

	"github.com/egoist/mygo/ui"
)

type lookState struct {
	isOpen  bool
	dir     settings.Dir
	saver   *settings.Saver
	set     settings.Settings
	users   []settings.UserTheme
	scan    job.Job
	options []themeOption
	choice  string
	chips   [len(layoutChoices)]int
	// problem is the last problem with the theme in use, so it is told
	// once.
	problem string
}

type themeOption struct {
	id, name, problem              string
	base, panel, accent, text, dim ui.Color
	// choose and preview are made with the option, not per build.
	choose  func()
	preview func(*ui.Painter, ui.Rect)
}

// useSettings runs before the first frame; errors wait for the first
// build, as every notice does (tellLater).
func (a *app) useSettings(dir settings.Dir) {
	a.look.dir = dir
	a.look.saver = settings.NewSaver(dir, func(err error) {
		a.post(func() { a.tellLater(lookFailed(notice.TopicSettingsSave, notice.SettingsSaveFailed, err.Error())) })
	})
	set, err := dir.Load()
	if err != nil {
		a.tellLater(lookFailed(notice.TopicSettingsRead, notice.SettingsReadFailed, err.Error()))
	}
	a.look.set = set
	users, err := dir.Themes()
	if err != nil {
		a.tellLater(lookFailed(notice.TopicThemes, notice.ThemesReadFailed, err.Error()))
	}
	a.useThemes(users)
}

// useThemes takes freshly read user themes, reports newly broken ones,
// and reapplies the settings, as the theme in use may have changed.
func (a *app) useThemes(users []settings.UserTheme) {
	for _, userTheme := range users {
		// applyLook reports the theme in use.
		if userTheme.Err == nil || userTheme.File == a.look.set.Theme {
			continue
		}
		prev := slices.IndexFunc(a.look.users, func(old settings.UserTheme) bool { return old.File == userTheme.File })
		wasTold := prev >= 0 && errText(a.look.users[prev].Err) == userTheme.Err.Error()
		if !wasTold {
			a.tellLater(themeFailed(userTheme.File, userTheme.Err))
		}
	}
	a.look.users = users
	a.applyLook()
	if a.look.isOpen {
		a.themeOptions()
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// lookFailed is a failure to read, save or apply the settings or themes;
// a later one of the same topic replaces it.
func lookFailed(topic notice.Topic, title notice.Code, args ...string) notice.Notice {
	return notice.Notice{Kind: notice.KindFailure, Title: title, Detail: notice.Reason, Args: args, Topic: topic}
}

// themeFailed is a failure to read the theme in file, each file's
// replacing its last.
func themeFailed(file string, err error) notice.Notice {
	return lookFailed(notice.TopicTheme+notice.Topic(file), notice.ThemeReadFailed, file, err.Error())
}

func (a *app) applyLook() {
	resolved, themeErr, layoutErr := settings.Resolve(a.look.set, a.look.users)
	a.setTheme(resolved)
	layout := &a.theme.Layout
	a.look.chips = [...]int{int(layout.SidebarSide), int(layout.PanelSide), int(layout.PlayerBar), int(layout.Density)}
	problem := errText(layoutErr)
	if themeErr != nil {
		problem = a.look.set.Theme + ": " + themeErr.Error() + "\n" + problem
	}
	if problem == a.look.problem {
		return
	}
	a.look.problem = problem
	if themeErr != nil {
		n := themeFailed(a.look.set.Theme, themeErr)
		n.Detail = notice.ThemeFallback
		a.tellLater(n)
	}
	if layoutErr != nil {
		a.tellLater(lookFailed(notice.TopicLayout, notice.LayoutFailed, layoutErr.Error()))
	}
}

// openLook rereads the user's themes, which may have changed on disk.
func (a *app) openLook() {
	a.look.isOpen = true
	a.look.choice = cmp.Or(a.look.set.Theme, theme.DefaultID)
	a.themeOptions()
	dir := a.look.dir
	if dir == "" {
		return
	}
	job.Run(
		&a.look.scan,
		a.post,
		func(context.Context) ([]settings.UserTheme, error) { return dir.Themes() },
		func(users []settings.UserTheme, err error) {
			if err != nil {
				a.tellLater(lookFailed(notice.TopicThemes, notice.ThemesReadFailed, err.Error()))
				return
			}
			a.useThemes(users)
		},
	)
}

func (a *app) themeOptions() {
	opts := a.look.options[:0]
	add := func(id, name string, t theme.Theme, err error) {
		colors := &t.Color
		panel, accent, text, dim := colors.Panel, colors.Accent, colors.Text, colors.Muted
		opts = append(opts, themeOption{
			id: id, name: name, problem: errText(err),
			base: colors.Base, panel: panel, accent: accent, text: text, dim: dim,
			choose:  func() { a.chooseTheme(id) },
			preview: func(p *ui.Painter, bounds ui.Rect) { paintThemePreview(p, bounds, panel, accent, text, dim) },
		})
	}
	for _, p := range theme.Presets() {
		add(p.ID, presetName(p), p.Theme(), nil)
	}
	for _, u := range a.look.users {
		add(u.File, u.Name, u.Theme, u.Err)
	}
	a.look.options = opts
}

// presetName says a built-in theme's name in the app's language.
func presetName(p theme.Preset) string {
	switch p.ID {
	case theme.DefaultID:
		return locales.ThemeDefault()
	case "midnight":
		return locales.ThemeMidnight()
	case "forest":
		return locales.ThemeForest()
	case "orchid":
		return locales.ThemeOrchid()
	case "studio":
		return locales.ThemeStudio()
	}
	return p.Name
}

func (a *app) chooseTheme(id string) {
	a.look.set.Theme = id
	a.applyLook()
	a.look.saver.Save(a.look.set)
}

// layoutChoices are the layout's segmented controls, in the order of
// arrangement's fields.
var layoutChoices = [...]struct {
	label func() string
	chips []layoutChip
}{
	{locales.LayoutSidebar, []layoutChip{{"left", locales.LayoutLeft}, {"right", locales.LayoutRight}, {"hidden", locales.LayoutHidden}}},
	{locales.LayoutPanel, []layoutChip{{"left", locales.LayoutLeft}, {"right", locales.LayoutRight}, {"hidden", locales.LayoutHidden}}},
	{locales.LayoutPlayerBar, []layoutChip{{"bottom", locales.LayoutBottom}, {"top", locales.LayoutTop}}},
	{locales.LayoutDensity, []layoutChip{{"comfortable", locales.LayoutComfortable}, {"compact", locales.LayoutCompact}}},
}

// layoutChip is an option of a layout choice: value is what settings keep,
// label what the user reads.
type layoutChip struct {
	value string
	label func() string
}

func arrangement(ar *theme.Arrangement) [len(layoutChoices)]*string {
	return [...]*string{&ar.Sidebar, &ar.Panel, &ar.PlayerBar, &ar.Density}
}

// chooseChip sets layout choice to its chip option. A panel moved to the
// sidebar's side takes it and moves the sidebar across; otherwise
// Normalized would move the panel back, as it keeps the sidebar's side.
func (a *app) chooseChip(choice, option int) {
	layout := &a.look.set.Layout
	field := arrangement(layout)[choice]
	*field = layoutChoices[choice].chips[option].value
	side := theme.Side(option) //nolint:gosec // option indexes a few chips
	takesSidebarSide := field == &layout.Panel && side != theme.Hidden && side == a.theme.Layout.SidebarSide
	if takesSidebarSide {
		layout.Sidebar = (theme.Left + theme.Right - side).String()
	}
	a.arrange()
}

// arrange applies the user's layout and saves the fields the user set,
// normalized as applied.
func (a *app) arrange() {
	a.applyLook()
	applied := theme.ArrangementOf(a.theme.Layout)
	appliedFields := arrangement(&applied)
	for i, field := range arrangement(&a.look.set.Layout) {
		if *field != "" {
			*field = *appliedFields[i]
		}
	}
	a.look.saver.Save(a.look.set)
}

// appearanceSheet builds only while open, as its closures would cost
// every frame otherwise.
func (a *app) appearanceSheet(c *ui.Context) {
	if !a.look.isOpen {
		return
	}
	ty, col := &th.Type, &th.Color
	ui.DialogBase(c, &a.look.isOpen, func(back, panel ui.Element) {
		sheetFrame(back, panel, 600, locales.Appearance()).MaxHeight(780).Gap(th.Space(6))
		sheetHeader(c, locales.Appearance(), locales.AppearanceIntro())
		ui.Scroll(c.Key("appearance-scroll")).Grow(1).MinHeight(0).Gap(th.Space(6)).Children(func() {
			ui.Column(c).Gap(th.Space(3)).Children(func() {
				eyebrow(c, locales.Theme())
				ui.RadioGroup(c, func() {
					for i := range a.look.options {
						a.themeCard(c, &a.look.options[i])
					}
				}).Row().Wrap().Gap(th.Space(2.5)).Label(locales.Theme())
				ui.Text(c, a.themesHint()).
					FontSize(ty.Small).TextColor(col.Faint).Selectable()
			})
			ui.Column(c).Gap(th.Space(3)).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Children(func() {
					eyebrow(c, locales.Layout()).Grow(1)
					if a.look.set.Layout != (theme.Arrangement{}) &&
						pill(c, "reset-layout", nil, locales.UseThemeLayout(), pillOpts{style: pillGhost, size: pillSmall}).
							Clicked() {
						a.look.set.Layout = theme.Arrangement{}
						a.arrange()
					}
				})
				for i := range layoutChoices {
					a.layoutChoice(c, i)
				}
			})
			a.backgroundPlayer(c)
		})
		ui.Row(c).Justify(ui.End).Children(func() {
			if pill(c, "appearance-done", nil, locales.Done(), pillOpts{style: pillLight}).Clicked() {
				a.look.isOpen = false
			}
		})
	})
}

func (a *app) themeCard(c *ui.Context, option *themeOption) {
	id := option.id
	radio := ui.RadioBase(c.Key(id), &a.look.choice, id).Column().AlignItems(ui.Stretch).
		Width(128).Padding(th.Space(1.5)).Gap(th.Space(1.5)).Radius(th.Radius.Card).
		Label(option.name).Cursor(ui.CursorPointer).Disabled(option.problem != "")
	if option.problem != "" {
		radio.Description(option.problem)
	}
	switch {
	case a.look.choice == id:
		radio.Border(2, th.Color.Accent).Background(th.Color.Raised)
	case radio.Hovered() && !radio.IsDisabled():
		radio.Border(1, th.Color.GhostBorder).Background(th.Color.Raised)
	default:
		radio.Border(1, th.Color.Line)
	}
	radio.Transition(colorFade).OnChange(option.choose)
	radio.Children(func() {
		if option.problem != "" {
			ui.Text(c, option.name).FontSize(th.Type.Label).FontWeight(600).SingleLine()
			ui.Text(c, option.problem).FontSize(th.Type.Small).TextColor(th.Color.Error).MaxLines(3)
			return
		}
		ui.Box(c).Height(44).Radius(th.Radius.Small).Background(option.base).Draw(option.preview)
		ui.Text(c, option.name).FontSize(th.Type.Label).FontWeight(600).SingleLine()
	})
}

func paintThemePreview(p *ui.Painter, bounds ui.Rect, panel, accent, text, dim ui.Color) {
	inset, dot := th.Space(1.25), th.Space(3.5)
	inner := ui.Rect{X: bounds.X + inset, Y: bounds.Y + inset, W: bounds.W - 2*inset, H: bounds.H - 2*inset}
	p.Fill(inner, panel, th.Radius.Thumb)
	x := inner.X + th.Space(2)
	p.Fill(ui.Rect{X: x, Y: inner.Y + (inner.H-dot)/2, W: dot, H: dot}, accent, dot/2)
	x += dot + th.Space(2)
	rest := inner.X + inner.W - x
	p.Fill(ui.Rect{X: x, Y: inner.Y + inner.H/2 - th.Space(2), W: rest * 0.7, H: th.Space(1.25)}, text, th.Space(1.25)/2)
	p.Fill(ui.Rect{X: x, Y: inner.Y + inner.H/2 + th.Space(1), W: rest * 0.45, H: th.Space(1)}, dim, th.Space(1)/2)
}

func (a *app) themesHint() string {
	m := &a.memo.themesHint
	if m.stale(a.look.dir) {
		m.v = locales.ThemesHint(a.look.dir.ThemesDir())
	}
	return m.v
}

func (a *app) layoutChoice(c *ui.Context, choice int) {
	label, chips, selected := layoutChoices[choice].label(), layoutChoices[choice].chips, &a.look.chips[choice]
	ui.Row(c).Gap(th.Space(4)).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).FontSize(th.Type.Label).TextColor(th.Color.Muted).Grow(1).MinWidth(0)
		seg := ui.SegmentedBase(c.Key(choice), selected, len(chips))
		seg.Track.Gap(th.Space(1.5)).Label(label).Children(func() {
			for option, opt := range chips {
				segment := seg.Segment(option)
				chip(c, segment, opt.label(), option == *selected)
				segment.OnChange(a.actions.chooseChip[choice][option])
			}
		})
	})
}

// backgroundPlayer is ytmd's settings: starting at login and, on Windows,
// ytm in terminals. A switch waits while its change is made.
func (a *app) backgroundPlayer(c *ui.Context) {
	st := &a.startup
	ui.Column(c).Gap(th.Space(3)).Children(func() {
		eyebrow(c, locales.BackgroundPlayer())
		isBusy := !st.isKnown || st.login.IsRunning()
		if settingSwitch(c, "start-at-login", &st.atLogin, locales.StartAtLogin(), locales.StartAtLoginHint(), isBusy) {
			a.setAtLogin(st.atLogin)
		}
		if !st.hasTerminal {
			return
		}
		isBusy = st.terminal.IsRunning()
		if settingSwitch(c, "ytm-in-terminal", &st.inTerminal, locales.YtmInTerminal(), locales.YtmInTerminalHint(), isBusy) {
			a.setInTerminal(st.inTerminal)
		}
	})
}

// settingSwitch is a row that switches *on, its label and hint before the
// switch's track; it reports whether the user switched it.
func settingSwitch(c *ui.Context, key string, on *bool, label, hint string, isDisabled bool) bool {
	ty, col := &th.Type, &th.Color
	sw := ui.SwitchBase(c.Key(key), on).Gap(th.Space(4)).AlignItems(ui.Center).Label(label).Description(hint).
		Disabled(isDisabled).Cursor(ui.CursorPointer)
	track, knob := col.Track, col.Muted
	if *on {
		track, knob = col.Accent, col.AccentText
	}
	if sw.IsDisabled() {
		sw.Opacity(0.6)
	}
	sw.Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(th.Space(0.5)).Children(func() {
			ui.Text(c, label).FontSize(ty.Label).TextColor(col.Text)
			ui.Text(c, hint).FontSize(ty.Small).TextColor(col.Faint)
		})
		row := ui.Row(c).Size(40, 24).Shrink(0).Padding(0, 3).AlignItems(ui.Center).Radius(12).Background(track).
			Transition(colorFade)
		if *on {
			row.Justify(ui.End)
		}
		row.Children(func() { ui.Box(c).Size(18, 18).Radius(9).Background(knob) })
	})
	return sw.Changed()
}
