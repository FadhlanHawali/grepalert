"""Renders docs/assets/staged-rollout.gif: a rule change going out in
stages, and a bad change stopping itself.

    python docs/assets/make_rollout.py

Shares fonts, colors and helpers with make_how_it_works.py.
"""
import os

from PIL import Image, ImageDraw

from make_how_it_works import (ACCENT, BG, BORDER, CHIP_BG, F_BOLD, F_MONO, F_SMALL, F_TEXT, F_TITLE, GREEN, GREEN_BG,
                               GREY_BG, H, MUTED, PANEL, RED, RED_BG, TEXT, W, chip, draw_caption, draw_moving_dots,
                               panel, save_gif)

OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "staged-rollout.gif")

AGENTS = [(24 + (k % 2) * 330, 24 + (k // 2) * 196, 340 + (k % 2) * 330, 206 + (k // 2) * 196) for k in range(4)]
AGENTS = [(x0, y0, x1 - 10, y1 - 14) for x0, y0, x1, y1 in AGENTS]
NAMES = ["web-01", "web-02", "web-03", "web-04"]
GIT = (712, 24, 956, 84)
DASH = (712, 104, 956, 290)
OPS = (712, 310, 956, 408)
PURPLE = (124, 77, 200)
PURPLE_BG = (240, 232, 250)

CAPTIONS = [
    (0, "1", "A rules change in git goes out in stages: one agent first, the others stay on the current version."),
    (34, "2", "Each stage is watched: alerts per agent on the new version vs the agents still on the old one."),
    (76, "3", "Looks normal, so it moves on: half of the agents, then all of them."),
    (130, "4", "A bad change (v3): its first agent suddenly alerts far more than the others..."),
    (184, "5", "...so the rollout stops by itself: every agent goes back to v2, and ops is told."),
]
TOTAL = 232

# version each agent runs, as (from frame, version)
VERSIONS = {
    0: [(0, "v1"), (28, "v2"), (154, "v3"), (198, "v2")],
    1: [(0, "v1"), (88, "v2")],
    2: [(0, "v1"), (116, "v2")],
    3: [(0, "v1"), (116, "v2")],
}
VCOL = {"v1": (MUTED, GREY_BG), "v2": (ACCENT, CHIP_BG), "v3": (PURPLE, PURPLE_BG)}


def version(i, n):
    return [v for f, v in VERSIONS[i] if f <= n][-1]


def rate(i, n):
    """Alerts per minute an agent shows: steady, except web-01 on v3."""
    base = [2, 3, 2, 1][i]
    if i == 0 and 154 <= n < 198:
        return min(40, base + (n - 154) * 2)
    return base


def agent_in(i):
    x0, y0, x1, y1 = AGENTS[i]
    return (x1, (y0 + y1) // 2)


DASH_L = (DASH[0], (DASH[1] + DASH[3]) // 2)
dots = [(6, 16, ((GIT[0] + GIT[2]) // 2, GIT[3]), ((DASH[0] + DASH[2]) // 2, DASH[1]), ACCENT),
        (18, 28, DASH_L, agent_in(0), ACCENT),
        (78, 88, DASH_L, agent_in(1), ACCENT),
        (106, 116, DASH_L, agent_in(2), ACCENT), (106, 116, DASH_L, agent_in(3), ACCENT),
        (132, 142, ((GIT[0] + GIT[2]) // 2, GIT[3]), ((DASH[0] + DASH[2]) // 2, DASH[1]), PURPLE),
        (144, 154, DASH_L, agent_in(0), PURPLE),
        (188, 198, DASH_L, agent_in(0), ACCENT),
        (198, 206, ((DASH[0] + DASH[2]) // 2, DASH[3]), ((OPS[0] + OPS[2]) // 2, OPS[1]), RED)]


def stage_strip(d, x, y, active, done, color):
    labels = ["1 agent", "50%", "all"]
    for k, lab in enumerate(labels):
        w = d.textlength(lab, font=F_SMALL) + 16
        if k < done:
            fg, bg = GREEN, GREEN_BG
        elif k == active:
            fg, bg = color, PANEL
        else:
            fg, bg = MUTED, PANEL
        d.rounded_rectangle((x, y, x + w, y + 20), 10, fill=bg, outline=fg)
        d.text((x + 8, y + 3), lab, font=F_SMALL, fill=fg)
        x += w + 6


def frame(n):
    img = Image.new("RGB", (W, H), BG)
    d = ImageDraw.Draw(img)
    for i in (1, 3):  # the right column; left-column lines would run under the cards
        d.line([agent_in(i), DASH_L], fill=BORDER, width=2)

    # agents
    for i, box in enumerate(AGENTS):
        v = version(i, n)
        fg, bg = VCOL[v]
        panel(d, box, NAMES[i], "group app-servers")
        x0, y0, x1, y1 = box
        fresh = any(f <= n < f + 10 for f, _ in VERSIONS[i][1:])
        d.text((x0 + 14, y0 + 44), "runs rules", font=F_SMALL, fill=MUTED)
        chip(d, (x0 + 84, y0 + 41), v, fg, GREEN_BG if fresh else bg, F_BOLD)
        if v != "v1" and version(1, n) == "v1" or (i == 0 and v == "v3"):
            d.text((x0 + 130, y0 + 44), "first in this rollout", font=F_SMALL, fill=MUTED)
        r = rate(i, n)
        spike = r > 6
        d.text((x0 + 14, y0 + 80), "alerts per minute", font=F_SMALL, fill=MUTED)
        d.text((x0 + 14, y0 + 98), str(r), font=F_TITLE, fill=RED if spike else TEXT)
        # little bar chart of the last minutes
        for k in range(12):
            m = max(0, n - (11 - k) * 3)
            h = min(46, rate(i, m) * 1.1 + 2)
            bx = x0 + 120 + k * 14
            d.rectangle((bx, y1 - 20 - h, bx + 9, y1 - 20), fill=RED if rate(i, m) > 6 else (198, 210, 236))

    # git
    panel(d, GIT, "GitHub / GitLab")
    if 4 <= n < 30:
        chip(d, (GIT[2] - 110, GIT[1] + 9), "v2 committed", GREEN, GREEN_BG)
    elif 130 <= n < 156:
        chip(d, (GIT[2] - 110, GIT[1] + 9), "v3 committed", PURPLE, PURPLE_BG)
    d.text((GIT[0] + 12, GIT[1] + 34), "rules.yaml", font=F_MONO, fill=MUTED)

    # dashboard: rollout panel
    panel(d, DASH, "Dashboard", "staged rollout")
    x, y = DASH[0] + 12, DASH[1] + 36
    if n < 18:
        d.text((x, y), "all agents on v1", font=F_TEXT, fill=MUTED)
    elif n < 120:
        d.text((x, y), "rolling out v1 → v2", font=F_BOLD, fill=ACCENT)
        stage = 0 if n < 78 else 1 if n < 106 else 2
        stage_strip(d, x, y + 24, stage, stage, ACCENT)
        if 34 <= n < 72 or 92 <= n < 104:
            start, end = (34, 72) if n < 76 else (92, 104)
            left = max(0, 5 - int(5 * (n - start) / (end - start)))
            d.text((x, y + 54), f"watching this stage: {left} min left", font=F_SMALL, fill=TEXT)
            d.text((x, y + 72), "v2: 2.0 alerts/min per agent", font=F_SMALL, fill=MUTED)
            d.text((x, y + 88), "v1: 2.0 alerts/min per agent", font=F_SMALL, fill=MUTED)
        elif 72 <= n < 78 or 104 <= n < 106:
            d.text((x, y + 54), "normal: moving to the next stage", font=F_BOLD, fill=GREEN)
    elif n < 132:
        d.text((x, y), "rolled out: all agents on v2", font=F_BOLD, fill=GREEN)
        stage_strip(d, x, y + 24, 3, 3, ACCENT)
    elif n < 186:
        d.text((x, y), "rolling out v2 → v3", font=F_BOLD, fill=PURPLE)
        stage_strip(d, x, y + 24, 0, 0, PURPLE)
        if n >= 156:
            d.text((x, y + 54), "watching this stage…", font=F_SMALL, fill=TEXT)
            d.text((x, y + 72), f"v3: {rate(0, n)}.0 alerts/min per agent", font=F_SMALL, fill=RED if rate(0, n) > 6 else MUTED)
            d.text((x, y + 88), "v2: 2.0 alerts/min per agent", font=F_SMALL, fill=MUTED)
    else:
        d.text((x, y), "rollout stopped", font=F_BOLD, fill=RED)
        d.text((x, y + 22), "v3 raised 40 alerts/min per agent,", font=F_SMALL, fill=TEXT)
        d.text((x, y + 38), "v2 raised 2.0 (limit: 3x)", font=F_SMALL, fill=TEXT)
        d.text((x, y + 60), "every agent is back on v2" if n >= 198 else "sending web-01 back to v2…", font=F_BOLD, fill=GREEN if n >= 198 else MUTED)
        d.text((x, y + 82), "fix v3, then Try again", font=F_SMALL, fill=MUTED)

    # ops webhook
    panel(d, OPS, "#ops", "webhook")
    if n >= 206:
        fresh = n < 216
        d.rounded_rectangle((OPS[0] + 10, OPS[1] + 36, OPS[2] - 10, OPS[1] + 86), 8, fill=RED_BG, outline=RED if fresh else BORDER)
        d.text((OPS[0] + 20, OPS[1] + 41), "Rollout of v3 stopped", font=F_BOLD, fill=TEXT)
        d.text((OPS[0] + 20, OPS[1] + 61), "alert spike; agents back on v2", font=F_SMALL, fill=MUTED)

    draw_moving_dots(d, dots, n)
    draw_caption(d, CAPTIONS, n)
    return img


if __name__ == "__main__":
    save_gif([frame(n) for n in range(TOTAL)], OUT, 190)
