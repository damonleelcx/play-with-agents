import { useEffect, useId, useRef, useState } from 'react'
import '../pages/landing/holo-card.css'

// A 3D holographic trading card, after the "Ashen One" holo card: an ornate
// gold frame, an arched portrait window, a foil sheen and glare that follow
// the pointer, hover to tilt, drag to rotate (with inertia and a spring back
// to the nearest face), double-click / double-tap / Enter to flip, an idle
// float and a layer of embers.
//
// Pure CSS 3D + one small 2D canvas, so it adds no 3D library to the bundle.
// The animation loop and the particles start only once the card is on
// screen, pause when it leaves, and everything is released on unmount.
// prefers-reduced-motion renders a static card (it still flips, instantly).

export type HoloCardProps = {
  title?: string
  subtitle?: string
  kicker?: string
  plate?: string
  back?: string
  backSub?: string
  caption?: string
  captionTouch?: string
  label?: string
  portraitAlt?: string
  images?: string[]
  className?: string
}

const DEFAULT_IMAGES = ['/play/aoi/aoi-card.webp', '/play/aoi/aoi-portrait.webp']

export default function HoloCard({
  title = 'AOI · 葵',
  subtitle = 'AI AGENT PLAYER',
  kicker = 'NO. 001 · THE HOST',
  plate = 'DEAL ME IN',
  back = 'PLAY WITH AGENTS',
  backSub = 'THE TABLE IS ALWAYS FULL',
  caption = 'HOVER TO TILT · DRAG TO ROTATE · DOUBLE-CLICK TO FLIP',
  captionTouch = 'DRAG TO ROTATE · DOUBLE-TAP TO FLIP',
  label = 'Holographic card. Drag to rotate; press Enter or double-click to flip.',
  portraitAlt = 'Aoi',
  images = DEFAULT_IMAGES,
  className,
}: HoloCardProps) {
  const uid = useId().replace(/[^a-zA-Z0-9]/g, '')
  const rootRef = useRef<HTMLElement>(null)
  const stageRef = useRef<HTMLDivElement>(null)
  const cardRef = useRef<HTMLDivElement>(null)
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const [flipped, setFlipped] = useState(false)
  const [imgIdx, setImgIdx] = useState(0)
  const [touch, setTouch] = useState(false)
  const [paused, setPaused] = useState(true)

  useEffect(() => {
    setTouch(window.matchMedia?.('(hover: none)').matches ?? false)
  }, [])

  useEffect(() => {
    const root = rootRef.current
    const stage = stageRef.current
    const card = cardRef.current
    const canvas = canvasRef.current
    if (!root || !stage || !card || !canvas) return

    const reduce =
      (window.matchMedia?.('(prefers-reduced-motion: reduce)').matches ?? false) ||
      document.documentElement.dataset.motion === 'reduce'

    // Physics state. base is the resting face (0 front, 180 back, ...);
    // off* are the live offsets from it; t* the hover targets.
    const s = {
      base: 0, offX: 0, offY: 0, vX: 0, vY: 0, tX: 0, tY: 0,
      mx: 50, my: 50, pmx: 50, pmy: 50, glare: 0,
      hovering: false, down: false, dragging: false,
      lastX: 0, lastY: 0, moved: 0, lastTap: 0, pid: -1,
    }

    const render = () => {
      const ry = s.base + s.offY
      card.style.transform = `perspective(1400px) rotateX(${s.offX.toFixed(2)}deg) rotateY(${ry.toFixed(2)}deg)`
      card.style.setProperty('--mx', `${s.mx.toFixed(1)}%`)
      card.style.setProperty('--my', `${s.my.toFixed(1)}%`)
      card.style.setProperty('--fx', `${(s.mx * 1.6 - 30).toFixed(1)}%`)
      card.style.setProperty('--fy', `${(s.my * 1.2 - 10).toFixed(1)}%`)
      card.style.setProperty('--glare', s.glare.toFixed(3))
    }

    const syncFlipped = () => setFlipped(Math.round(s.base / 180) % 2 !== 0)

    const flip = () => {
      s.base += 180
      if (reduce) {
        s.offY = 0
        render()
      } else {
        s.offY -= 180 // keep the pose continuous; the spring animates the turn
        s.vY -= 4
        wake()
      }
      syncFlipped()
    }

    const relPos = (e: PointerEvent) => {
      const r = stage.getBoundingClientRect()
      return [(e.clientX - r.left) / r.width, (e.clientY - r.top) / r.height]
    }

    const onMove = (e: PointerEvent) => {
      if (reduce) return
      const [px, py] = relPos(e)
      if (s.down && e.pointerId === s.pid) {
        const dx = e.clientX - s.lastX
        const dy = e.clientY - s.lastY
        s.lastX = e.clientX
        s.lastY = e.clientY
        s.moved += Math.abs(dx) + Math.abs(dy)
        if (s.moved > 5) s.dragging = true
        if (s.dragging) {
          const ddy = dx * 0.5
          const ddx = -dy * 0.32
          s.offY += ddy
          s.offX = Math.max(-38, Math.min(38, s.offX + ddx))
          s.vY = s.vY * 0.5 + ddy * 0.5
          s.vX = s.vX * 0.5 + ddx * 0.5
        }
      }
      if (e.pointerType !== 'touch') {
        s.hovering = true
        s.tY = (px - 0.5) * 26
        s.tX = (0.5 - py) * 20
        s.pmx = px * 100
        s.pmy = py * 100
      } else if (s.dragging) {
        s.pmx = px * 100
        s.pmy = py * 100
      }
      wake()
    }

    const onDown = (e: PointerEvent) => {
      if (reduce || (e.pointerType === 'mouse' && e.button !== 0)) return
      s.down = true
      s.dragging = false
      s.moved = 0
      s.pid = e.pointerId
      s.lastX = e.clientX
      s.lastY = e.clientY
      s.vX = s.vY = 0
      try { stage.setPointerCapture(e.pointerId) } catch { /* ignore */ }
      wake()
    }

    const release = (e: PointerEvent) => {
      if (!s.down || e.pointerId !== s.pid) return
      s.down = false
      try { stage.releasePointerCapture(e.pointerId) } catch { /* ignore */ }
      if (s.dragging) {
        // settle on the face the flick is heading for
        const projected = s.base + s.offY + s.vY * 9
        const face = Math.round(projected / 180) * 180
        const total = s.base + s.offY
        if (face !== s.base) {
          s.base = face
          s.offY = total - face
          syncFlipped()
        }
      } else if (e.pointerType === 'touch' && e.type === 'pointerup') {
        const now = performance.now()
        if (now - s.lastTap < 340) {
          s.lastTap = 0
          flip()
        } else s.lastTap = now
      }
      s.dragging = false
      if (e.pointerType === 'touch') {
        s.hovering = false
        s.tX = s.tY = 0
      }
      wake()
    }

    const onLeave = (e: PointerEvent) => {
      if (s.down && e.pointerId === s.pid) return
      s.hovering = false
      s.tX = s.tY = 0
      wake()
    }

    const onDbl = (e: MouseEvent) => {
      e.preventDefault()
      flip()
    }

    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault()
        flip()
      } else if (!reduce && (e.key === 'ArrowLeft' || e.key === 'ArrowRight')) {
        e.preventDefault()
        s.vY += e.key === 'ArrowLeft' ? -6 : 6
        wake()
      }
    }

    stage.addEventListener('pointermove', onMove)
    stage.addEventListener('pointerdown', onDown)
    stage.addEventListener('pointerup', release)
    stage.addEventListener('pointercancel', release)
    stage.addEventListener('pointerleave', onLeave)
    stage.addEventListener('dblclick', onDbl)
    stage.addEventListener('keydown', onKey)

    // ---- embers -------------------------------------------------------
    const ctx = reduce ? null : canvas.getContext('2d')
    type P = { x: number; y: number; vx: number; vy: number; r: number; life: number; max: number; ph: number; blue: boolean }
    let parts: P[] = []
    let cw = 0
    let ch = 0
    let dpr = 1
    let sprite: HTMLCanvasElement | null = null
    let spriteBlue: HTMLCanvasElement | null = null

    const makeSprite = (inner: string, outer: string) => {
      const c = document.createElement('canvas')
      c.width = c.height = 32
      const g = c.getContext('2d')!
      const grd = g.createRadialGradient(16, 16, 0, 16, 16, 16)
      grd.addColorStop(0, '#fff6e6')
      grd.addColorStop(0.18, inner)
      grd.addColorStop(0.45, outer)
      grd.addColorStop(1, 'rgba(0,0,0,0)')
      g.fillStyle = grd
      g.fillRect(0, 0, 32, 32)
      return c
    }

    const spawn = (p?: P, anywhere = false): P => {
      const q = p ?? ({} as P)
      q.x = cw * (0.15 + Math.random() * 0.7)
      q.y = anywhere ? Math.random() * ch : ch * (0.72 + Math.random() * 0.3)
      q.vx = (Math.random() - 0.5) * 0.25 * dpr
      q.vy = -(0.25 + Math.random() * 0.65) * dpr
      q.r = (0.7 + Math.random() * 1.9) * dpr
      q.max = 160 + Math.random() * 260
      q.life = anywhere ? Math.random() * q.max : 0
      q.ph = Math.random() * Math.PI * 2
      q.blue = Math.random() < 0.18
      return q
    }

    const sizeCanvas = () => {
      const r = canvas.getBoundingClientRect()
      dpr = Math.min(2, window.devicePixelRatio || 1)
      cw = Math.max(1, Math.round(r.width * dpr))
      ch = Math.max(1, Math.round(r.height * dpr))
      canvas.width = cw
      canvas.height = ch
      const n = Math.round(Math.min(70, Math.max(26, (r.width * r.height) / 9000)))
      parts = Array.from({ length: n }, () => spawn(undefined, true))
    }

    const drawEmbers = (dt: number, t: number) => {
      if (!ctx || !sprite || !spriteBlue) return
      ctx.clearRect(0, 0, cw, ch)
      ctx.globalCompositeOperation = 'lighter'
      for (const p of parts) {
        p.life += dt
        if (p.life > p.max || p.y < -20) spawn(p)
        p.x += (p.vx + Math.sin(t * 0.0012 + p.ph) * 0.22 * dpr) * dt
        p.y += p.vy * dt
        const k = p.life / p.max
        const fade = Math.min(1, k * 6) * (1 - k)
        const flicker = 0.75 + 0.25 * Math.sin(t * 0.012 + p.ph * 3)
        ctx.globalAlpha = Math.max(0, fade * flicker)
        const size = p.r * 7
        ctx.drawImage(p.blue ? spriteBlue : sprite, p.x - size / 2, p.y - size / 2, size, size)
      }
      ctx.globalAlpha = 1
      ctx.globalCompositeOperation = 'source-over'
    }

    // ---- loop -----------------------------------------------------------
    let raf = 0
    let last = 0
    let visible = false
    let inited = false

    const step = (dt: number) => {
      if (!s.dragging) {
        const k = 0.05
        const damp = Math.pow(0.86, dt)
        s.vY = (s.vY + (s.tY - s.offY) * k * dt) * damp
        s.vX = (s.vX + (s.tX - s.offX) * k * dt) * damp
        s.offY += s.vY * dt
        s.offX += s.vX * dt
      }
      const live = s.hovering || s.dragging
      const tmx = live ? s.pmx : 50 + s.offY * 1.4
      const tmy = live ? s.pmy : 50 - s.offX * 1.4
      const a = 1 - Math.pow(0.82, dt)
      s.mx += (tmx - s.mx) * a
      s.my += (tmy - s.my) * a
      const gT = live ? 1 : Math.min(1, (Math.abs(s.offY) + Math.abs(s.offX)) / 30)
      s.glare += (gT - s.glare) * a
      render()
    }

    const frame = (now: number) => {
      const dt = Math.min(3, (now - last) / 16.667)
      last = now
      step(dt)
      drawEmbers(dt, now)
      if (visible) raf = requestAnimationFrame(frame)
      else raf = 0
    }

    function wake() {
      if (reduce || !visible || raf) return
      last = performance.now()
      raf = requestAnimationFrame(frame)
    }

    const start = () => {
      if (reduce) return
      if (!inited) {
        inited = true
        sprite = makeSprite('#ffc27a', 'rgba(255,120,40,0.35)')
        spriteBlue = makeSprite('#bfe0ff', 'rgba(61,123,255,0.35)')
        sizeCanvas()
      }
      wake()
    }

    render()
    const io = new IntersectionObserver(
      ([entry]) => {
        visible = entry.isIntersecting
        setPaused(!visible)
        if (visible) start()
        else if (raf) {
          cancelAnimationFrame(raf)
          raf = 0
        }
      },
      { rootMargin: '80px' },
    )
    io.observe(root)

    const ro = new ResizeObserver(() => {
      if (inited) sizeCanvas()
    })
    ro.observe(canvas)

    return () => {
      io.disconnect()
      ro.disconnect()
      if (raf) cancelAnimationFrame(raf)
      stage.removeEventListener('pointermove', onMove)
      stage.removeEventListener('pointerdown', onDown)
      stage.removeEventListener('pointerup', release)
      stage.removeEventListener('pointercancel', release)
      stage.removeEventListener('pointerleave', onLeave)
      stage.removeEventListener('dblclick', onDbl)
      stage.removeEventListener('keydown', onKey)
      parts = []
      sprite = spriteBlue = null
      canvas.width = canvas.height = 0
    }
  }, [])

  const g = (n: string) => `${n}${uid}`
  const url = (n: string) => `url(#${g(n)})`
  const imgOk = imgIdx < images.length

  return (
    <figure ref={rootRef} className={`holo ${paused ? 'is-paused' : ''} ${className ?? ''}`}>
      <canvas ref={canvasRef} className="holo-embers" aria-hidden="true" />
      <div className="holo-ground" aria-hidden="true" />
      <div
        ref={stageRef}
        className="holo-stage"
        role="button"
        tabIndex={0}
        aria-label={label}
        aria-pressed={flipped}
      >
        <div className="holo-float">
          <div ref={cardRef} className="holo-card">
            {/* FRONT */}
            <div className="holo-face holo-front" aria-hidden={flipped}>
              <div className="holo-portrait">
                <div className="holo-rays" />
                {imgOk ? (
                  <img
                    src={images[imgIdx]}
                    alt={portraitAlt}
                    draggable={false}
                    decoding="async"
                    onError={() => setImgIdx((i) => i + 1)}
                  />
                ) : (
                  <span className="holo-glyph" aria-hidden="true">葵</span>
                )}
              </div>
              <svg className="holo-frame" viewBox="0 0 300 420" aria-hidden="true">
                <defs>
                  <linearGradient id={g('gold')} x1="0" y1="0" x2="1" y2="1">
                    <stop offset="0" stopColor="#fff1c1" />
                    <stop offset="0.16" stopColor="#e2b65a" />
                    <stop offset="0.34" stopColor="#8c5e1f" />
                    <stop offset="0.52" stopColor="#f6dc95" />
                    <stop offset="0.7" stopColor="#a8762c" />
                    <stop offset="0.86" stopColor="#ecca7a" />
                    <stop offset="1" stopColor="#7a4f18" />
                  </linearGradient>
                  <linearGradient id={g('goldv')} x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0" stopColor="#fff6d8" />
                    <stop offset="0.5" stopColor="#e8c169" />
                    <stop offset="1" stopColor="#9a6a22" />
                  </linearGradient>
                  <radialGradient id={g('body')} cx="0.5" cy="0.45" r="0.75">
                    <stop offset="0" stopColor="#21180e" />
                    <stop offset="0.6" stopColor="#130e08" />
                    <stop offset="1" stopColor="#070504" />
                  </radialGradient>
                  <linearGradient id={g('strip')} x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0" stopColor="#ffcf7a" stopOpacity="0" />
                    <stop offset="0.2" stopColor="#ffb64d" />
                    <stop offset="0.5" stopColor="#fff0c4" />
                    <stop offset="0.8" stopColor="#ffb64d" />
                    <stop offset="1" stopColor="#ffcf7a" stopOpacity="0" />
                  </linearGradient>
                  <radialGradient id={g('gem')} cx="0.35" cy="0.35" r="0.7">
                    <stop offset="0" stopColor="#e6f0ff" />
                    <stop offset="0.35" stopColor="#5b93ff" />
                    <stop offset="1" stopColor="#0d2a7a" />
                  </radialGradient>
                  <pattern id={g('tex')} width="6" height="6" patternUnits="userSpaceOnUse">
                    <path d="M0 6 L6 0" stroke="#c99a3c" strokeOpacity="0.07" strokeWidth="1" />
                  </pattern>
                  <g id={g('corner')}>
                    <path d="M15 66 C15 36 36 15 66 15" fill="none" stroke={url('gold')} strokeWidth="2.2" />
                    <path d="M21 58 C22 40 40 22 58 21" fill="none" stroke={url('gold')} strokeWidth="0.9" />
                    <path d="M24 74 c-2-12 2-20 10-22 c8-2 12 6 6 10 c-4 2-7-1-5-4" fill="none" stroke={url('gold')} strokeWidth="1.4" strokeLinecap="round" />
                    <path d="M74 24 c-12-2-20 2-22 10 c-2 8 6 12 10 6 c2-4-1-7-4-5" fill="none" stroke={url('gold')} strokeWidth="1.4" strokeLinecap="round" />
                    <path d="M30 30 q14-3 20-16 q-14 3-20 16z" fill={url('gold')} />
                    <path d="M30 30 q3-14 16-20 q-3 14-16 20z" fill={url('gold')} opacity="0.85" />
                    <circle cx="23" cy="23" r="6" fill={url('gold')} />
                    <circle cx="23" cy="23" r="3.4" fill={url('gem')} />
                  </g>
                  <g id={g('studs')}>
                    <rect x="21" y="112" width="13" height="226" rx="2" fill="#0d0905" stroke={url('gold')} strokeWidth="1.3" />
                    <rect x="25.6" y="132" width="3.8" height="186" rx="1.9" fill={url('strip')} className="holo-strip" />
                    <path d="M27.5 104 l6 8 l-6 8 l-6-8z" fill={url('gold')} />
                    <path d="M27.5 330 l6 8 l-6 8 l-6-8z" fill={url('gold')} />
                    <path d="M27.5 219 l5 6 l-5 6 l-5-6z" fill={url('gold')} />
                  </g>
                  <g id={g('scroll')}>
                    <path d="M0 0 c10-8 22-8 30 0 c6 6 2 14-5 12 c-5-1-5-7 0-8" fill="none" stroke={url('gold')} strokeWidth="1.3" strokeLinecap="round" />
                    <path d="M30 0 c8 -4 16 -3 22 2" fill="none" stroke={url('gold')} strokeWidth="1" strokeLinecap="round" />
                  </g>
                </defs>

                {/* gold rim */}
                <path
                  fillRule="evenodd"
                  fill={url('gold')}
                  d="M15.5 0.5 H284.5 Q299.5 0.5 299.5 15.5 V404.5 Q299.5 419.5 284.5 419.5 H15.5 Q0.5 419.5 0.5 404.5 V15.5 Q0.5 0.5 15.5 0.5 Z M17 6 H283 Q294 6 294 17 V403 Q294 414 283 414 H17 Q6 414 6 403 V17 Q6 6 17 6 Z"
                />
                {/* dark body with the arched window cut out */}
                <path
                  fillRule="evenodd"
                  fill={url('body')}
                  d="M17 6 H283 Q294 6 294 17 V403 Q294 414 283 414 H17 Q6 414 6 403 V17 Q6 6 17 6 Z M46 326 V148 A104 74 0 0 1 254 148 V326 Z"
                />
                <path
                  fillRule="evenodd"
                  fill={url('tex')}
                  d="M17 6 H283 Q294 6 294 17 V403 Q294 414 283 414 H17 Q6 414 6 403 V17 Q6 6 17 6 Z M46 326 V148 A104 74 0 0 1 254 148 V326 Z"
                />
                <rect x="12.5" y="12.5" width="275" height="395" rx="8" fill="none" stroke={url('gold')} strokeWidth="1" />
                <rect x="15.5" y="15.5" width="269" height="389" rx="6" fill="none" stroke={url('gold')} strokeOpacity="0.45" strokeWidth="0.6" />

                {/* window mouldings */}
                <path d="M46 326 V148 A104 74 0 0 1 254 148 V326 Z" fill="none" stroke={url('gold')} strokeWidth="3.2" />
                <path d="M39.5 332.5 V148 A110.5 80.5 0 0 1 260.5 148 V332.5 Z" fill="none" stroke={url('gold')} strokeWidth="1.1" />
                <path d="M42.6 329.4 V148 A107.4 77.4 0 0 1 257.4 148 V329.4" fill="none" stroke="#000" strokeOpacity="0.55" strokeWidth="1.6" />
                {/* gothic tracery on the arch shoulders */}
                <path d="M46 148 c18-2 28-14 30-34" fill="none" stroke={url('gold')} strokeWidth="1" opacity="0.8" />
                <path d="M254 148 c-18-2-28-14-30-34" fill="none" stroke={url('gold')} strokeWidth="1" opacity="0.8" />
                <path d="M62 96 c10 4 14 14 10 24" fill="none" stroke={url('gold')} strokeWidth="0.9" opacity="0.7" />
                <path d="M238 96 c-10 4-14 14-10 24" fill="none" stroke={url('gold')} strokeWidth="0.9" opacity="0.7" />

                {/* keystone */}
                <path d="M150 62 l11 13 l-11 14 l-11-14z" fill={url('gold')} stroke="#3a250c" strokeWidth="0.6" />
                <circle cx="150" cy="75.5" r="4" fill={url('gem')} />
                <path d="M128 78 c8 0 14-4 16-10 M172 78 c-8 0-14-4-16-10" fill="none" stroke={url('gold')} strokeWidth="1.1" />

                {/* side pillars */}
                <use href={`#${g('studs')}`} />
                <use href={`#${g('studs')}`} transform="translate(300 0) scale(-1 1)" />

                {/* corners */}
                <use href={`#${g('corner')}`} />
                <use href={`#${g('corner')}`} transform="translate(300 0) scale(-1 1)" />
                <use href={`#${g('corner')}`} transform="translate(0 420) scale(1 -1)" />
                <use href={`#${g('corner')}`} transform="translate(300 420) scale(-1 -1)" />

                {/* title plate */}
                <path d="M64 19 H236 L252 41 L236 63 H64 L48 41 Z" fill="#0b0805" stroke={url('gold')} strokeWidth="1.8" />
                <path d="M68 23.5 H232 L245 41 L232 58.5 H68 L55 41 Z" fill="none" stroke={url('gold')} strokeOpacity="0.55" strokeWidth="0.7" />
                <circle cx="48" cy="41" r="3.2" fill={url('gem')} stroke={url('gold')} strokeWidth="1" />
                <circle cx="252" cy="41" r="3.2" fill={url('gem')} stroke={url('gold')} strokeWidth="1" />
                <text x="150" y="44.5" textAnchor="middle" className="holo-title" fill={url('goldv')}>{title}</text>
                <text x="150" y="54.5" textAnchor="middle" className="holo-sub" fill="#d8b56b">{subtitle}</text>

                {/* bottom plate */}
                <use href={`#${g('scroll')}`} transform="translate(56 336) scale(-0.9 0.9) translate(-60 0)" />
                <path d="M60 342 H240 L257 370 L240 398 H60 L43 370 Z" fill="#0b0805" stroke={url('gold')} strokeWidth="1.8" />
                <path d="M64 346.5 H236 L250 370 L236 393.5 H64 L50 370 Z" fill="none" stroke={url('gold')} strokeOpacity="0.55" strokeWidth="0.7" />
                <circle cx="43" cy="370" r="3.2" fill={url('gem')} stroke={url('gold')} strokeWidth="1" />
                <circle cx="257" cy="370" r="3.2" fill={url('gem')} stroke={url('gold')} strokeWidth="1" />
                <path d="M98 358 H134 M166 358 H202" stroke={url('gold')} strokeWidth="0.7" />
                <text x="150" y="360" textAnchor="middle" className="holo-kicker" fill="#d8b56b">{kicker}</text>
                <text x="150" y="384.5" textAnchor="middle" className="holo-plate" fill={url('goldv')}>{plate}</text>
              </svg>
              <div className="holo-foil" />
              <div className="holo-glare" />
            </div>

            {/* BACK */}
            <div className="holo-face holo-back" aria-hidden={!flipped}>
              <svg className="holo-frame" viewBox="0 0 300 420" aria-hidden="true">
                <defs>
                  <linearGradient id={g('bgold')} x1="0" y1="0" x2="1" y2="1">
                    <stop offset="0" stopColor="#fff1c1" />
                    <stop offset="0.25" stopColor="#c9963e" />
                    <stop offset="0.5" stopColor="#f6dc95" />
                    <stop offset="0.75" stopColor="#9a6a22" />
                    <stop offset="1" stopColor="#f0d283" />
                  </linearGradient>
                  <radialGradient id={g('bbody')} cx="0.5" cy="0.5" r="0.7">
                    <stop offset="0" stopColor="#182544" />
                    <stop offset="0.65" stopColor="#0d1528" />
                    <stop offset="1" stopColor="#060a14" />
                  </radialGradient>
                  <pattern id={g('lat')} width="18" height="18" patternUnits="userSpaceOnUse">
                    <path d="M9 0 L18 9 L9 18 L0 9 Z" fill="none" stroke="#e9c46a" strokeOpacity="0.22" strokeWidth="0.8" />
                    <circle cx="9" cy="9" r="1.1" fill="#8fb6ff" fillOpacity="0.35" />
                  </pattern>
                  <linearGradient id={g('chev')} x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0" stopColor="#8fb6ff" />
                    <stop offset="1" stopColor="#3d7bff" />
                  </linearGradient>
                </defs>
                <rect x="0.5" y="0.5" width="299" height="419" rx="15" fill={url('bgold')} />
                <rect x="6" y="6" width="288" height="408" rx="11" fill={url('bbody')} />
                <rect x="18" y="18" width="264" height="384" rx="6" fill={url('lat')} />
                <rect x="14.5" y="14.5" width="271" height="391" rx="8" fill="none" stroke={url('bgold')} strokeWidth="1.2" />
                <rect x="20.5" y="20.5" width="259" height="379" rx="5" fill="none" stroke={url('bgold')} strokeOpacity="0.5" strokeWidth="0.6" />
                {[[18, 18], [282, 18], [18, 402], [282, 402]].map(([x, y]) => (
                  <path key={`${x}-${y}`} d={`M${x} ${y - 9} l9 9 l-9 9 l-9-9z`} fill={url('bgold')} />
                ))}
                <circle cx="150" cy="196" r="76" fill="#0a1020" stroke={url('bgold')} strokeWidth="2.4" />
                <circle cx="150" cy="196" r="67" fill="none" stroke={url('bgold')} strokeWidth="0.8" strokeDasharray="2 4" />
                <circle cx="150" cy="196" r="56" fill="none" stroke={url('bgold')} strokeOpacity="0.6" strokeWidth="0.8" />
                <path d="M150 154 L190 222 H110 Z" fill="none" stroke={url('bgold')} strokeWidth="5" strokeLinejoin="round" />
                <path d="M150 178 L168 210 H132 Z" fill={url('chev')} />
                <text x="150" y="133" textAnchor="middle" className="holo-suit" fill="#e9c46a">♠</text>
                <text x="226" y="202" textAnchor="middle" className="holo-suit" fill="#ff8a8a">♥</text>
                <text x="150" y="271" textAnchor="middle" className="holo-suit" fill="#e9c46a">♣</text>
                <text x="74" y="202" textAnchor="middle" className="holo-suit" fill="#ff8a8a">♦</text>
                <path d="M70 318 H230" stroke={url('bgold')} strokeWidth="0.8" />
                <text x="150" y="342" textAnchor="middle" className="holo-back-title" fill={url('bgold')}>{back}</text>
                <text x="150" y="360" textAnchor="middle" className="holo-kicker" fill="#b9c6e4">{backSub}</text>
                <path d="M70 372 H230" stroke={url('bgold')} strokeWidth="0.8" />
              </svg>
              <div className="holo-foil" />
              <div className="holo-glare" />
            </div>
          </div>
        </div>
      </div>
      <figcaption className="holo-caption">{touch ? captionTouch : caption}</figcaption>
    </figure>
  )
}
