"""Aoi's full-length art, cut straight from the character sheet's FRONT turnaround.

    python scripts/gen-art/full_from_sheet.py [sheet.png]

The sheet defaults to docs/assets/aoi-character-sheet.webp; the lossless PNG
original gives slightly cleaner edges. No generation: the figure is the sheet's
own, masked off its panel (and the stray hair of the big portrait beside it),
upscaled ~2.9x (Lanczos + light unsharp) and saved as a transparent cut-out at
the old art's size and position (896x1792, figure 1530px tall from y=131), so it
sits on whatever stage the page draws behind it.
"""
import os
import sys

import numpy as np
from PIL import Image, ImageDraw, ImageFilter
from scipy import ndimage

ROOT = os.path.normpath(os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", ".."))
SHEET = sys.argv[1] if len(sys.argv) > 1 else os.path.join(ROOT, "docs", "assets", "aoi-character-sheet.webp")
OUT = os.path.join(ROOT, "web", "public", "play", "aoi", "aoi-full.webp")

CROP = (420, 50, 600, 600)  # the FRONT turnaround on the 1214x1295 sheet
# A generous outline of the figure, drawn on the crop at 3x.
OUTLINE3 = [(230, 40), (320, 40), (395, 150), (400, 300), (450, 350), (485, 500), (508, 690), (498, 820), (470, 860),
            (452, 1390), (472, 1600), (310, 1615), (255, 1615), (140, 1605), (150, 1390), (128, 870), (52, 852),
            (36, 700), (58, 560), (112, 380), (170, 300), (198, 200), (224, 90)]
PANEL = np.array([28, 34, 47.])         # the turnaround panel's navy
TOP, BOTTOM, CENTRE = 40 / 3, 1605 / 3, 272 / 3  # head, soles and centre line in crop pixels
W, H, FIG_H, FIG_Y = 896, 1792, 1530, 131


def main():
    src = Image.open(SHEET).convert("RGB").crop(CROP)
    a = np.asarray(src).astype(float)
    outline = Image.new("L", src.size, 0)
    ImageDraw.Draw(outline).polygon([(x / 3, y / 3) for x, y in OUTLINE3], fill=255)
    inside = np.asarray(outline) > 0

    # Figure-ness by distance from the panel colour; panel pixels joined to
    # the outside are background, dark holes inside the figure are not.
    d = np.sqrt(((a - PANEL) ** 2).sum(2))
    fig = np.clip((d - 8) / 12, 0, 1)
    lab, _ = ndimage.label((d < 14) | ~inside)
    outside = np.isin(lab, list(set(np.unique(lab[~inside])) - {0}))
    alpha = np.where(outside, np.minimum(fig, inside * 1.0), 1.0) * inside
    hard = ndimage.binary_fill_holes(ndimage.binary_closing(alpha > 0.5, iterations=2)) & inside
    alpha = np.maximum(alpha * hard, ndimage.binary_erosion(hard, iterations=1) * 1.0)
    alpha = ndimage.gaussian_filter(alpha, 0.7)

    s = FIG_H / (BOTTOM - TOP)
    big = src.resize((round(src.width * s), round(src.height * s)), Image.LANCZOS)
    big = big.filter(ImageFilter.UnsharpMask(radius=1.6, percent=55, threshold=2))
    mask = Image.fromarray((alpha * 255).astype(np.uint8)).resize(big.size, Image.BICUBIC)

    big.putalpha(mask)
    out = Image.new("RGBA", (W, H), (0, 0, 0, 0))
    out.alpha_composite(big, (round(W / 2 - CENTRE * s), round(FIG_Y - TOP * s)))
    out.save(OUT, quality=88, alpha_quality=90, method=6)
    print("wrote", OUT)


if __name__ == "__main__":
    main()
