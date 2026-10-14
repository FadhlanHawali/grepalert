"""Renders docs/assets/standalone.gif: one agent on its own, no dashboard.

    python docs/assets/make_standalone.py

Shares fonts, colors and helpers with make_how_it_works.py.
"""
import os

from PIL import Image, ImageDraw

from make_how_it_works import (ACCENT, BG, BORDER, CAPTION, CHIP_BG, F_BOLD, F_MONO, F_SMALL, F_TEXT, F_TITLE, GREEN,
                               GREEN_BG, GREY_BG, H, MUTED, PANEL, RED, RED_BG, TEXT, W, chip, draw_caption,
                               draw_moving_dots, panel, save_gif)

OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "standalone.gif")

SERVER = (24, 24, 700, 408)
RULES = (40, 62, 524, 204)
AGENT = (540, 62, 684, 204)
LOG = (40, 218, 684, 394)
CHAT = (740, 24, 956, 408)
YELLOW_BG = (253, 243, 214)
WARN = (183, 121, 31)

RULES_V1 = [
    "rules:",
    "  - name: datasource-suspended",
    "    pattern: 'Datasource (?P<ds>\\S+) is suspended'",
    "    severity: critical",
    "    suppress: {window: 10m, group_by: ds}",
]
RULES_ADDED = [
    "  - name: disk-full",
    "    pattern: 'disk (?P<mount>\\S+) is (?P<pct>\\d+)% full'",
]

CAPTIONS = [
    (0, "1", "No dashboard needed: the agent reads its rules from a file on the server."),
    (30, "2", "A line matches: the pattern pulls out DS1 and the agent posts straight to the webhook."),
    (70, "3", "Edit rules.yaml and save: the agent reloads it within seconds, no restart."),
    (124, "4", "Repeats inside the window are held back, then summed up once when it closes."),
]
TOTAL = 176

INFO = ["INFO GET /api/orders 200 12ms", "INFO pool: 18/50 connections in use", "INFO GET /api/items 200 8ms",
        "INFO job report-daily finished", "INFO GET /health 200 1ms"]
DS1 = "ERROR Datasource DS1 is suspended because pool exhausted"
DISK = "WARN disk /data is 97% full"
lines = [(f, INFO[k % len(INFO)], "info") for k, f in enumerate(range(2, TOTAL, 8))]
story = [(34, DS1, "match"), (98, DISK, "match"), (128, DS1, "repeat"), (138, DS1, "repeat")]
lines = [l for l in lines if not any(abs(l[0] - s[0]) < 4 for s in story)] + story
lines.sort()

def mid_right(b):
    return (b[2], (b[1] + b[3]) // 2)

AGENT_OUT = mid_right(AGENT)
dots = [
    (4, 16, mid_right(RULES), (AGENT[0], 120), ACCENT),
    (40, 54, AGENT_OUT, (CHAT[0], AGENT_OUT[1]), RED),
    (80, 92, mid_right(RULES), (AGENT[0], 120), ACCENT),
    (102, 114, AGENT_OUT, (CHAT[0], AGENT_OUT[1]), WARN),
    (152, 164, AGENT_OUT, (CHAT[0], AGENT_OUT[1]), RED),
]
MESSAGES = [
    (54, "Critical, DS1 is suspended", None, RED, RED_BG),
    (114, "Warning, /data is 97% full", None, WARN, YELLOW_BG),
    (164, "Critical, DS1 is suspended", "+2 more held back in the last 10m", RED, RED_BG),
]


def frame(n):
    img = Image.new("RGB", (W, H), BG)
    d = ImageDraw.Draw(img)
    d.line([AGENT_OUT, (CHAT[0], AGENT_OUT[1])], fill=BORDER, width=2)
    panel(d, SERVER, "web-01", "a single server: agent + rules file, no dashboard")

    # rules file
    edited = n >= 74
    d.rounded_rectangle(RULES, 8, fill=(250, 251, 253), outline=BORDER)
    d.text((RULES[0] + 10, RULES[1] + 6), "/etc/grepalert/rules.yaml", font=F_BOLD, fill=TEXT)
    if 74 <= n < 96:
        chip(d, (RULES[2] - 66, RULES[1] + 5), "saved", GREEN, GREEN_BG)
    body = RULES_V1 + (RULES_ADDED if edited else [])
    for k, text in enumerate(body):
        y = RULES[1] + 28 + k * 15
        if k >= len(RULES_V1) and n < 110:
            d.rectangle((RULES[0] + 6, y - 1, RULES[2] - 6, y + 13), fill=GREEN_BG)
        d.text((RULES[0] + 12, y), text, font=F_MONO, fill=GREEN if k >= len(RULES_V1) else MUTED)

    # agent
    d.rounded_rectangle(AGENT, 8, fill=CHIP_BG, outline=ACCENT)
    d.text((AGENT[0] + 14, AGENT[1] + 10), "agent", font=F_TITLE, fill=ACCENT)
    if n < 16:
        status, col = "starting…", MUTED
    elif n < 92:
        status, col = "1 rule loaded", TEXT
    else:
        status, col = "reloaded: 2 rules", GREEN
    d.text((AGENT[0] + 14, AGENT[1] + 34), status, font=F_SMALL, fill=col)
    d.text((AGENT[0] + 14, AGENT[1] + 52), "no restart" if 92 <= n < 124 else "tailing app.log", font=F_SMALL, fill=MUTED)
    d.text((AGENT[0] + 14, AGENT[1] + 70), "memory: 11 MB", font=F_SMALL, fill=MUTED)
    held = sum(1 for f, _, k in story if k == "repeat" and f <= n) if n < 152 else 0
    if held:
        chip(d, (AGENT[0] + 12, AGENT[3] - 32), f"held back: {held}", MUTED, GREY_BG, F_BOLD)
    elif 38 <= n < 70:
        chip(d, (AGENT[0] + 12, AGENT[3] - 32), "ds = DS1", ACCENT, PANEL, F_BOLD)
    elif 100 <= n < 124:
        chip(d, (AGENT[0] + 8, AGENT[3] - 32), "pct = 97", WARN, PANEL, F_BOLD)

    # log tail
    d.rounded_rectangle(LOG, 8, fill=(250, 251, 253), outline=BORDER)
    d.text((LOG[0] + 10, LOG[1] + 6), "/var/log/app.log", font=F_BOLD, fill=TEXT)
    shown = [l for l in lines if l[0] <= n][-7:]
    for k, (f, text, kind) in enumerate(shown):
        y = LOG[1] + 30 + k * 20
        if kind != "info" and n - f < 40:
            bg = GREY_BG if kind == "repeat" else (YELLOW_BG if text.startswith("WARN") else RED_BG)
            d.rounded_rectangle((LOG[0] + 6, y - 2, LOG[2] - 6, y + 15), 4, fill=bg)
        col = MUTED if kind in ("info", "repeat") else (WARN if text.startswith("WARN") else RED)
        d.text((LOG[0] + 12, y), text, font=F_MONO, fill=TEXT if kind == "info" else col)
        if kind == "repeat" and n - f < 40:
            d.text((LOG[2] - 84, y), "suppressed", font=F_SMALL, fill=MUTED)

    # webhook
    panel(d, CHAT, "#ops-alerts", "webhook")
    y = CHAT[1] + 40
    for f, text, sub, fg, bg in MESSAGES:
        if n < f:
            continue
        h = 46 if sub else 28
        fresh = n - f < 10
        d.rounded_rectangle((CHAT[0] + 10, y, CHAT[2] - 10, y + h), 8, fill=bg, outline=fg if fresh else BORDER)
        d.text((CHAT[0] + 20, y + 5), text, font=F_BOLD, fill=TEXT)
        if sub:
            d.text((CHAT[0] + 20, y + 24), sub, font=F_SMALL, fill=MUTED)
        y += h + 8

    draw_moving_dots(d, dots, n)
    draw_caption(d, CAPTIONS, n)
    return img


if __name__ == "__main__":
    save_gif([frame(n) for n in range(TOTAL)], OUT, 120)
