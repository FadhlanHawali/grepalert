"""Renders docs/assets/dashboard-down.gif: what an agent does when the
dashboard goes down, and when the agent restarts during the outage.

    python docs/assets/make_outage.py

Shares fonts, colors and helpers with make_how_it_works.py.
"""
import os

from PIL import Image, ImageDraw

from make_how_it_works import (ACCENT, BG, BORDER, CHIP_BG, F_BOLD, F_MONO, F_SMALL, F_TITLE, GREEN, GREEN_BG,
                               GREY_BG, H, MUTED, PANEL, RED, RED_BG, TEXT, W, chip, draw_caption, draw_moving_dots,
                               panel, save_gif)

OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "dashboard-down.gif")

SERVER = (24, 24, 680, 408)
DISK = (40, 62, 400, 146)
AGENT = (416, 62, 664, 146)
LOG = (40, 160, 664, 394)
DASH = (712, 24, 956, 120)
CHAT = (712, 150, 956, 408)
WARN = (183, 121, 31)
DOWN_BG = (240, 241, 244)

CAPTIONS = [
    (0, "1", "Normally alerts go through the dashboard: it combines them across hosts and applies silences."),
    (44, "2", "The dashboard goes down. The agent keeps running from the rules and silences on its own disk."),
    (70, "3", "After 15 s without the dashboard, the agent alerts the webhook directly. Silenced rules stay silent."),
    (112, "4", "The agent restarts while the dashboard is still down. Lines keep arriving meanwhile."),
    (146, "5", "It resumes from its saved position, so the line written during the restart still alerts."),
    (176, "6", "The dashboard is back: the agent reconnects on its own and alerts go through it again."),
]
TOTAL = 216

DOWN_FROM, DOWN_TO = 46, 176          # dashboard unreachable
RESTART_FROM, RESTART_TO = 114, 132   # agent process down

INFO = ["INFO GET /api/orders 200 12ms", "INFO pool: 18/50 connections in use", "INFO GET /api/items 200 8ms",
        "INFO job report-daily finished", "INFO GET /health 200 1ms"]
story = [
    (8, "ERROR Datasource DS1 is suspended because pool exhausted", "match"),
    (74, "ERROR Datasource DS2 is suspended because disk full", "match"),
    (96, "WARN slow query took 2300ms (rule silenced for maintenance)", "silenced"),
    (122, "ERROR Datasource DS3 is suspended because credentials expired", "late"),
    (184, "ERROR Datasource DS4 is suspended because pool exhausted", "match"),
]
lines = [(f, INFO[k % len(INFO)], "info") for k, f in enumerate(range(2, TOTAL, 7))]
lines = [l for l in lines if not any(abs(l[0] - s[0]) < 4 for s in story)] + story
lines.sort()

AG = ((AGENT[0] + AGENT[2]) // 2, AGENT[1])                 # agent top edge (to the dashboard)
AG_R = (AGENT[2], (AGENT[1] + AGENT[3]) // 2)                # agent right edge (direct to webhook)
DASH_IN = (DASH[0], (DASH[1] + DASH[3]) // 2)
DASH_OUT = ((DASH[0] + DASH[2]) // 2, DASH[3])
CHAT_IN = (CHAT[0], AG_R[1] + 80)

dots = [
    (12, 22, AG_R, DASH_IN, RED), (22, 30, DASH_OUT, (DASH_OUT[0], CHAT[1]), RED),
    (78, 90, AG_R, CHAT_IN, RED),
    (150, 162, AG_R, CHAT_IN, RED),
    (188, 198, AG_R, DASH_IN, RED), (198, 206, DASH_OUT, (DASH_OUT[0], CHAT[1]), RED),
]
MESSAGES = [
    (30, "Critical, DS1 is suspended", "via the dashboard"),
    (90, "Critical, DS2 is suspended", "sent directly by web-01"),
    (162, "Critical, DS3 is suspended", "sent directly, after the restart"),
    (206, "Critical, DS4 is suspended", "via the dashboard again"),
]


def dashed(d, a, b, col, on=True):
    n = 18
    for k in range(n):
        if k % 2:
            continue
        t0, t1 = k / n, (k + 1) / n
        d.line([(a[0] + (b[0] - a[0]) * t0, a[1] + (b[1] - a[1]) * t0), (a[0] + (b[0] - a[0]) * t1, a[1] + (b[1] - a[1]) * t1)],
               fill=col if on else BORDER, width=2)


def frame(n):
    img = Image.new("RGB", (W, H), BG)
    d = ImageDraw.Draw(img)
    down = DOWN_FROM <= n < DOWN_TO
    restarting = RESTART_FROM <= n < RESTART_TO
    fallback = down and n >= 70

    # connections
    d.line([AG_R, DASH_IN], fill=BORDER if down else ACCENT, width=2)
    d.line([DASH_OUT, (DASH_OUT[0], CHAT[1])], fill=BORDER, width=2)
    dashed(d, AG_R, CHAT_IN, RED, on=fallback and not restarting)

    panel(d, SERVER, "web-01", "agent connected to a dashboard")

    # what the agent keeps on disk
    d.rounded_rectangle(DISK, 8, fill=(250, 251, 253), outline=BORDER)
    d.text((DISK[0] + 10, DISK[1] + 6), "on this server's disk", font=F_BOLD, fill=TEXT)
    files = [("rules.yaml", "last group rules"), ("state.json", "read position"), ("silences.json", "1 active silence")]
    for k, (name, what) in enumerate(files):
        x = DISK[0] + 10 + k * 118
        lit = 132 + k * 4 <= n < 146 + k * 4 or (k != 1 and 46 <= n < 64)
        d.rounded_rectangle((x, DISK[1] + 30, x + 110, DISK[1] + 74), 6, fill=GREEN_BG if lit else PANEL, outline=GREEN if lit else BORDER)
        d.text((x + 8, DISK[1] + 35), name, font=F_MONO, fill=TEXT)
        d.text((x + 8, DISK[1] + 53), what, font=F_SMALL, fill=GREEN if lit else MUTED)

    # agent
    if restarting:
        d.rounded_rectangle(AGENT, 8, fill=DOWN_BG, outline=BORDER)
        d.text((AGENT[0] + 14, AGENT[1] + 10), "agent", font=F_TITLE, fill=MUTED)
        d.text((AGENT[0] + 14, AGENT[1] + 34), "restarting…", font=F_BOLD, fill=MUTED)
        d.text((AGENT[0] + 14, AGENT[1] + 54), "not reading the log right now", font=F_SMALL, fill=MUTED)
    else:
        d.rounded_rectangle(AGENT, 8, fill=CHIP_BG, outline=ACCENT)
        d.text((AGENT[0] + 14, AGENT[1] + 10), "agent", font=F_TITLE, fill=ACCENT)
        if RESTART_TO <= n < 150:
            d.text((AGENT[0] + 14, AGENT[1] + 34), "started: rules, position and", font=F_SMALL, fill=GREEN)
            d.text((AGENT[0] + 14, AGENT[1] + 50), "silences loaded from disk", font=F_SMALL, fill=GREEN)
        elif down:
            d.text((AGENT[0] + 14, AGENT[1] + 34), "dashboard: unreachable", font=F_BOLD, fill=RED)
            if n < 70:
                d.text((AGENT[0] + 14, AGENT[1] + 54), f"direct alerts in {max(0, 15 - (n - DOWN_FROM) * 15 // 24)} s", font=F_SMALL, fill=MUTED)
            else:
                d.text((AGENT[0] + 14, AGENT[1] + 54), "sending alerts directly", font=F_SMALL, fill=RED)
        else:
            d.text((AGENT[0] + 14, AGENT[1] + 34), "dashboard: connected", font=F_BOLD, fill=GREEN)
            d.text((AGENT[0] + 14, AGENT[1] + 54), "reconnected on its own" if n >= DOWN_TO else "alerts go via the dashboard", font=F_SMALL, fill=MUTED)

    # log tail
    d.rounded_rectangle(LOG, 8, fill=(250, 251, 253), outline=BORDER)
    d.text((LOG[0] + 10, LOG[1] + 6), "/var/log/app.log", font=F_BOLD, fill=TEXT)
    shown = [l for l in lines if l[0] <= n][-9:]
    for k, (f, text, kind) in enumerate(shown):
        y = LOG[1] + 30 + k * 22
        if kind == "late":
            # written while the agent was down: noticed only once it is back
            active = n >= 146
            if n - f < 70:
                d.rounded_rectangle((LOG[0] + 6, y - 2, LOG[2] - 6, y + 15), 4, fill=RED_BG if active else GREY_BG)
            d.text((LOG[0] + 12, y), text, font=F_MONO, fill=RED if active else MUTED)
            note = "caught up after restart" if active else "written while the agent is down"
            if n - f < 70:
                d.text((LOG[2] - 12 - d.textlength(note, font=F_SMALL), y), note, font=F_SMALL, fill=RED if active else MUTED)
            continue
        if kind != "info" and n - f < 40:
            d.rounded_rectangle((LOG[0] + 6, y - 2, LOG[2] - 6, y + 15), 4, fill=GREY_BG if kind == "silenced" else RED_BG)
        col = TEXT if kind == "info" else MUTED if kind == "silenced" else RED
        d.text((LOG[0] + 12, y), text, font=F_MONO, fill=col)
        if kind == "silenced" and n - f < 40:
            d.text((LOG[2] - 60, y), "silenced", font=F_SMALL, fill=MUTED)

    # dashboard
    if down:
        d.rounded_rectangle(DASH, 10, fill=DOWN_BG, outline=BORDER)
        d.text((DASH[0] + 12, DASH[1] + 9), "Dashboard", font=F_TITLE, fill=MUTED)
        chip(d, (DASH[2] - 70, DASH[1] + 8), "down", RED, RED_BG, F_BOLD)
        d.text((DASH[0] + 12, DASH[1] + 36), "not reachable", font=F_SMALL, fill=MUTED)
    else:
        panel(d, DASH, "Dashboard")
        if n >= DOWN_TO:
            chip(d, (DASH[2] - 66, DASH[1] + 8), "back", GREEN, GREEN_BG, F_BOLD)
        d.text((DASH[0] + 12, DASH[1] + 36), "combines alerts across hosts,", font=F_SMALL, fill=MUTED)
        d.text((DASH[0] + 12, DASH[1] + 54), "applies silences, keeps history", font=F_SMALL, fill=MUTED)

    # webhook
    panel(d, CHAT, "#ops-alerts", "webhook")
    y = CHAT[1] + 40
    for f, text, sub in MESSAGES:
        if n < f:
            continue
        fresh = n - f < 10
        direct = "directly" in sub
        d.rounded_rectangle((CHAT[0] + 10, y, CHAT[2] - 10, y + 46), 8, fill=RED_BG, outline=RED if fresh else BORDER)
        d.text((CHAT[0] + 20, y + 5), text, font=F_BOLD, fill=TEXT)
        d.text((CHAT[0] + 20, y + 25), sub, font=F_SMALL, fill=RED if direct else MUTED)
        y += 54

    draw_moving_dots(d, dots, n)
    draw_caption(d, CAPTIONS, n)
    return img


if __name__ == "__main__":
    save_gif([frame(n) for n in range(TOTAL)], OUT, 100)
