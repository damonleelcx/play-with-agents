# Art credits

Source: `docs/assets/aoi-character-sheet.webp`, Aoi's official character sheet.
Generation: DashScope (Alibaba Model Studio). Scripts are in `scripts/gen-art/`.
Run them in this order: `gen.py portrait-clean`, `gen_full.py`, `gen.py card table av-ren av-mika av-bram av-nova av-lin`, `post.py`.
The key comes from `PLAY_IMAGE_API_KEY`. In total, 17 generation calls were made.

| File | How it was made |
|---|---|
| aoi/aoi-full.webp | **wan2.5-i2i-preview** (reference model, 896x1792). The references were the FRONT turnaround crop (sheet 436,58–586,594, 3x Lanczos, on a seamless navy canvas) and the face from the cleaned portrait. The prompt asked for the same girl and outfit, standing front view, deep navy (#0a1020) background and blue rim light. |
| aoi/aoi-portrait.webp | **qwen-image-edit** on the left portrait (sheet 0,0–432,656, 2x). The instruction removed the HEROES AGENT logo, the Japanese text and the "Player" signature. Everything else was kept. |
| aoi/aoi-card.webp | **wan2.5-i2i-preview** with references to the cleaned portrait and face (1024x1536). The prompt asked for upper body, a fan of playing cards, a blue rim light, a warm ember glow and floating embers on a dark background. |
| aoi/aoi-face-*.webp | Sheet crop (100px square inside each expression tile, labels excluded), then 4x Lanczos with a mild unsharp mask. |
| aoi/aoi-outfit-*.webp | Sheet crop (112x305 per figure, labels excluded), then 3x Lanczos with unsharp. |
| aoi/aoi-action.webp | Sheet crop of the IN GAME / ACTION panel (840,738–1017,1012), then 3x. |
| aoi/scene-city.webp, scene-gaming.webp, scene-beach.webp | Sheet crops from the bottom strip, 3x. The caption is cropped away. The weapon scene is not used. |
| aoi/aoi-sheet.webp | The sheet re-encoded at WebP q80. |
| agents/aoi.webp | Crop of the cleaned portrait, 512px. |
| agents/{ren,mika,bram,nova,lin}.webp | **wan2.5-t2i-preview** text-to-image (1024², best of 2) with a shared head-and-shoulders, navy and electric-blue bokeh style prompt (see `gen.py` AVATARS), then resized to 512. |
| scene-table.webp | **wan2.5-t2i-preview** (1664x928, best of 2), then Lanczos to 1920x1080. |
| /favicon.png, /apple-touch-icon.png | Crop of the "Smile" expression face, 64 and 180 px. |
