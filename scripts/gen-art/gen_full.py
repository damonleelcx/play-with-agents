"""Aoi full body: wan2.5-i2i-preview with the FRONT turnaround (3x Lanczos, on a seamless navy canvas) + the cleaned portrait face as references.
Run after `python gen.py portrait-clean`. Output .out/full-w4.png (+ -1 alternative)."""
from PIL import Image, ImageFilter, ImageDraw
import ds
sheet=Image.open(ds.ROOT/'docs/assets/aoi-character-sheet.webp').convert('RGB')
f=sheet.crop((436,58,586,594))
d=ImageDraw.Draw(f); d.rectangle((0,360,2,536),fill=(26,32,45))  # stray portrait hair at left edge
f=f.resize((f.width*3,f.height*3),Image.LANCZOS).filter(ImageFilter.UnsharpMask(2,60,2))
W,H=896,1792
bg=Image.new('RGB',(W,H),(14,20,36))
m=Image.new('L',f.size,0); ImageDraw.Draw(m).rectangle((30,30,f.width-30,f.height-30),fill=255); m=m.filter(ImageFilter.GaussianBlur(25))
bg.paste(f,((W-f.width)//2,(H-f.height)//2),m); bg.save(ds.OUT/'front-in2.png')
face=Image.open(ds.OUT/'portrait-clean.png').crop((280,40,800,600)); face.save(ds.OUT/'face-ref.png')
P=('Image 1 shows the character\'s full outfit and pose; image 2 shows her face. Create a crisp, high-resolution full-body illustration of this same girl '
 'standing in a relaxed front view exactly like image 1, with the face, eyes and expression of image 2 (gentle smile, large dark blue-grey eyes, long black high ponytail with side-swept bangs, '
 'small glowing blue ring earpiece headset). Same outfit as image 1: black and blue tech jacket worn off the shoulders, white crop top, blue harness straps, black shorts with belt and pouches, thigh strap, white-and-blue sneakers. '
 'Natural anatomy and proportions, correct hands. Semi-realistic anime game-art style, soft blue rim light. The whole background is one seamless uniform deep navy color (#0a1020) with no panels, frames or shapes. No text.')
N='blurry, text, watermark, extra limbs, deformed hands, elongated legs, glow outline on skin, headphones, frame, panel, border, rectangle'
print(ds.i2i('full-w4',P,[ds.data_url(bg),ds.data_url(face)],negative=N,size='896*1792',n=2))
