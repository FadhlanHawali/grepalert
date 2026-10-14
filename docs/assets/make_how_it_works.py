"""Renders docs/assets/how-it-works.gif, the animation in the README.

    python docs/assets/make_how_it_works.py

Needs Pillow and the Segoe UI / Consolas fonts (Windows); set FONT_DIR to
use another folder with fonts of those file names.
"""
import math
import os

from PIL import Image, ImageDraw, ImageFont

W, H = 980, 500
FPS_MS = 80  # frame duration
FONT_DIR = os.environ.get("FONT_DIR", r"C:\Windows\Fonts")
OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "how-it-works.gif")


def font(name, size):
    return ImageFont.truetype(os.path.join(FONT_DIR, name), size)


F_TITLE = font("segoeuib.ttf", 15)
F_TEXT = font("segoeui.ttf", 13)
F_SMALL = font("segoeui.ttf", 12)
F_BOLD = font("segoeuib.ttf", 13)
F_CAP = font("segoeuib.ttf", 15)
F_MONO = font("consola.ttf", 11)

BG = (246, 247, 249)
PANEL = (255, 255, 255)
BORDER = (223, 227, 234)
TEXT = (29, 35, 48)
MUTED = (107, 115, 133)
ACCENT = (47, 111, 237)
RED = (214, 69, 69)
RED_BG = (252, 233, 233)
GREEN = (31, 157, 85)
GREEN_BG = (226, 245, 234)
CHIP_BG = (229, 237, 253)
GREY_BG = (236, 238, 242)

# ---- layout ----
SERVERS = [(24, 24 + i * 128, 640, 138 + i * 128) for i in range(3)]  # x0, y0, x1, y1
NAMES = ["web-01", "web-02", "web-03"]
GIT = (712, 24, 956, 92)
DASH = (712, 128, 956, 214)
CHAT = (712, 250, 956, 408)
CAPTION = (24, 424, 956, 480)


def agent_pt(i):
    x0, y0, x1, y1 = SERVERS[i]
    return (x1 - 52, (y0 + y1) // 2 + 6)


def center(box):
    return ((box[0] + box[2]) // 2, (box[1] + box[3]) // 2)


# ---- story ----
INFO = [
    "INFO GET /api/orders 200 12ms",
    "INFO pool: 18/50 connections in use",
    "INFO GET /api/items 200 8ms",
    "INFO job report-daily finished",
    "INFO GET /health 200 1ms",
]
MATCH_DS1 = "ERROR Datasource DS1 is suspended because pool exhausted"
MATCH_DS2 = "ERROR Datasource DS2 is suspended because disk full"

CAPTIONS = [
    (0, "1", "Rules live in git. The dashboard syncs them and pushes them to every agent."),
    (42, "2", "Each agent tails its logs. A line matches: the pattern pulls out DS1, the webhook gets your message."),
    (86, "3", "Same problem on many servers? The dashboard sends one alert, not three."),
    (128, "4", "Repeats inside the suppression window are held back: no alert storm."),
]
TOTAL = 172

# Log lines appended per server: (frame, text, kind)
events = {0: [], 1: [], 2: []}
for i in range(3):
    for k, f in enumerate(range(2 + i * 2, TOTAL, 9)):
        events[i].append((f, INFO[(k + i) % len(INFO)], "info"))
events[0].append((46, MATCH_DS1, "match"))
for i in range(3):
    events[i].append((90 + i, MATCH_DS2, "match"))
events[0].append((132, MATCH_DS1, "repeat"))
for i in range(3):  # keep the story's lines from being pushed out by noise
    events[i] = [e for e in events[i] if not (e[2] == "info" and any(abs(e[0] - m[0]) < 5 and m[2] != "info" for m in events[i]))]
    events[i].sort()

# Moving dots: (start, end, from, to, color)
dots = [(6, 18, (center(GIT)[0], GIT[3]), (center(DASH)[0], DASH[1]), ACCENT)]
for i in range(3):
    dots.append((20, 32, (DASH[0], center(DASH)[1]), agent_pt(i), ACCENT))
dots += [(54, 64, agent_pt(0), (DASH[0], center(DASH)[1]), RED), (64, 72, (center(DASH)[0], DASH[3]), (center(CHAT)[0], CHAT[1]), RED)]
for i in range(3):
    dots.append((96, 106, agent_pt(i), (DASH[0], center(DASH)[1]), RED))
dots.append((110, 118, (center(DASH)[0], DASH[3]), (center(CHAT)[0], CHAT[1]), RED))

MESSAGES = [(72, "Critical, DS1 is suspended", None), (118, "Critical, DS2 is suspended", "on 3 hosts: web-01, web-02, web-03")]


def ease(t):
    return 0.5 - 0.5 * math.cos(math.pi * max(0.0, min(1.0, t)))


def panel(d, box, title=None, sub=None):
    d.rounded_rectangle(box, 10, fill=PANEL, outline=BORDER, width=1)
    if title:
        d.text((box[0] + 12, box[1] + 9), title, font=F_TITLE, fill=TEXT)
    if sub:
        d.text((box[0] + 12 + d.textlength(title, font=F_TITLE) + 8, box[1] + 11), sub, font=F_SMALL, fill=MUTED)


def chip(d, xy, text, fg, bg, f=F_SMALL):
    w = d.textlength(text, font=f)
    x, y = xy
    d.rounded_rectangle((x, y, x + w + 14, y + 20), 10, fill=bg)
    d.text((x + 7, y + 3), text, font=f, fill=fg)
    return x + w + 14


def frame(n):
    img = Image.new("RGB", (W, H), BG)
    d = ImageDraw.Draw(img)

    # connectors
    for i in range(3):
        ax, ay = agent_pt(i)
        d.line([(ax + 40, ay), (DASH[0] - 1, center(DASH)[1])], fill=BORDER, width=2)
    d.line([center(GIT)[0], GIT[3], center(DASH)[0], DASH[1]], fill=BORDER, width=2)
    d.line([center(DASH)[0], DASH[3], center(CHAT)[0], CHAT[1]], fill=BORDER, width=2)

    # servers: log tail + agent
    for i, box in enumerate(SERVERS):
        panel(d, box, NAMES[i], "/var/log/app.log")
        x0, y0, x1, y1 = box
        shown = [e for e in events[i] if e[0] <= n][-4:]
        for k, (f, text, kind) in enumerate(shown):
            y = y0 + 36 + k * 19
            age = n - f
            if kind != "info" and age < 40:
                d.rounded_rectangle((x0 + 8, y - 2, x0 + 396, y + 15), 4, fill=RED_BG if kind == "match" else GREY_BG)
            col = RED if kind == "match" else MUTED if kind == "repeat" else TEXT
            d.text((x0 + 14, y), text[:58], font=F_MONO, fill=col)
        # agent badge
        ax, ay = agent_pt(i)
        got_rules = n >= 32
        d.rounded_rectangle((ax - 34, ay - 30, ax + 40, ay + 30), 8, fill=GREEN_BG if got_rules and n < 44 else CHIP_BG, outline=ACCENT, width=1)
        d.text((ax - 22, ay - 24), "agent", font=F_BOLD, fill=ACCENT)
        d.text((ax - 26, ay - 6), "rules v2" if got_rules else "rules v1", font=F_SMALL, fill=GREEN if got_rules else MUTED)
        # what the pattern extracted / suppressed
        cx, cy = x0 + 404, ay - 10
        if i == 0 and 50 <= n < 86:
            chip(d, (cx, cy), "datasource = DS1", ACCENT, CHIP_BG, F_BOLD)
        if 94 <= n < 128:
            chip(d, (cx, cy), "datasource = DS2", ACCENT, CHIP_BG, F_BOLD)
        if i == 0 and n >= 134:
            chip(d, (cx + 14, cy), "suppressed", MUTED, GREY_BG, F_BOLD)

    # git, dashboard, chat
    panel(d, GIT, "GitHub / GitLab")
    d.text((GIT[0] + 12, GIT[1] + 34), "rules.yaml  (pattern + webhook)", font=F_MONO, fill=MUTED)
    if 4 <= n < 20:
        chip(d, (GIT[2] - 80, GIT[1] + 9), "changed", GREEN, GREEN_BG)
    panel(d, DASH, "Dashboard")
    if 18 <= n < 34:
        d.text((DASH[0] + 12, DASH[1] + 34), "pushing rules v2 to 3 agents…", font=F_TEXT, fill=ACCENT)
    elif 98 <= n < 116:
        k = min(3, 1 + (n - 98) // 3)
        d.text((DASH[0] + 12, DASH[1] + 34), f"same alert from {k} host{'s' if k > 1 else ''}…", font=F_TEXT, fill=RED)
        d.text((DASH[0] + 12, DASH[1] + 54), "gathering, then sending once", font=F_SMALL, fill=MUTED)
    else:
        d.text((DASH[0] + 12, DASH[1] + 34), "agents · groups · silences", font=F_TEXT, fill=MUTED)
        d.text((DASH[0] + 12, DASH[1] + 54), "one alert across many hosts", font=F_SMALL, fill=MUTED)

    panel(d, CHAT, "#ops-alerts", "webhook")
    y = CHAT[1] + 38
    for f, text, sub in MESSAGES:
        if n < f:
            continue
        h = 46 if sub else 28
        fresh = n - f < 10
        d.rounded_rectangle((CHAT[0] + 10, y, CHAT[2] - 10, y + h), 8, fill=RED_BG if fresh else (248, 244, 244), outline=RED if fresh else BORDER)
        d.text((CHAT[0] + 20, y + 5), text, font=F_BOLD, fill=TEXT)
        if sub:
            d.text((CHAT[0] + 20, y + 24), sub, font=F_SMALL, fill=MUTED)
        y += h + 8

    # moving dots
    for s, e, a, b, col in dots:
        if s <= n <= e:
            t = ease((n - s) / (e - s))
            x, yy = a[0] + (b[0] - a[0]) * t, a[1] + (b[1] - a[1]) * t
            d.ellipse((x - 7, yy - 7, x + 7, yy + 7), fill=col)
            d.ellipse((x - 3, yy - 3, x + 3, yy + 3), fill=PANEL)

    # caption
    cap = [c for c in CAPTIONS if c[0] <= n][-1]
    d.rounded_rectangle(CAPTION, 10, fill=TEXT)
    d.ellipse((CAPTION[0] + 14, CAPTION[1] + 14, CAPTION[0] + 42, CAPTION[1] + 42), fill=ACCENT)
    num_w = d.textlength(cap[1], font=F_CAP)
    d.text((CAPTION[0] + 28 - num_w / 2, CAPTION[1] + 17), cap[1], font=F_CAP, fill=PANEL)
    d.text((CAPTION[0] + 56, CAPTION[1] + 18), cap[2], font=F_CAP, fill=PANEL)
    return img


def main():
    frames = [frame(n) for n in range(TOTAL)]
    # One shared palette keeps colors steady between frames and the file small.
    palette = frames[120].quantize(colors=96, method=Image.Quantize.MEDIANCUT)
    pal = [f.quantize(palette=palette, dither=Image.Dither.NONE) for f in frames]
    durations = [FPS_MS] * TOTAL
    durations[-1] = 2200  # hold the last frame before looping
    pal[0].save(OUT, save_all=True, append_images=pal[1:], duration=durations, loop=0, optimize=True, disposal=1)
    print(OUT, os.path.getsize(OUT) // 1024, "KB")


if __name__ == "__main__":
    main()
