"""Generation jobs (each one call). Usage: python gen.py <job> [<job>...]
Raw outputs land in .out/ (git-ignored); post.py turns them into web assets."""
import sys
from PIL import Image
import ds

NEG = ('text, letters, watermark, logo, signature, blurry, lowres, deformed hands, extra fingers, extra limbs, '
       'bad anatomy, distorted face, nsfw, cleavage, revealing clothes')
STYLE = ('semi-realistic anime game character art, detailed painterly rendering, soft cinematic lighting, '
         'night navy and electric blue palette, dark navy background with soft blue bokeh glow, blue rim light')
AV = ('head-and-shoulders portrait avatar, centered, facing the viewer at a slight three-quarter angle, '
      'face in the upper middle of the frame, shoulders at the bottom edge, ')

AVATARS = {
    'ren': 'a calm young Japanese man, a strategist, neat dark slate-grey hair, thin rimless glasses, composed faint smile, '
           'high-collar dark navy long coat with subtle blue trim',
    'mika': 'a fearless show-off young Japanese woman, short tousled crimson-pink hair, confident playful grin, '
            'black and red bomber jacket over a dark top',
    'bram': 'an old sea captain, kind weathered face, full white beard, warm gentle smile, crinkled eyes, '
            'navy captain\'s hat with gold braid, navy pea coat',
    'nova': 'a friendly cute humanoid robot, rounded white and blue armor panels, a smooth dark visor face showing two '
            'glowing cyan eyes like happy arcs, small antenna, cheerful',
    'lin': 'a shy prodigy young Chinese woman, sleek black bob haircut with bangs, soft timid smile, '
           'oversized cream knit cardigan, holding a small deck of playing cards near her chest with both hands',
}


def sheet():
    return Image.open(ds.ROOT / 'docs/assets/aoi-character-sheet.webp').convert('RGB')


def job(name):
    if name.startswith('face2-'):
        return face2_job(name[6:])
    if name.startswith('face-'):
        return face_job(name[5:])
    if name.startswith('outfit-'):
        return outfit_job(name[7:])
    if name in ENH_JOBS:
        return enhance(name, ENH_JOBS[name])
    if name == 'portrait-clean':
        c = sheet().crop((0, 0, 432, 656)); c = c.resize((c.width * 2, c.height * 2), Image.LANCZOS)
        return ds.qedit('portrait-clean', 'Remove all overlaid text from this image: the "HEROES AGENT / YOUR AI AGENT PLAYER" logo at the top-left, '
                        'the Japanese text lines, and the blue handwritten "Player" signature. Fill those areas with the same dark navy background. '
                        'Keep the girl, her face, hair, outfit and everything else exactly unchanged.', [ds.data_url(c)])
    if name == 'card':
        ref = Image.open(ds.OUT / 'portrait-clean.png'); face = Image.open(ds.OUT / 'face-ref.png')
        p = ('Image 1 and image 2 show the same girl. Create a dramatic holographic trading-card illustration of this exact girl, upper body, '
             'same face, large blue-grey eyes, confident playful smile, long black high ponytail with bangs, small glowing blue ring earpiece headset, '
             'black and blue tech jacket worn over a white top. She holds a fanned hand of playing cards up near her shoulder, elegant hand with five correct fingers. '
             'Dark background, strong electric-blue rim light from one side and a warm orange ember glow from below, small glowing embers floating in the air, '
             'cinematic, semi-realistic anime game art, tasteful. No text.')
        return ds.i2i('card', p, [ds.data_url(ref), ds.data_url(face)], negative=NEG, size='1024*1536', n=2)
    if name == 'table':
        p = ('Cinematic wide shot of an empty poker table at night inside a sleek futuristic rooftop lounge. Deep green-blue felt, scattered playing cards '
             'and neat stacks of poker chips, blue neon strips along the walls, warm amber pendant lamp light pooling on the table, city lights through tall windows, '
             'shallow depth of field, moody, no people, ' + STYLE)
        return ds.t2i('table', p, NEG + ', people, person', model='wan2.5-t2i-preview', size='1664*928', n=2)
    if name.startswith('av-'):
        k = name[3:]
        return ds.t2i(name, AV + AVATARS[k] + ', ' + STYLE, NEG, model='wan2.5-t2i-preview', size='1024*1024', n=2)
    raise SystemExit('unknown job ' + name)



ENH = ('Enhance this picture into a sharp, high-resolution, finely detailed version of itself. Keep the composition, the girl, '
       'her face, expression, hair, clothes, colours and lighting exactly the same.')


def enhance(name, box, scale=3):
    c = sheet().crop(box); c = c.resize((c.width * scale, c.height * scale), Image.LANCZOS)
    return ds.qedit(name, ENH, [ds.data_url(c)], negative='blurry, lowres, text, watermark')


ENH_JOBS = {
    'enh-faces1': (858, 66, 1198, 201),    # expressions, top row (Neutral, Smile, Wink), labels excluded
    'enh-faces2': (858, 229, 1198, 362),   # expressions, bottom row (Surprised, Angry, Sad)
    'enh-outfits': (358, 688, 820, 998),   # outfit row, without the label line
    'enh-action': (840, 738, 1017, 1012),
    'enh-city': (0, 1057, 405, 1226),
    'enh-gaming': (447, 1057, 628, 1295),
    'enh-beach': (990, 1057, 1214, 1295),
}


# --- HD expressions: identity from the cleaned portrait, expression from the sheet tile ---
FACE_TILES = {'neutral': (860, 67), 'smile': (976, 67), 'wink': (1091, 67),
              'surprised': (860, 230), 'angry': (976, 230), 'sad': (1091, 230)}
FACE_EXPR = {
    'neutral': 'a calm, gentle neutral expression with a soft closed-mouth hint of a smile, both eyes open',
    'smile': 'a bright, happy open smile showing a little of her upper teeth, cheerful eyes, both eyes open',
    'wink': 'a playful wink: exactly ONE eye closed (the eye on the right side of the picture) and the other eye clearly open, with a cute closed-mouth smile',
    'surprised': 'a surprised expression: wide open eyes, raised eyebrows, small open "o" mouth',
    'angry': 'a cute playful pout: cheeks slightly puffed, lips pushed into a small pout, eyebrows a little furrowed, teasingly annoyed and adorable rather than truly angry, both eyes open',
    'sad': 'a sad expression: eyebrows tilted upward in the middle, slightly glossy downcast eyes, small downturned mouth, both eyes open',
}
FACE_P = ('Image 1 is the character. Image 2 shows the facial expression to use. Draw this exact same girl from image 1 as a head-and-shoulders portrait: '
          'same face, large blue-grey eyes, long black hair in a high ponytail with side-swept bangs, small glowing blue ring earpiece headset clearly visible on her ear, '
          'black high tech collar with blue accents and the top of her black and blue jacket. She has {expr}. '
          'Face centred in the frame and facing the viewer with a slight three-quarter turn, head top near the upper edge, shoulders at the bottom edge. '
          'Plain dark navy background (#0a1020) with a subtle blue rim light on her hair and shoulders. Semi-realistic anime game art, sharp and detailed. No text.')

# --- HD outfits: outfit from the sheet figure, identity from the cleaned portrait ---
OUTFIT_X = {'default': 414, 'casual': 532, 'combat': 648, 'summer': 762}
OUTFIT_DESC = {
    'default': 'black and blue tech jacket worn off the shoulders, white crop top, blue harness straps, black shorts with belt and pouches, thigh strap, white-and-blue sneakers',
    'casual': 'white baseball cap, oversized white-and-grey zip hoodie with small blue patches and plain long sleeves (nothing attached to the sleeves, '
              'no objects floating beside her), one hand on her hip, short black shorts, white sneakers',
    'combat': 'black tactical bodysuit with high collar, black armoured gloves and sleeves, utility belt with pouches, black trousers, black-and-white sneakers',
    'summer': 'black bikini-style sports crop top with a light open white jacket over it, denim shorts, thigh strap, white sneakers',
}
OUTFIT_P = ('Image 1 is the character (face and hair). Image 2 shows the outfit and pose. Draw this exact same girl as a crisp full-body illustration, standing exactly like image 2, '
            'wearing exactly the outfit of image 2: {desc}. Same face as image 1, large blue-grey eyes, long black high ponytail with bangs, small glowing blue ring earpiece headset. '
            'Natural proportions, correct hands, whole body from head to shoes visible with a small margin. Plain deep navy background (#0a1020) with subtle blue rim light. '
            'Semi-realistic anime game art, tasteful. No text.')
FNEG = NEG + ', both eyes closed, cropped head, multiple people, frame, border'


def face_job(name):
    ident = Image.open(ds.OUT / 'portrait-clean.png').convert('RGB')
    x, y = FACE_TILES[name]
    tile = sheet().crop((x + 3, y + 3, x + 103, y + 130)); tile = tile.resize((tile.width * 4, tile.height * 4), Image.LANCZOS)
    return ds.i2i('face-' + name, FACE_P.format(expr=FACE_EXPR[name]), [ds.data_url(ident), ds.data_url(tile)],
                  negative=FNEG, size='1024*1024', n=2)


def outfit_job(name):
    ident = Image.open(ds.OUT / 'face-ref.png').convert('RGB')
    cx = OUTFIT_X[name]
    f = sheet().crop((cx - 56, 690, cx + 56, 995))  # figure fills the canvas height
    f = f.resize((round(f.width * 4.75), round(f.height * 4.75)), Image.LANCZOS)
    canvas = Image.new('RGB', (768, 1536), (20, 27, 41))  # the sheet's own outfit-row colour: no visible panel
    canvas.paste(f, ((768 - f.width) // 2, (1536 - f.height) // 2))
    return ds.i2i('outfit-' + name, OUTFIT_P.format(desc=OUTFIT_DESC[name]), [ds.data_url(ident), ds.data_url(canvas)],
                  negative=FNEG, size='768*1536', n=2)


FACE_P2 = ('Image 1 is the master portrait of this girl. Keep EVERYTHING from image 1 identical: the same person, same semi-realistic soft-shaded rendering style, '
           'same lighting, same dark navy background, same framing and pose, same hair, same glowing blue ring earpiece headset, same black collar and outfit. '
           'Change ONLY her facial expression to match image 2: {expr}. No text.')


def face2_job(name):
    """Second pass: expression edit on top of the chosen smile render, for a consistent set."""
    master = Image.open(ds.OUT / 'face-smile-1.png').convert('RGB')
    x, y = FACE_TILES[name]
    tile = sheet().crop((x + 3, y + 3, x + 103, y + 130)); tile = tile.resize((tile.width * 4, tile.height * 4), Image.LANCZOS)
    return ds.i2i('face2-' + name, FACE_P2.format(expr=FACE_EXPR[name]), [ds.data_url(master), ds.data_url(tile)],
                  negative=FNEG + ', flat cel shading, different outfit', size='1024*1024', n=2)


if __name__ == '__main__':
    for n in sys.argv[1:]:
        print(n, job(n))
