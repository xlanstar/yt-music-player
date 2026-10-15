# Themes

Appearance (the palette button in the top bar, or ⌘, / Ctrl+,) picks a theme and arranges the layout. Choices apply at once and are kept in `settings.json` in the player's config directory (`~/Library/Application Support/yt-music-player` on macOS).

Your own themes are JSON files in its `themes/` directory, listed by file name. Each part is optional:

- `name`: shown in Appearance; the file's name otherwise.
- `base`: a preset to start from: `default`, `midnight`, `forest`, `orchid` or `studio`.
- `seed`: `accent`, `base` and `text` colors from which every color is derived anew.
- `color`, `contrast`: single colors, by the names of `theme.Colors` and `theme.Contrast` in lower camel case (`accentHover`, `muted`, …).
- `type` (6–96) and `radius` (0–999): sizes in DIPs, named likewise; `unit` (2–8, 4 by default): the spacing unit, in DIPs, which every gap and padding is a multiple of.
- `layout`: `sidebar` and `panel` (`left`, `right`, `hidden`), `playerBar` (`bottom`, `top`), `density` (`comfortable`, `compact`), `sizes` (`row`, `rowArt`, `card`, `smallCard`, `cell`) and `regions` (`sidebar`, `panel`, `player`, `header`, `gutter`, `page`, `panelRadius`).

Colors are `#RGB`, `#RRGGBB`, `#RRGGBBAA` or `oklch(L C H)` with an optional `/ A`; colors outside sRGB are clamped into it. The player is always dark, as album art brings the color: a seed's base is darkened to it, and text too dim to keep 4.5:1 on the surfaces is lightened; surfaces too light for any text to keep it are rejected. Unknown fields, bad colors and sizes out of bounds are errors, which Appearance shows by the file, and the default theme is used instead.

Example ([`internal/theme/testdata/dusk.json`](../internal/theme/testdata/dusk.json)):

```json
{
  "name": "Dusk",
  "base": "default",
  "seed": { "accent": "#7AA2F7", "base": "oklch(0.17 0.03 275)" },
  "color": { "rise": "oklch(0.8 0.15 160)", "badge": "#FFFFFF24" },
  "radius": { "card": 8, "large": 12 },
  "layout": { "sidebar": "right", "density": "compact", "regions": { "panel": 360 } }
}
```

`theme.Encode` writes a theme out whole, every token named, as a start for one of your own.
