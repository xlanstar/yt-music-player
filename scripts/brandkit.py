#!/usr/bin/env python3
"""Renders the identity into assets/brand/: brandkit.svg (the 3x3 board) and
the mark on dark and light grounds (docs/brand.md). Python's standard
library only."""

import math
import os

OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "assets", "brand")
os.makedirs(OUT, exist_ok=True)

INK, GRAPHITE, BONE, AMBER, DUSK = "#0E0D0B", "#1D1B18", "#EEE7D9", "#FFA630", "#6F7A88"
CANVAS = "#161512"
SANS = "'Inter Display', Inter, 'Helvetica Neue', Arial, sans-serif"
MONO = "'JetBrains Mono', 'SF Mono', Menlo, monospace"

# The mark: five queue rows, left-aligned, whose lengths (1:3:5:3:1) draw a play
# triangle; the middle row, the song playing, carries the accent. 120-unit box.
W, STEP, X0, SPAN = 14, 22, 23, 74


def rows():
    for k in (-2, -1, 0, 1, 2):
        y = 60 + k * STEP
        x1 = X0 + SPAN * (1 - abs(k) / 2.5)
        yield k, y, x1


def mark(x, y, size, fg=BONE, accent=AMBER):
    s = size / 120
    out = [
        f'<g transform="translate({x:.2f} {y:.2f}) scale({s:.4f})" fill="none" stroke-width="{W}" stroke-linecap="round">'
    ]
    for k, ry, x1 in rows():
        out.append(
            f'<line x1="{X0}" y1="{ry}" x2="{x1:.2f}" y2="{ry}" stroke="{accent if k == 0 else fg}"/>'
        )
    out.append("</g>")
    return "".join(out)


SQUIRCLE = "M 924 512 C 924 890.23 890.23 924 512 924 C 133.77 924 100 890.23 100 512 C 100 133.77 133.77 100 512 100 C 890.23 100 924 133.77 924 512 Z"


def app_icon(x, y, size):
    s = size / 1024
    return (
        f'<g transform="translate({x:.2f} {y:.2f}) scale({s:.5f})">'
        f'<path fill="{INK}" d="{SQUIRCLE}"/>'
        f'<path fill="none" stroke="#FFFFFF" stroke-opacity=".08" stroke-width="6" d="{SQUIRCLE}"/>'
        + mark(512 - 270 + 14, 512 - 270, 540)
        + "</g>"
    )


def text(
    x,
    y,
    s,
    size,
    fill,
    family=SANS,
    weight=400,
    anchor="start",
    ls=0,
    opacity: float = 1,
    extra="",
):
    o = f' fill-opacity="{opacity}"' if opacity != 1 else ""
    return (
        f'<text x="{x}" y="{y}" font-family="{family}" font-size="{size}" font-weight="{weight}" '
        f'fill="{fill}"{o} text-anchor="{anchor}" letter-spacing="{ls}"{extra}>{s}</text>'
    )


PW, PH = 592, 344


def label(px, py, name, num, color=DUSK):
    return text(px + 24, py + 34, name.upper(), 11, color, MONO, 500, ls=1.6) + text(
        px + PW - 24, py + 34, num, 11, color, MONO, 500, "end", 1.6
    )


def panel(px, py, fill, body, clip_id):
    return (
        f'<clipPath id="{clip_id}"><rect x="{px}" y="{py}" width="{PW}" height="{PH}" rx="14"/></clipPath>'
        f'<g clip-path="url(#{clip_id})"><rect x="{px}" y="{py}" width="{PW}" height="{PH}" fill="{fill}"/>{body}</g>'
        f'<rect x="{px + 0.5}" y="{py + 0.5}" width="{PW - 1}" height="{PH - 1}" rx="14" fill="none" stroke="#FFFFFF" stroke-opacity=".06"/>'
    )


XS, YS = (48, 664, 1280), (60, 428, 796)
P = [(x, y) for y in YS for x in XS]
parts = []

# 01 Identity: the lockup, nothing else.
x, y = P[0]
parts.append(
    panel(
        x,
        y,
        INK,
        mark(x + 92, y + PH / 2 - 44, 88)
        + text(
            x + 206, y + PH / 2 + 15, "YT Music Player", 44, BONE, weight=600, ls=-1.2
        )
        + label(x, y, "Identity", "01")
        + text(
            x + 24, y + PH - 26, "Native. Light. Plays on.", 12, DUSK, MONO, 400, ls=0.4
        ),
        "c1",
    )
)

# 02 Construction: the rows on their grid, the triangle they draw, the 1:3:5 ratio.
x, y = P[1]
g = ['<g stroke="#FFFFFF" stroke-opacity=".05">']
for i in range(0, PW + 1, 22):
    g.append(f'<line x1="{x + i}" y1="{y}" x2="{x + i}" y2="{y + PH}"/>')
for j in range(0, PH + 1, 22):
    g.append(f'<line x1="{x}" y1="{y + j}" x2="{x + PW}" y2="{y + j}"/>')
g.append("</g>")
size = 220
s = size / 120
mx, my = x + 110, y + PH / 2 - size / 2 + 10
c = [mark(mx, my, size, fg="#46423A", accent="#7A5A2C")]
# triangle the rows' ends draw: apex at the middle row's cap edge
ax = mx + (X0 + SPAN + W / 2) * s
ay = my + 60 * s
top = (mx + (X0 - W / 2) * s, my + (60 - 2.5 * STEP) * s)
bot = (top[0], my + (60 + 2.5 * STEP) * s)
c.append(
    f'<path d="M {top[0]:.1f} {top[1]:.1f} L {ax:.1f} {ay:.1f} L {bot[0]:.1f} {bot[1]:.1f} Z" fill="none" stroke="{AMBER}" stroke-width="1.2" stroke-dasharray="5 5"/>'
)
for k, ry, x1 in rows():
    cy = my + ry * s
    for cx in (mx + X0 * s, mx + x1 * s):
        c.append(
            f'<circle cx="{cx:.1f}" cy="{cy:.1f}" r="{W / 2 * s:.1f}" fill="none" stroke="{BONE}" stroke-opacity=".55" stroke-width="1"/>'
        )
    c.append(
        f'<line x1="{mx + X0 * s:.1f}" y1="{cy:.1f}" x2="{mx + x1 * s:.1f}" y2="{cy:.1f}" stroke="{BONE}" stroke-opacity=".55" stroke-width="1"/>'
    )
    c.append(
        text(
            f"{mx + x1 * s + W / 2 * s + 14:.1f}",
            f"{cy + 4:.1f}",
            {0: "5u", 1: "3u", 2: "1u"}[abs(k)],
            11,
            AMBER if k == 0 else DUSK,
            MONO,
        )
    )
c.append(
    text(
        x + PW - 24,
        y + PH - 52,
        "five rows, one playing",
        13,
        BONE,
        MONO,
        400,
        "end",
        opacity=0.8,
    )
)
c.append(
    text(x + PW - 24, y + PH - 30, "1 : 3 : 5 : 3 : 1", 13, AMBER, MONO, 500, "end", 1)
)
parts.append(
    panel(
        x,
        y,
        "#121110",
        "".join(g) + "".join(c) + label(x, y, "Construction", "02"),
        "c2",
    )
)

# 03 Terminal: ytm, the same player from a shell.
x, y = P[2]
tx, ty, tw, th = x + 40, y + 60, PW - 80, PH - 100
t = [
    f'<rect x="{tx}" y="{ty}" width="{tw}" height="{th}" rx="10" fill="#0A0908" stroke="#FFFFFF" stroke-opacity=".10"/>',
    f'<line x1="{tx}" y1="{ty + 30}" x2="{tx + tw}" y2="{ty + 30}" stroke="#FFFFFF" stroke-opacity=".08"/>',
]
for i in range(3):
    t.append(
        f'<circle cx="{tx + 18 + i * 16}" cy="{ty + 15}" r="4.5" fill="#FFFFFF" fill-opacity=".14"/>'
    )
t.append(text(tx + tw / 2, ty + 19.5, "ytm — zsh", 11, DUSK, MONO, 400, "middle"))
lx = tx + 22
t.append(
    text(lx, ty + 62, f'<tspan fill="{DUSK}">~</tspan> ytm status', 14, BONE, MONO)
)
t.append(mark(lx, ty + 79, 18))
t.append(text(lx + 28, ty + 93, "Low Tide", 14, BONE, MONO, 600))
t.append(text(lx + 28, ty + 115, "Harbor Lights · Night Swim", 14, DUSK, MONO))
bw = 260
t.append(
    f'<rect x="{lx + 28}" y="{ty + 132}" width="{bw}" height="4" rx="2" fill="#FFFFFF" fill-opacity=".12"/>'
)
t.append(
    f'<rect x="{lx + 28}" y="{ty + 132}" width="{bw * 0.56:.0f}" height="4" rx="2" fill="{AMBER}"/>'
)
t.append(text(lx + 28 + bw + 14, ty + 138, "2:14 / 3:58", 12, DUSK, MONO))
t.append(text(lx, ty + 176, f'<tspan fill="{DUSK}">~</tspan>', 14, BONE, MONO))
t.append(f'<rect x="{lx + 18}" y="{ty + 164}" width="9" height="16" fill="{AMBER}"/>')
parts.append(panel(x, y, GRAPHITE, "".join(t) + label(x, y, "Terminal", "03"), "c3"))

# 04 Essence: the one loud panel.
x, y = P[3]
parts.append(
    panel(
        x,
        y,
        AMBER,
        text(x + 36, y + 196, "Plays on.", 92, INK, weight=600, ls=-3.5)
        + text(
            x + 40,
            y + 244,
            "Close the window. The music stays.",
            18,
            INK,
            weight=500,
            opacity=0.72,
        )
        + label(x, y, "Essence", "04", "#5A3A10")
        + mark(x + PW - 24 - 28, y + PH - 24 - 28, 28, fg=INK, accent=INK),
        "c4",
    )
)

# 05 Color.
x, y = P[4]
sw = [
    ("Ink", INK, BONE, 0.28),
    ("Graphite", GRAPHITE, BONE, 0.21),
    ("Bone", BONE, INK, 0.21),
    ("Amber", AMBER, INK, 0.17),
    ("Dusk", DUSK, INK, 0.13),
]
cx = x
b = []
for name, col, fg, frac in sw:
    w = PW * frac
    b.append(
        f'<rect x="{cx:.1f}" y="{y}" width="{w + 1:.1f}" height="{PH}" fill="{col}"/>'
    )
    b.append(text(f"{cx + 18:.1f}", y + PH - 44, name, 15, fg, weight=600))
    b.append(
        text(f"{cx + 18:.1f}", y + PH - 24, col.upper(), 11, fg, MONO, opacity=0.7)
    )
    cx += w
b.append(
    f'<line x1="{x + PW * 0.28}" y1="{y}" x2="{x + PW * 0.28}" y2="{y + PH}" stroke="#FFFFFF" stroke-opacity=".06"/>'
)
parts.append(panel(x, y, INK, "".join(b) + label(x, y, "Color", "05"), "c5"))

# 06 Type.
x, y = P[5]
parts.append(
    panel(
        x,
        y,
        GRAPHITE,
        text(x + 24, y + 214, "Aa", 168, BONE, weight=600, ls=-6)
        + text(x + 300, y + 116, "Inter Display", 22, BONE, weight=600, ls=-0.3)
        + text(x + 300, y + 140, "Titles, names, controls", 13, DUSK)
        + text(x + 300, y + 192, "JetBrains Mono", 22, BONE, MONO, 500, ls=-0.5)
        + text(x + 300, y + 216, "Labels, time, the shell", 13, DUSK)
        + f'<line x1="{x + 24}" y1="{y + 262}" x2="{x + PW - 24}" y2="{y + 262}" stroke="#FFFFFF" stroke-opacity=".08"/>'
        + text(
            x + 24,
            y + 300,
            "abcdefghijklmnopqrstuvwxyz 0123456789",
            17,
            BONE,
            weight=500,
            opacity=0.85,
            ls=0.2,
        )
        + text(x + PW - 24, y + 300, "3:58", 17, AMBER, MONO, 500, "end")
        + label(x, y, "Typography", "06"),
        "c6",
    )
)

# 07 Physical: a record sleeve and the record half out of it.
x, y = P[6]
sx, sy, ss = x + 72, y + 62, 230
rcx, rcy, rr = sx + ss + 34, sy + ss / 2, 104
rec = [f'<circle cx="{rcx}" cy="{rcy}" r="{rr}" fill="#0A0908"/>']
for r in range(44, rr - 4, 5):
    rec.append(
        f'<circle cx="{rcx}" cy="{rcy}" r="{r}" fill="none" stroke="#FFFFFF" stroke-opacity="{0.035 + (r % 3) * 0.015:.3f}" stroke-width="1"/>'
    )
rec.append(f'<circle cx="{rcx}" cy="{rcy}" r="36" fill="{AMBER}"/>')
rec.append(mark(rcx - 15, rcy - 15, 30, fg=INK, accent=BONE))
sleeve = (
    f'<rect x="{sx}" y="{sy}" width="{ss}" height="{ss}" rx="3" fill="{BONE}" filter="url(#shadow)"/>'
    + mark(sx + 26, sy + 26, 56, fg=INK, accent=AMBER)
    + text(sx + 26, sy + ss - 44, "YT Music Player", 15, INK, weight=600, ls=-0.3)
    + text(
        sx + 26,
        sy + ss - 24,
        "SIDE A · PLAYS ON",
        9,
        INK,
        MONO,
        500,
        ls=1.4,
        opacity=0.6,
    )
)
parts.append(
    panel(x, y, "#13120F", "".join(rec) + sleeve + label(x, y, "Object", "07"), "c7")
)

# 08 Image: halftone sun on a night horizon, its reflection drawn in rows.
x, y = P[7]
hz = y + 218
scx, scy, sr = x + 330, y + 214, 104
img = [f'<rect x="{x}" y="{y}" width="{PW}" height="{PH}" fill="url(#sky)"/>']
step = 7
for gy in range(int(scy - sr), int(hz), step):
    for gx in range(int(scx - sr), int(scx + sr) + 1, step):
        ox = gx + (step / 2 if ((gy - int(scy - sr)) // step) % 2 else 0)
        d = math.hypot(ox - scx, gy - scy)
        if d > sr:
            continue
        t_ = (gy - (scy - sr)) / (sr)  # 0 at top, ~1 at horizon
        r = 0.5 + 2.9 * min(1, t_**1.2) * (1 - 0.25 * (d / sr) ** 4)
        img.append(f'<circle cx="{ox:.1f}" cy="{gy:.1f}" r="{r:.2f}"/>')
img.insert(1, f'<g fill="{AMBER}">')
img.append("</g>")
img.append(f'<rect x="{x}" y="{hz}" width="{PW}" height="{PH}" fill="{INK}"/>')
img.append(
    f'<line x1="{x}" y1="{hz}" x2="{x + PW}" y2="{hz}" stroke="{AMBER}" stroke-opacity=".5"/>'
)
ry = hz + 8
gap = 6
i = 0
while ry < y + PH - 6:
    half = sr * (1 - i * 0.09)
    if half <= 6:
        break
    img.append(
        f'<line x1="{scx - half:.1f}" y1="{ry:.1f}" x2="{scx + half:.1f}" y2="{ry:.1f}" stroke="{AMBER}" stroke-width="{max(0.8, 3 - i * 0.28):.2f}" stroke-linecap="round" stroke-opacity="{max(0.12, 0.85 - i * 0.08):.2f}"/>'
    )
    ry += gap
    gap += 2.2
    i += 1
img.append(f'<rect x="{x}" y="{y}" width="{PW}" height="{PH}" fill="url(#scan)"/>')
img.append(
    text(x + 24, y + PH - 26, "After hours", 12, BONE, MONO, 500, ls=0.4, opacity=0.8)
)
parts.append(
    panel(x, y, INK, "".join(img) + label(x, y, "Image", "08", "#8A8F96"), "c8")
)

# 09 System: the player bar, the three programs, the icon at its sizes.
x, y = P[8]
bx, by, bw2, bh = x + 24, y + 62, PW - 48, 72
s9 = [
    f'<rect x="{bx}" y="{by}" width="{bw2}" height="{bh}" rx="12" fill="#0B0A09" stroke="#FFFFFF" stroke-opacity=".08"/>',
    f'<rect x="{bx + 12}" y="{by + 12}" width="48" height="48" rx="6" fill="url(#cover)"/>',
    f'<circle cx="{bx + 36}" cy="{by + 40}" r="11" fill="{AMBER}"/>',
    f'<rect x="{bx + 12}" y="{by + 40}" width="48" height="20" fill="{INK}" fill-opacity=".85"/>',
    text(bx + 74, by + 32, "Low Tide", 15, BONE, weight=600),
    text(bx + 74, by + 52, "Harbor Lights", 13, DUSK),
]
pcx = bx + 330
pcy = by + bh / 2
s9.append(
    f'<path d="M {pcx - 48} {pcy - 7} L {pcx - 60} {pcy} L {pcx - 48} {pcy + 7} Z" fill="{BONE}" fill-opacity=".7"/><rect x="{pcx - 63}" y="{pcy - 7}" width="2.4" height="14" fill="{BONE}" fill-opacity=".7"/>'
)
s9.append(
    f'<circle cx="{pcx}" cy="{pcy}" r="19" fill="{AMBER}"/><rect x="{pcx - 6}" y="{pcy - 7}" width="4" height="14" rx="1" fill="{INK}"/><rect x="{pcx + 2}" y="{pcy - 7}" width="4" height="14" rx="1" fill="{INK}"/>'
)
s9.append(
    f'<path d="M {pcx + 48} {pcy - 7} L {pcx + 60} {pcy} L {pcx + 48} {pcy + 7} Z" fill="{BONE}" fill-opacity=".7"/><rect x="{pcx + 60.6}" y="{pcy - 7}" width="2.4" height="14" fill="{BONE}" fill-opacity=".7"/>'
)
s9.append(
    f'<rect x="{bx + 420}" y="{pcy - 2}" width="100" height="4" rx="2" fill="#FFFFFF" fill-opacity=".12"/><rect x="{bx + 420}" y="{pcy - 2}" width="56" height="4" rx="2" fill="{AMBER}"/>'
)
chx = bx
chy = by + bh + 28
for name, on in (
    ("Desktop", True),
    ("ytm", True),
    ("ytmd", True),
    ("idle · 0 frames", False),
):
    w = 26 + len(name) * 7.4
    s9.append(
        f'<rect x="{chx}" y="{chy}" width="{w:.0f}" height="26" rx="13" fill="none" stroke="{AMBER if on else DUSK}" stroke-opacity="{0.55 if on else 0.5}"/>'
    )
    s9.append(
        f'<circle cx="{chx + 13}" cy="{chy + 13}" r="3" fill="{AMBER if on else DUSK}"/>'
    )
    s9.append(
        text(f"{chx + 22:.0f}", chy + 17.5, name, 11.5, BONE if on else DUSK, MONO)
    )
    chx += w + 10
iy = y + PH - 30
ix = bx
for sz in (64, 40, 24, 16):
    s9.append(app_icon(ix - sz * 100 / 1024, iy - sz + sz * 100 / 1024 - 6, sz))
    ix += sz * 824 / 1024 + 18
s9.append(
    text(
        x + PW - 24, y + PH - 26, "one player, three doors", 12, DUSK, MONO, 400, "end"
    )
)
parts.append(panel(x, y, GRAPHITE, "".join(s9) + label(x, y, "System", "09"), "c9"))

defs = """<defs>
<filter id="shadow" x="-20%" y="-20%" width="140%" height="150%"><feDropShadow dx="0" dy="14" stdDeviation="14" flood-color="#000" flood-opacity=".55"/></filter>
<linearGradient id="sky" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#0C0C10"/><stop offset=".55" stop-color="#1B1712"/><stop offset=".64" stop-color="#3A2510"/></linearGradient>
<linearGradient id="cover" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#26201A"/><stop offset="1" stop-color="#4A2E12"/></linearGradient>
<pattern id="scan" width="3" height="3" patternUnits="userSpaceOnUse"><rect width="3" height="1" fill="#000" fill-opacity=".22"/></pattern>
</defs>"""

board = (
    f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1920 1200" width="1920" height="1200">'
    "<!-- YT Music Player identity board, generated: the mark is five queue rows whose lengths draw a play triangle. -->"
    f'{defs}<rect width="1920" height="1200" fill="{CANVAS}"/>'
    + text(48, 38, "YT MUSIC PLAYER — IDENTITY SYSTEM", 11, DUSK, MONO, 500, ls=2)
    + text(1872, 38, "V1 · PLAYS ON", 11, DUSK, MONO, 500, "end", 2)
    + text(
        48,
        1176,
        "Native. GPU-drawn. Wakes nothing when idle.",
        11,
        DUSK,
        MONO,
        400,
        ls=0.6,
    )
    + text(1872, 1176, "9 / 9", 11, DUSK, MONO, 500, "end", 2)
    + "".join(parts)
    + "</svg>\n"
)
open(f"{OUT}/brandkit.svg", "w").write(board)

open(f"{OUT}/mark.svg", "w").write(
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 120 120" width="120" height="120">'
    "<!-- Five queue rows (1:3:5:3:1) draw a play triangle; the middle row is the song playing. -->"
    + mark(0, 0, 120)
    + "</svg>\n"
)
open(f"{OUT}/mark-light.svg", "w").write(
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 120 120" width="120" height="120">'
    + mark(0, 0, 120, fg=INK, accent=AMBER)
    + "</svg>\n"
)
