"""Build every web asset from the character sheet and the raw generations in .out/.
Usage: python post.py   (no network; run gen.py first for the generated sources)."""
import pathlib

from PIL import Image, ImageFilter

import ds

SRC = ds.ROOT / 'docs/assets/aoi-character-sheet.webp'
PUB = ds.ROOT / 'web/public'
AOI = PUB / 'play/aoi'
AG = PUB / 'play/agents'
RAW = ds.OUT
AOI.mkdir(parents=True, exist_ok=True)
AG.mkdir(parents=True, exist_ok=True)
sheet = Image.open(SRC).convert('RGB')


def up(img, scale):
    """Lanczos upscale in 2x steps with a mild unsharp mask after each step."""
    while scale > 1:
        s = min(2, scale)
        img = img.resize((round(img.width * s), round(img.height * s)), Image.LANCZOS)
        img = img.filter(ImageFilter.UnsharpMask(radius=1.6, percent=55, threshold=2))
        scale /= s
    return img


def save(img, path, q=84, limit_kb=None):
    path = pathlib.Path(path)
    while True:
        img.save(path, 'WEBP', quality=q, method=6)
        kb = path.stat().st_size / 1024
        if not limit_kb or kb <= limit_kb or q <= 50:
            break
        q -= 4
    print(f'{path.relative_to(ds.ROOT)}  {img.width}x{img.height}  {kb:.0f}KB  q{q}')


# --- generated, reference-image based ---------------------------------------
save(Image.open(RAW / 'full-w4.png').convert('RGB'), AOI / 'aoi-full.webp', 90, 440)
save(Image.open(RAW / 'portrait-clean.png').convert('RGB'), AOI / 'aoi-portrait.webp', 90, 300)
save(Image.open(RAW / 'card.png').convert('RGB'), AOI / 'aoi-card.webp', 90, 340)

# --- crops of the sheet ------------------------------------------------------
save(sheet, AOI / 'aoi-sheet.webp', 80, 400)

TILES_X = [860, 976, 1091]
TILES_Y = [67, 230]
# HD expressions (wan2.5-i2i). Same square crop for all six so the set lines up.
FACE_PICK = {'neutral': 'face2-neutral', 'smile': 'face-smile-1', 'wink': 'face2-wink-1',
             'surprised': 'face2-surprised', 'angry': 'face2-angry-1', 'sad': 'face2-sad'}
FACE_BOX = (50, 40, 910, 900)
for name, f in FACE_PICK.items():
    im = Image.open(RAW / f'{f}.png').convert('RGB').crop(FACE_BOX).resize((768, 768), Image.LANCZOS)
    save(im, AOI / f'aoi-face-{name}.webp', 85, 88)

# HD outfits (wan2.5-i2i), 768x1536
OUTFIT_PICK = {'default': 'outfit-default', 'casual': 'outfit-casual', 'combat': 'outfit-combat', 'summer': 'outfit-summer'}
for name, f in OUTFIT_PICK.items():
    save(Image.open(RAW / f'{f}.png').convert('RGB'), AOI / f'aoi-outfit-{name}.webp', 85, 150)

save(up(sheet.crop((840, 738, 1017, 1012)), 3), AOI / 'aoi-action.webp', 85, 150)
# bottom strip: captions cropped away; the third (weapon) scene is deliberately skipped
save(up(sheet.crop((0, 1057, 405, 1226)), 3), AOI / 'scene-city.webp', 84, 200)
save(up(sheet.crop((447, 1057, 628, 1295)), 3), AOI / 'scene-gaming.webp', 84, 200)
save(up(sheet.crop((990, 1057, 1214, 1295)), 3), AOI / 'scene-beach.webp', 84, 200)

# --- avatars 512x512 ---------------------------------------------------------
portrait = Image.open(RAW / 'portrait-clean.png').convert('RGB')
save(portrait.crop((140, 50, 832, 742)).resize((512, 512), Image.LANCZOS), AG / 'aoi.webp', 84, 58)
PICK = {'ren': 'av-ren-1', 'mika': 'av-mika-1', 'bram': 'av-bram-1', 'nova': 'av-nova-1', 'lin': 'av-lin-1'}
for k, f in PICK.items():
    im = Image.open(RAW / f'{f}.png').convert('RGB')
    save(im.resize((512, 512), Image.LANCZOS), AG / f'{k}.webp', 84, 58)

# --- table scene 1920x1080 ---------------------------------------------------
t = Image.open(RAW / 'table.png').convert('RGB')
t = t.resize((1937, 1080), Image.LANCZOS).filter(ImageFilter.UnsharpMask(1.4, 40, 2))
save(t.crop((8, 0, 1928, 1080)), PUB / 'play/scene-table.webp', 82, 400)

# --- favicons from the HD Smile expression ----------------------------------
FAV_BOX = (270, 250, 690, 670)  # tight on the face, inside the 1024 render
fav = Image.open(RAW / 'face-smile-1.png').convert('RGB').crop(FAV_BOX)
fav.resize((64, 64), Image.LANCZOS).save(PUB / 'favicon.png', optimize=True)
fav.resize((180, 180), Image.LANCZOS).save(PUB / 'apple-touch-icon.png', optimize=True)
for f in ['favicon.png', 'apple-touch-icon.png']:
    p = PUB / f
    print(f'web/public/{f}  {Image.open(p).size}  {p.stat().st_size / 1024:.0f}KB')
