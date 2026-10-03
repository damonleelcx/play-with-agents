// Tiny WebAudio blips: no audio files. Created lazily on the first sound so
// browsers that require a gesture before audio are respected.
let ctx: AudioContext | null = null

function ac(): AudioContext | null {
  try {
    if (!ctx) {
      const C = (window as any).AudioContext || (window as any).webkitAudioContext
      if (!C) return null
      ctx = new C()
    }
    if (ctx!.state === 'suspended') ctx!.resume().catch(() => {})
    return ctx
  } catch {
    return null
  }
}

function tone(freq: number, dur: number, type: OscillatorType, gain: number, delay = 0, slideTo?: number) {
  const a = ac()
  if (!a) return
  const t0 = a.currentTime + delay
  const o = a.createOscillator()
  const g = a.createGain()
  o.type = type
  o.frequency.setValueAtTime(freq, t0)
  if (slideTo) o.frequency.exponentialRampToValueAtTime(slideTo, t0 + dur)
  g.gain.setValueAtTime(0.0001, t0)
  g.gain.exponentialRampToValueAtTime(gain, t0 + 0.008)
  g.gain.exponentialRampToValueAtTime(0.0001, t0 + dur)
  o.connect(g).connect(a.destination)
  o.start(t0)
  o.stop(t0 + dur + 0.02)
}

function noise(dur: number, gain: number, delay = 0, hp = 2000) {
  const a = ac()
  if (!a) return
  const len = Math.floor(a.sampleRate * dur)
  const buf = a.createBuffer(1, len, a.sampleRate)
  const d = buf.getChannelData(0)
  for (let i = 0; i < len; i++) d[i] = (Math.random() * 2 - 1) * (1 - i / len)
  const src = a.createBufferSource()
  src.buffer = buf
  const f = a.createBiquadFilter()
  f.type = 'highpass'
  f.frequency.value = hp
  const g = a.createGain()
  g.gain.value = gain
  src.connect(f).connect(g).connect(a.destination)
  src.start(a.currentTime + delay)
}

export type Blip = 'deal' | 'chips' | 'turn' | 'win' | 'fold'

export function blip(kind: Blip, enabled: boolean) {
  if (!enabled) return
  switch (kind) {
    case 'deal':
      noise(0.06, 0.12, 0, 3200)
      break
    case 'chips':
      tone(2400, 0.05, 'triangle', 0.05)
      tone(2900, 0.05, 'triangle', 0.04, 0.045)
      tone(2600, 0.06, 'triangle', 0.035, 0.09)
      break
    case 'turn':
      tone(660, 0.14, 'sine', 0.09)
      tone(990, 0.2, 'sine', 0.08, 0.11)
      break
    case 'win':
      tone(523, 0.14, 'sine', 0.07)
      tone(659, 0.14, 'sine', 0.07, 0.1)
      tone(784, 0.28, 'sine', 0.07, 0.2)
      break
    case 'fold':
      tone(320, 0.12, 'sine', 0.04, 0, 200)
      break
  }
}
