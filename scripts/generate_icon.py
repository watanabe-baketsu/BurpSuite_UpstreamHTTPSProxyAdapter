#!/usr/bin/env python3
"""Generate app icons for Burp Upstream HTTPS Proxy Adapter."""

import math
from PIL import Image, ImageDraw, ImageFilter

SIZE = 1024
C = SIZE // 2


def lerp(c1, c2, t):
    return tuple(int(a + (b - a) * max(0, min(1, t))) for a, b in zip(c1, c2))


def arrow(draw, x1, y, x2, color, width, head):
    draw.line([(x1, y), (x2, y)], fill=color, width=width)
    d = 1 if x2 > x1 else -1
    pts = [
        (x2, y),
        (x2 - d * head, int(y - head * 0.5)),
        (x2 - d * head, int(y + head * 0.5)),
    ]
    draw.polygon(pts, fill=color)


def create_icon():
    S = SIZE * 2
    c = S // 2
    img = Image.new("RGBA", (S, S), (0, 0, 0, 0))
    draw = ImageDraw.Draw(img)

    # Palette
    bg = (16, 16, 28)
    bg_inner = (22, 22, 36)
    blue = (100, 140, 255)
    purple = (155, 85, 240)
    teal = (62, 210, 145)

    # ── Background ──
    m = 50
    draw.rounded_rectangle([m, m, S - m, S - m], radius=400, fill=bg)

    # ── Shield dimensions (large, centered) ──
    sw, sh = 680, 740
    s_top = c - sh // 2 + 20
    s_bot = c + sh // 2 + 20
    s_left = c - sw // 2
    s_right = c + sw // 2
    s_knee = c + sh // 7  # where shield starts narrowing

    shield = [
        (s_left, s_top),
        (s_right, s_top),
        (s_right, s_knee),
        (c, s_bot),
        (s_left, s_knee),
    ]

    # ── Glow behind shield ──
    glow = Image.new("RGBA", (S, S), (0, 0, 0, 0))
    gd = ImageDraw.Draw(glow)
    for i in range(40, 0, -1):
        t = i / 40
        sc = 1 + t * 0.06
        color = lerp(blue, purple, t)
        a = int(10 + 80 * (1 - t))
        pts = [(c + (x - c) * sc, c + (y - c) * sc) for x, y in shield]
        gd.polygon(pts, outline=(*color, a))
    glow = glow.filter(ImageFilter.GaussianBlur(radius=14))
    img = Image.alpha_composite(img, glow)
    draw = ImageDraw.Draw(img)

    # ── Shield solid border ──
    for i in range(8, 0, -1):
        t = i / 8
        sc = 1 + t * 0.008
        color = lerp(blue, purple, 0.2 + t * 0.5)
        a = int(100 + 155 * (1 - t))
        pts = [(c + (x - c) * sc, c + (y - c) * sc) for x, y in shield]
        draw.polygon(pts, outline=(*color, a))

    # ── Shield fill ──
    fs = 0.96
    fill_pts = [(c + (x - c) * fs, c + (y - c) * fs) for x, y in shield]
    draw.polygon(fill_pts, fill=bg_inner)

    # ── Lock icon ──
    lx, ly = c, c + 60
    bw, bh = 200, 150
    # Body
    draw.rounded_rectangle(
        [lx - bw // 2, ly, lx + bw // 2, ly + bh],
        radius=24, fill=blue,
    )
    # Shackle
    shr, shh = 60, 100
    draw.arc(
        [lx - shr, ly - shh, lx + shr, ly + 14],
        start=180, end=360, fill=blue, width=30,
    )
    # Keyhole
    kr = 24
    ky = ly + bh // 3
    draw.ellipse([lx - kr, ky - kr, lx + kr, ky + kr], fill=bg_inner)
    draw.rounded_rectangle([lx - 10, ky + 6, lx + 10, ky + 50], radius=5, fill=bg_inner)

    # ── Arrows through the shield ──
    ay = c + 10
    aw = 22

    # Left arrow: Burp side (blue)
    arrow(draw, 110, ay, s_left - 30, blue, aw, 50)
    draw.ellipse([56, ay - 30, 116, ay + 30], fill=blue)

    # Right arrow: Upstream side (purple)
    arrow(draw, s_right + 30, ay, S - 110, purple, aw, 50)
    draw.ellipse([S - 116, ay - 30, S - 56, ay + 30], fill=purple)

    # ── Activity indicator bars (top of shield) ──
    by = s_top + 80
    bw2, bh2 = 50, 12
    gap = 18
    total_w = 3 * bw2 + 2 * gap
    bx_start = c - total_w // 2
    for i in range(3):
        col = lerp(blue, teal, i / 2)
        x = bx_start + i * (bw2 + gap)
        draw.rounded_rectangle([x, by, x + bw2, by + bh2], radius=6, fill=(*col, 220))

    # ── Downscale ──
    img = img.resize((SIZE, SIZE), Image.LANCZOS)
    return img


def create_tray_template():
    """Generate a 32x32 black-on-transparent template image for the macOS
    menu-bar icon. Template images must be black + alpha only — macOS
    composites the appropriate colour for the current appearance (light/dark).
    """
    S = 256  # work at higher res, downscale at the end
    c = S // 2
    img = Image.new("RGBA", (S, S), (0, 0, 0, 0))
    draw = ImageDraw.Draw(img)

    # Shield silhouette — same proportions as appicon but solid black.
    sw, sh = int(S * 0.78), int(S * 0.86)
    s_top = c - sh // 2 + 6
    s_bot = c + sh // 2 + 6
    s_left = c - sw // 2
    s_right = c + sw // 2
    s_knee = c + sh // 7
    shield = [
        (s_left, s_top), (s_right, s_top),
        (s_right, s_knee), (c, s_bot), (s_left, s_knee),
    ]
    draw.polygon(shield, fill=(0, 0, 0, 255))

    # Punch out a small lock keyhole so the silhouette reads as a security icon.
    kr = int(S * 0.05)
    ky = c + 12
    draw.ellipse([c - kr, ky - kr, c + kr, ky + kr], fill=(0, 0, 0, 0))
    draw.rounded_rectangle(
        [c - kr // 2, ky, c + kr // 2, ky + kr * 2],
        radius=2, fill=(0, 0, 0, 0),
    )

    return img.resize((32, 32), Image.LANCZOS)


def create_tray_regular():
    """Generate a 32x32 colour PNG for Linux/Windows tray (where template
    images are not supported). Uses a flat blue palette to stay legible at
    small sizes; the gradient detail of the full app icon is lost at 32px.
    """
    S = 256
    c = S // 2
    img = Image.new("RGBA", (S, S), (0, 0, 0, 0))
    draw = ImageDraw.Draw(img)

    blue = (100, 140, 255, 255)

    sw, sh = int(S * 0.78), int(S * 0.86)
    s_top = c - sh // 2 + 6
    s_bot = c + sh // 2 + 6
    s_left = c - sw // 2
    s_right = c + sw // 2
    s_knee = c + sh // 7
    shield = [
        (s_left, s_top), (s_right, s_top),
        (s_right, s_knee), (c, s_bot), (s_left, s_knee),
    ]
    draw.polygon(shield, fill=blue)

    kr = int(S * 0.05)
    ky = c + 12
    draw.ellipse([c - kr, ky - kr, c + kr, ky + kr], fill=(0, 0, 0, 0))
    draw.rounded_rectangle(
        [c - kr // 2, ky, c + kr // 2, ky + kr * 2],
        radius=2, fill=(0, 0, 0, 0),
    )

    return img.resize((32, 32), Image.LANCZOS)


def main():
    img = create_icon()

    png = "build/appicon.png"
    img.save(png, "PNG")
    print(f"  {png} ({SIZE}x{SIZE})")

    ico_sizes = [16, 24, 32, 48, 64, 128, 256]
    imgs = [img.resize((s, s), Image.LANCZOS) for s in ico_sizes]
    ico = "build/windows/icon.ico"
    imgs[0].save(ico, format="ICO", sizes=[(s, s) for s in ico_sizes], append_images=imgs[1:])
    print(f"  {ico} ({', '.join(str(s) for s in ico_sizes)})")

    # Tray icons — embedded by the Go side via go:embed. The template image
    # is used on macOS so it auto-adapts to the menu-bar appearance; the
    # regular variant is used on Linux/Windows where templates are ignored.
    tray_template = "build/tray/tray-template.png"
    tray_regular = "build/tray/tray-regular.png"
    import os
    os.makedirs("build/tray", exist_ok=True)
    create_tray_template().save(tray_template, "PNG")
    print(f"  {tray_template} (32x32 template)")
    create_tray_regular().save(tray_regular, "PNG")
    print(f"  {tray_regular} (32x32 regular)")


if __name__ == "__main__":
    main()
