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


if __name__ == '__main__':
    for n in sys.argv[1:]:
        print(n, job(n))
