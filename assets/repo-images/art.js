// Draws a page's scene into <canvas id="art"> (philosophy.md): a halftone
// sun on a horizon, its reflection in rows, the water's rows and the grain.
// The body's data attributes place it, in CSS pixels:
//
//   data-horizon="y"            the horizon, from the top
//   data-sun="x r"              the sun's center on the horizon, its radius
//   data-reflection="n"         the reflection's rows, 7 unless fewer show
//   data-timeline="x0 x1 at"    optional: a line along the horizon from x0
//                               to x1, a song's length (3:47), played in
//                               amber up to at seconds, and ticked
"use strict";
(() => {
  const W = 1280, H = 640;
  const PITCH = 8; // the halftone grid
  const SONG = 227; // 3:47, the placeholder song the captures play

  const data = document.body.dataset;
  const nums = (s) => s.trim().split(/[\s,]+/).map(Number);
  const horizon = Number(data.horizon);
  const [cx, r] = nums(data.sun);
  const reflection = Number(data.reflection || 7);

  const css = getComputedStyle(document.documentElement);
  const color = (name) => css.getPropertyValue(name).trim();
  const amber = color("--amber");

  const canvas = document.getElementById("art");
  const dpr = window.devicePixelRatio || 1;
  canvas.width = W * dpr;
  canvas.height = H * dpr;
  const g = canvas.getContext("2d");
  g.scale(dpr, dpr);
  const hair = 1 / dpr; // one device pixel
  const snap = (v) => Math.round(v * dpr) / dpr + hair / 2;

  const row = (xa, xb, y, width) => {
    g.lineWidth = width;
    g.beginPath();
    g.moveTo(xa, y);
    g.lineTo(xb, y);
    g.stroke();
  };

  // The glow: the faintest warmth of the air around the sun, above the
  // horizon only, so the water reads darker than the sky.
  const glow = g.createRadialGradient(cx, horizon, r * 0.5, cx, horizon, r * 2.4);
  glow.addColorStop(0, color("--art-glow"));
  glow.addColorStop(1, "rgba(255, 166, 48, 0)");
  g.fillStyle = glow;
  g.fillRect(0, 0, W, horizon);

  // The water: a hairline every three pixels, fading toward the viewer.
  g.lineCap = "butt";
  g.strokeStyle = color("--art-water");
  for (let y = horizon + 3; y < H; y += 3) {
    g.globalAlpha = Math.pow(1 - (y - horizon) / (H - horizon), 1.4);
    row(0, W, snap(y), hair);
  }
  g.globalAlpha = 1;

  // The sun: dots on the grid, swelling toward the horizon, then drawn out
  // sideways until they fuse into rows, the light become a queue. The grid
  // is centered on the sun, so it is symmetric.
  g.lineCap = "round";
  g.strokeStyle = g.fillStyle = amber;
  for (let i = 0; ; i++) {
    const h = PITCH / 2 + i * PITCH;
    if (h >= r) break;
    const y = horizon - h;
    const t = h / r; // 0 at the horizon, 1 at the top
    const dot = (PITCH / 2) * (0.08 + 0.6 * Math.pow(1 - t, 1.2));
    if (dot < 0.32) continue;
    // 0 a round dot, 1 touching its neighbours.
    const stretch = Math.min(1, Math.max(0, (0.3 - t) / 0.22));
    const reach = stretch * (PITCH / 2);
    const n = Math.floor((Math.sqrt(r * r - h * h) - dot) / PITCH);
    if (stretch === 1) {
      row(cx - n * PITCH - reach, cx + n * PITCH + reach, y, 2 * dot);
      continue;
    }
    for (let k = -n; k <= n; k++) {
      const x = cx + k * PITCH;
      if (reach > 0) {
        row(x - reach, x + reach, y, 2 * dot);
      } else {
        g.beginPath();
        g.arc(x, y, dot, 0, 2 * Math.PI);
        g.fill();
      }
    }
  }

  // The reflection: rows that shorten, thin and fade as they fall, like
  // the mark's.
  for (let i = 0, y = horizon + 9; i < reflection; i++) {
    const half = r * (0.9 - i * 0.125);
    g.globalAlpha = 1 - i * 0.13;
    row(cx - half, cx + half, y, 3.2 - i * 0.36);
    y += 7 + i * 2.5;
  }
  g.globalAlpha = 1;

  // The timeline: the horizon parted around the sun, played in amber (two
  // device pixels), the rest a hairline; ticks every 10 s, longer every
  // minute and at both ends, none in the sun's glare.
  if (data.timeline) {
    const [x0, x1, at] = nums(data.timeline);
    const x = (s) => x0 + ((x1 - x0) * s) / SONG;
    const yh = snap(horizon);
    const sunL = cx - r - PITCH, sunR = cx + r + PITCH;
    const played = x(at);
    g.lineCap = "butt";
    for (const [a, b] of [[x0, Math.min(x1, sunL)], [Math.max(x0, sunR), x1]]) {
      g.strokeStyle = amber;
      if (Math.min(b, played) > a) row(a, Math.min(b, played), yh + hair / 2, 2 * hair);
      g.strokeStyle = color("--art-line");
      if (b > Math.max(a, played)) row(Math.max(a, played), b, yh, hair);
    }
    g.strokeStyle = color("--art-tick");
    g.lineWidth = hair;
    const tick = (at, up, down) => {
      g.beginPath();
      g.moveTo(snap(at), yh - up);
      g.lineTo(snap(at), yh + down);
      g.stroke();
    };
    for (let s = 10; s < SONG; s += 10) {
      if (x(s) < sunL || x(s) > sunR) tick(x(s), 0, s % 60 === 0 ? 7 : 3);
    }
    tick(x0, 4, 7);
    tick(x1, 4, 7);
  }

  // The grain, from a seeded generator so that every render is the same;
  // on the ground, under the windows.
  let seed = 0x9e3779b9;
  const random = () => {
    seed = (seed + 0x6d2b79f5) | 0;
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
  const grain = document.createElement("canvas");
  grain.width = canvas.width;
  grain.height = canvas.height;
  const gc = grain.getContext("2d");
  const img = gc.createImageData(grain.width, grain.height);
  const [gr, gn, gb] = nums(color("--art-grain"));
  const ga = Number(color("--art-grain-alpha")) * 255;
  for (let p = 0; p < img.data.length; p += 4) {
    img.data[p] = gr;
    img.data[p + 1] = gn;
    img.data[p + 2] = gb;
    img.data[p + 3] = random() * ga;
  }
  gc.putImageData(img, 0, 0);
  g.setTransform(1, 0, 0, 1, 0, 0);
  g.drawImage(grain, 0, 0);
})();
