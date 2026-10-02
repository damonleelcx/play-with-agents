"""Minimal DashScope (Alibaba Model Studio) client for the art pipeline.

The API key is read from the environment (PLAY_IMAGE_API_KEY) or from the
repo's git-ignored .env file. It is never printed or written anywhere.
Every generation call is counted in <out>/calls.log so the total budget
(MAX_CALLS) is enforced across runs.
"""
import base64
import io
import json
import os
import pathlib
import time

import requests

ROOT = pathlib.Path(__file__).resolve().parents[2]
BASE = "https://dashscope.aliyuncs.com/api/v1"
MAX_CALLS = int(os.environ.get("GEN_ART_MAX_CALLS", "60"))
OUT = pathlib.Path(os.environ.get("GEN_ART_OUT", ROOT / "scripts" / "gen-art" / ".out"))
OUT.mkdir(parents=True, exist_ok=True)


def _key():
    k = os.environ.get("PLAY_IMAGE_API_KEY")
    if not k:
        env = ROOT / ".env"
        for line in env.read_text(encoding="utf-8").splitlines():
            if line.startswith("PLAY_IMAGE_API_KEY="):
                k = line.split("=", 1)[1].strip().strip('"').strip("'")
    if not k:
        raise SystemExit("PLAY_IMAGE_API_KEY not set")
    return k


def _headers(async_=False):
    h = {"Authorization": "Bearer " + _key(), "Content-Type": "application/json"}
    if async_:
        h["X-DashScope-Async"] = "enable"
    return h


def _count(tag):
    log = OUT / "calls.log"
    n = len(log.read_text().splitlines()) if log.exists() else 0
    if n >= MAX_CALLS:
        raise SystemExit(f"generation budget exhausted ({n}/{MAX_CALLS})")
    with log.open("a") as f:
        f.write(f"{time.strftime('%H:%M:%S')} {tag}\n")
    return n + 1


def data_url(img, fmt="PNG"):
    """PIL image -> base64 data URL (no third-party upload)."""
    buf = io.BytesIO()
    img.convert("RGB").save(buf, fmt)
    mime = "image/png" if fmt == "PNG" else "image/jpeg"
    return f"data:{mime};base64," + base64.b64encode(buf.getvalue()).decode()


def _poll(task_id, max_polls=60, every=5):
    for _ in range(max_polls):  # bounded: <= max_polls*every seconds
        r = requests.get(f"{BASE}/tasks/{task_id}", headers=_headers(), timeout=30)
        j = r.json()
        st = j.get("output", {}).get("task_status")
        if st == "SUCCEEDED":
            return j
        if st in ("FAILED", "CANCELED", "UNKNOWN"):
            raise RuntimeError(json.dumps(j)[:800])
        time.sleep(every)
    raise TimeoutError(task_id)


def _download(urls, name):
    paths = []
    for i, u in enumerate(urls):
        p = OUT / (f"{name}.png" if i == 0 else f"{name}-{i}.png")
        p.write_bytes(requests.get(u, timeout=120).content)
        paths.append(p)
    return paths


def t2i(name, prompt, negative="", model="wan2.2-t2i-plus", size="1024*1536", n=1):
    _count(f"t2i {model} {name}")
    body = {"model": model, "input": {"prompt": prompt, "negative_prompt": negative},
            "parameters": {"size": size, "n": n, "prompt_extend": False, "watermark": False}}
    r = requests.post(f"{BASE}/services/aigc/text2image/image-synthesis",
                      headers=_headers(True), json=body, timeout=60)
    j = r.json()
    if "output" not in j:
        raise RuntimeError(json.dumps(j)[:800])
    j = _poll(j["output"]["task_id"])
    return _download([x["url"] for x in j["output"]["results"] if "url" in x], name)


def i2i(name, prompt, images, negative="", model="wan2.5-i2i-preview", size=None, n=1):
    """Reference-image generation (wan2.5-i2i). images: list of data URLs."""
    _count(f"i2i {model} {name}")
    params = {"n": n, "watermark": False}
    if size:
        params["size"] = size
    body = {"model": model, "input": {"prompt": prompt, "images": images, "negative_prompt": negative},
            "parameters": params}
    r = requests.post(f"{BASE}/services/aigc/image2image/image-synthesis",
                      headers=_headers(True), json=body, timeout=120)
    j = r.json()
    if "output" not in j:
        raise RuntimeError(json.dumps(j)[:800])
    j = _poll(j["output"]["task_id"])
    return _download([x["url"] for x in j["output"]["results"] if "url" in x], name)


def qedit(name, prompt, images, negative="", model="qwen-image-edit", extra=None):
    """Synchronous qwen-image-edit(-plus): images + instruction -> edited image."""
    _count(f"qedit {model} {name}")
    content = [{"image": u} for u in images] + [{"text": prompt}]
    params = {"watermark": False, "negative_prompt": negative or " "}
    params.update(extra or {})
    body = {"model": model, "input": {"messages": [{"role": "user", "content": content}]},
            "parameters": params}
    r = requests.post(f"{BASE}/services/aigc/multimodal-generation/generation",
                      headers=_headers(), json=body, timeout=300)
    j = r.json()
    try:
        urls = [c["image"] for c in j["output"]["choices"][0]["message"]["content"] if "image" in c]
    except (KeyError, IndexError):
        raise RuntimeError(json.dumps(j)[:800])
    return _download(urls, name)
