"""Static covers for the built-in and seeded games (one shared art style).

    python covers.py [id ...]   -> .out/cover-<id>.png and cover-<id>-1.png (best of 2)

Pick one, then export it as 1280x720 WebP q90 to web/public/play/covers/<id>.webp.
Studio-built games get their covers from internal/art at build time instead."""
import sys, pathlib
sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
import ds
from concurrent.futures import ThreadPoolExecutor

STYLE = ("Cinematic digital painting, premium board-game key art, rich detail, dramatic volumetric lighting, "
         "deep navy night palette with electric blue glow and warm ember-orange accents, shallow depth of field, "
         "vivid, family-friendly, wide 16:9 composition, no text anywhere.")
NEG = ("text, letters, words, numbers, typography, caption, title, logo, watermark, signature, UI, frame, border, "
       "people's faces, hands, blurry, low quality, jpeg artifacts, deformed, gore, nsfw")
S = {
 "holdem": "A luxurious poker table at a low three-quarter angle: deep navy felt, two face-down playing cards with ornate blue backs and tall neat stacks of glossy clay chips in ember orange, navy and white in the foreground, five face-down cards in a row glowing softly in the centre, a warm ember lamp overhead and electric blue rim light, a cosy late-night card room softly blurred behind.",
 "tictactoe": "A hand-carved dark wooden tic-tac-toe board on a desk at night, chunky glowing electric-blue X pieces and warm ember-orange O rings made of polished glass, three blue X pieces in a diagonal row lit up brightly, soft bokeh city lights through a window behind.",
 "connect-four": "An upright connect-four frame of translucent navy acrylic seen at an angle, glossy round discs in electric blue and ember orange stacked in the columns, one disc falling into a column with a light trail, four blue discs lined up diagonally glowing, dramatic studio lighting on a dark background.",
 "reversi": "A reversi board with an 8 by 8 grid of deep green-navy squares, round two-sided discs flipping in mid-air from dark navy to bright ice-white with sparks of blue light, a wave of flipped discs rippling across the board, low-angle macro shot with ember backlight.",
 "lantern-market": "A magical night market: paper lanterns in five colours (crimson, gold, jade green, sky blue, violet) hanging in strings over wooden stalls, a fan of illustrated lantern cards and a small pile of wooden tokens on a wooden counter in the foreground, warm ember glow under an electric-blue night sky.",
}
ids = sys.argv[1:] or list(S)
def one(i):
    try:
        return i, ds.t2i("cover-" + i, S[i] + " " + STYLE, negative=NEG, model="wan2.5-t2i-preview", size="1280*720", n=2)
    except BaseException as e:
        return i, "ERR " + str(e)[:300]
with ThreadPoolExecutor(5) as ex:
    for i, r in ex.map(one, ids):
        print(i, r, flush=True)
