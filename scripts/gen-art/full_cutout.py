"""Aoi's full-length art, cut out of docs/assets/aoi-full-source.webp.

    pip install rembg==2.0.61 onnxruntime scipy
    python scripts/gen-art/full_cutout.py

No generation: the figure is the source image's own, with its background, text and floor
reflection removed. Saved as a transparent cut-out at the old art's size and position
(896x1792, figure 1530px tall from y=131), so it sits on whatever stage the page draws
behind it.
"""
import os

import numpy as np
from PIL import Image
from rembg import new_session, remove
from scipy import ndimage

ROOT = os.path.normpath(os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", ".."))
SOURCE = os.path.join(ROOT, "docs", "assets", "aoi-full-source.webp")
OUT = os.path.join(ROOT, "web", "public", "play", "aoi", "aoi-full.webp")

# The floor shines, so the models keep each shoe's reflection. These are the shoes' lower
# edges, traced by hand on the source as x, y.
SOLES = (
    ((400, 1474), (404, 1480), (412, 1484), (425, 1487), (440, 1489), (460, 1489), (472, 1487),
     (482, 1485), (489, 1481), (492, 1472), (494, 1466), (505, 1463), (512, 1450)),
    ((738, 1450), (748, 1464), (752, 1466), (753, 1480), (757, 1487), (762, 1491), (775, 1493),
     (795, 1495), (815, 1495), (830, 1493), (840, 1490), (842, 1484), (845, 1470), (848, 1450)),
)
FLOOR = 1450  # below this there's only her shoes
W, H, FIG_H, FIG_Y = 896, 1792, 1530, 131


def cut_soles(a):
    below = np.zeros(a.shape)  # how much of each pixel is shoe, below the floor line
    ys = np.arange(a.shape[0])
    for sole in SOLES:
        xs, edge = zip(*sole)
        for x in range(xs[0], xs[-1] + 1):
            below[:, x] = np.clip(np.interp(x, xs, edge) + 0.5 - ys, 0, 1)
    a[FLOOR:] = (a * below)[FLOOR:].astype(np.uint8)
    return a


def main():
    src = Image.open(SOURCE).convert("RGB")
    # One model keeps her flying hair, the other her jacket. Together they miss neither.
    a = np.zeros((src.height, src.width), np.uint8)
    for model in ("isnet-anime", "isnet-general-use"):
        a = np.maximum(a, np.asarray(remove(src, session=new_session(model), only_mask=True, post_process_mask=False)))
    a = cut_soles(a)

    # Only what's joined to her stays, which drops scraps of the signature and the panels.
    lab, _ = ndimage.label(a > 40)
    her = lab == np.argmax(np.bincount(lab[lab > 0]))
    a = np.where(her, a, 0)
    # Blue jacket on a blue wall reads as half see-through, so inside her is solid.
    core = ndimage.binary_erosion(her, iterations=10)
    a = np.where(core, 255, a)

    fig = src.copy()
    fig.putalpha(Image.fromarray(a.astype(np.uint8)))
    x0, y0, x1, y1 = fig.getchannel("A").point(lambda v: 255 if v > 128 else 0).getbbox()
    fig = fig.crop((x0, y0, x1, y1))
    scale = FIG_H / fig.height
    fig = fig.resize((round(fig.width * scale), FIG_H), Image.LANCZOS)
    out = Image.new("RGBA", (W, H), (0, 0, 0, 0))
    out.paste(fig, ((W - fig.width) // 2, FIG_Y))
    out.save(OUT, quality=88, method=6)
    print(OUT, out.size, out.getchannel("A").getbbox(), os.path.getsize(OUT), "bytes")


if __name__ == "__main__":
    main()
