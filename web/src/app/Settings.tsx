import { useEffect, useRef, useState } from 'react'
import { NavLink, useNavigate, useParams } from 'react-router-dom'
import { AgentAvatar, AoiFace } from '../components/Aoi'
import { IconAlert, IconBell, IconCards, IconChart, IconCheck, IconLock, IconPalette, IconRefresh, IconRobot, IconShield, IconSpeaker, IconStar, IconStop, IconUser, IconWand } from '../components/Icons'
import { PasswordInput } from '../pages/auth/AuthLayout'
import { api, type Agent } from '../lib/api'
import { setVoiceVolume, speak, stop as stopVoice, useVoice } from '../lib/voice'
import { useI18n, type Lang } from '../lib/i18n'
import { fmtTime } from './missionModel'
import { usePrefs, useToast } from './prefs'
import { ROSTER } from './roster'

const SECTIONS = ['profile', 'aoi', 'table', 'agents', 'studio', 'notifications', 'appearance', 'privacy', 'security', 'usage'] as const
type Section = (typeof SECTIONS)[number]
const ICONS: Record<Section, (p: { size?: number }) => JSX.Element> = {
  profile: IconUser, aoi: IconStar, table: IconCards, agents: IconRobot, studio: IconWand,
  notifications: IconBell, appearance: IconPalette, privacy: IconShield, security: IconLock, usage: IconChart,
}

export default function Settings() {
  const { section = 'profile' } = useParams()
  const { t } = useI18n()
  const { data, error, reload } = usePrefs()
  const navRef = useRef<HTMLElement>(null)
  const current: Section = (SECTIONS as readonly string[]).includes(section) ? (section as Section) : 'profile'
  // On phones the section strip scrolls sideways; keep the active one in view.
  const pageRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    navRef.current?.querySelector('.active')?.scrollIntoView({ block: 'nearest', inline: 'center' })
    pageRef.current?.scrollTo({ top: 0 })
  }, [current])
  const I = ICONS[current]

  return (
    <div className="page settings" ref={pageRef}>
      <header className="page-head"><h1>{t.settings.title}</h1></header>
      <div className="settings-grid">
        <nav className="settings-nav" ref={navRef} aria-label={t.settings.title}>
          {SECTIONS.map((k) => {
            const Ic = ICONS[k]
            return <NavLink key={k} to={`/app/settings/${k}`} className={current === k ? 'active' : ''}><Ic size={17} /> <span>{t.settings.groups[k]}</span></NavLink>
          })}
        </nav>
        <div className="settings-body" key={current}>
          <div className="sb-head">
            <span className="sb-icon"><I size={20} /></span>
            <div><h2>{t.settings.groups[current]}</h2><p>{t.settings.hints[current]}</p></div>
          </div>
          {!data && current !== 'security' && current !== 'usage' ? (
            error ? (
              <div className="banner banner-warn"><IconAlert size={16} /><span>{t.settings.offline}</span>
                <button className="btn btn-soft btn-sm" onClick={reload}><IconRefresh size={14} /> {t.common.retry}</button></div>
            ) : <div className="skel-block"><span className="shimmer" /></div>
          ) : (
            <>
              {current === 'profile' && <Profile />}
              {current === 'aoi' && <AoiPrefs />}
              {current === 'table' && <TablePrefs />}
              {current === 'agents' && <AgentPrefs />}
              {current === 'studio' && <StudioPrefs />}
              {current === 'notifications' && <NotifyPrefs />}
              {current === 'appearance' && <Appearance />}
              {current === 'privacy' && <Privacy />}
              {current === 'security' && <Security />}
              {current === 'usage' && <Usage />}
            </>
          )}
        </div>
      </div>
    </div>
  )
}

// ── building blocks ───────────────────────────────────────────────────────
function Card({ children, title, danger }: { children: React.ReactNode; title?: string; danger?: boolean }) {
  return <section className={`s-card ${danger ? 'danger' : ''}`}>{title && <h3>{title}</h3>}{children}</section>
}
function Row({ label, hint, children, stack }: { label: string; hint?: string; children: React.ReactNode; stack?: boolean }) {
  return <div className={`s-row ${stack ? 'stack' : ''}`}><div className="s-label"><strong>{label}</strong>{hint && <small>{hint}</small>}</div><div className="s-ctl">{children}</div></div>
}
function Seg<T extends string | number>({ value, options, onChange, label }: { value: T; options: [T, string][]; onChange: (v: T) => void; label: string }) {
  return (
    <div className="seg small" role="radiogroup" aria-label={label}>
      {options.map(([k, l]) => <button key={String(k)} role="radio" className={value === k ? 'on' : ''} aria-checked={value === k} onClick={() => value !== k && onChange(k)}>{l}</button>)}
    </div>
  )
}
function Switch({ value, onChange, label }: { value: boolean; onChange: (v: boolean) => void; label: string }) {
  return <button className={`switch ${value ? 'on' : ''}`} role="switch" aria-checked={value} aria-label={label} onClick={() => onChange(!value)}><i /></button>
}
function Toggle({ k, label, hint, def = true }: { k: string; label: string; hint?: string; def?: boolean }) {
  const { prefs, save } = usePrefs()
  const v = prefs[k] === undefined ? def : !!prefs[k]
  return <Row label={label} hint={hint}><Switch value={v} label={label} onChange={(x) => save({ preferences: { [k]: x } })} /></Row>
}
function Choice<T extends string | number>({ k, label, hint, options, def }: { k: string; label: string; hint?: string; options: [T, string][]; def: T }) {
  const { prefs, save } = usePrefs()
  const raw = prefs[k]
  // Numbers may come back as strings (or the other way round); compare loosely.
  const v = (options.find(([o]) => String(o) === String(raw))?.[0] ?? def) as T
  return <Row label={label} hint={hint}><Seg value={v} options={options} label={label} onChange={(x) => save({ preferences: { [k]: x } })} /></Row>
}
function TextPref({ label, hint, value, placeholder, onSave, max = 40 }: { label: string; hint?: string; value: string; placeholder?: string; onSave: (v: string) => Promise<boolean> | void; max?: number }) {
  const [v, setV] = useState(value)
  useEffect(() => setV(value), [value])
  const commit = () => { if (v.trim() !== value) onSave(v.trim()) }
  return (
    <Row label={label} hint={hint}>
      <input className="input s-input" value={v} placeholder={placeholder} maxLength={max} onChange={(e) => setV(e.target.value)}
        onBlur={commit} onKeyDown={(e) => { if (e.key === 'Enter') (e.target as HTMLInputElement).blur() }} />
    </Row>
  )
}
const ent = <T extends Record<string, string>>(o: T) => Object.entries(o) as [keyof T & string, string][]

// ── sections ──────────────────────────────────────────────────────────────
function Profile() {
  const { t, lang } = useI18n()
  const { data, prefs, save } = usePrefs()
  const u = data!.user
  return (
    <Card>
      <div className="s-profile">
        <span className="me-avatar lg">{(u.name || u.email || '?').slice(0, 1).toUpperCase()}</span>
        <div><strong>{u.name || u.email}</strong><small>{u.email} {u.email_verified && <span className="ok-tag"><IconCheck size={12} /></span>}</small></div>
      </div>
      <TextPref label={t.settings.displayName} hint={t.settings.displayHint} value={u.name || ''} max={80} onSave={(v) => save({ name: v })} />
      <Row label={t.settings.language}>
        <Seg<Lang> value={(prefs.language || lang) as Lang} label={t.settings.language} options={[['en', 'English'], ['zh', '中文']]} onChange={(v) => save({ preferences: { language: v } })} />
      </Row>
      <Row label={t.settings.joined}><span className="s-value">{new Date(u.created_at).toLocaleDateString(lang === 'zh' ? 'zh-CN' : 'en-US', { year: 'numeric', month: 'long', day: 'numeric' })}</span></Row>
    </Card>
  )
}

function AoiPrefs() {
  const { t, lang } = useI18n()
  const { prefs, save } = usePrefs()
  const tone = prefs.aoi_tone || 'playful'
  const zh = lang === 'zh'
  const preview: Record<string, [string, any]> = {
    playful: [zh ? '「加注？好大胆，我喜欢。」' : '“Ooh, a raise? Brave. I like brave.”', 'wink'],
    calm: [zh ? '「慢慢来，底池不会跑。」' : '“Take your time. The pot will wait.”', 'neutral'],
    competitive: [zh ? '「跟。亮出你的底牌吧。」' : '“Call. Show me what you’ve got.”', 'angry'],
  }
  return (
    <>
      <div className="aoi-preview">
        <AoiFace mood={preview[tone]?.[1] || 'smile'} size={52} ring />
        <p>{preview[tone]?.[0]}</p>
      </div>
      <Card>
        <Choice k="aoi_tone" label={t.settings.aoiTone} def="playful" options={ent(t.settings.tones)} />
        <Choice k="aoi_talk" label={t.settings.aoiTalk} def="normal" options={ent(t.settings.talks)} />
        <Toggle k="aoi_coaching" label={t.settings.coaching} hint={t.settings.coachingHint} def={false} />
        <TextPref label={t.settings.callMe} hint={t.settings.callMeHint} placeholder={t.settings.callMePh} value={prefs.call_me || ''} onSave={(v) => save({ preferences: { call_me: v } })} />
        <Toggle k="memory_enabled" label={t.settings.memory} hint={t.settings.memoryHint} />
      </Card>
      <VoicePrefs />
    </>
  )
}

// Aoi's voice. Hidden entirely when the server has no voice configured.
function VoicePrefs() {
  const { t } = useI18n()
  const { prefs, save } = usePrefs()
  const voice = useVoice()
  const saved = Number(prefs.voice_volume ?? 80)
  const [vol, setVol] = useState(saved)
  const timer = useRef(0)
  useEffect(() => setVol(saved), [saved])
  if (!voice.enabled) return null
  const on = prefs.aoi_voice !== false
  const sampling = voice.key === 'sample' && (voice.speaking || voice.loading)
  const onVol = (v: number) => {
    setVol(v)
    setVoiceVolume(v) // hear it change right away; save once the hand stops
    window.clearTimeout(timer.current)
    timer.current = window.setTimeout(() => { if (v !== saved) save({ preferences: { voice_volume: v } }) }, 450)
  }
  return (
    <Card title={t.settings.voice}>
      <Toggle k="aoi_voice" label={t.settings.voiceOn} hint={t.settings.voiceHint} />
      {on && (
        <>
          <Toggle k="voice_autoplay" label={t.settings.autoplay} hint={t.settings.autoplayHint} def={false} />
          <Toggle k="table_voice" label={t.settings.tableVoice} hint={t.settings.tableVoiceHint} def={false} />
          <Row label={t.settings.volume}>
            <div className="vol">
              <input type="range" min={0} max={100} step={5} value={vol} onChange={(e) => onVol(Number(e.target.value))} aria-label={t.settings.volume}
                style={{ ['--v' as any]: `${vol}%` }} />
              <span>{vol}</span>
            </div>
          </Row>
          <div className="s-row">
            <div className="s-label hear">
              <AoiFace mood="smile" size={40} speaking={sampling && voice.speaking} />
              <small>{t.settings.sample}</small>
            </div>
            <button className="btn btn-soft btn-sm" onClick={() => (sampling ? stopVoice() : speak(t.settings.sample, 'sample'))}>
              {sampling ? <IconStop size={14} /> : <IconSpeaker size={15} />} {t.settings.hear}
            </button>
          </div>
        </>
      )}
    </Card>
  )
}

function Swatches({ k, def, options, kind }: { k: string; def: string; options: [string, string][]; kind: 'back' | 'felt' }) {
  const { prefs, save } = usePrefs()
  const v = prefs[k] || def
  return (
    <div className="swatches" role="radiogroup">
      {options.map(([id, label]) => (
        <button key={id} role="radio" aria-checked={v === id} className={`swatch ${v === id ? 'on' : ''}`} onClick={() => v !== id && save({ preferences: { [k]: id } })}>
          <span className={kind === 'back' ? `cardback cb-${id}` : `sw-felt sw-felt-${id}`}>{kind === 'back' && id === 'aoi' && <i>葵</i>}</span>
          <small>{label}</small>
          {v === id && <span className="sw-check"><IconCheck size={11} /></span>}
        </button>
      ))}
    </div>
  )
}

function TablePrefs() {
  const { t, f } = useI18n()
  return (
    <>
      <Card>
        <Choice<number> k="turn_seconds" label={t.settings.turnClock} hint={`${t.settings.turnHint} ${t.settings.noClockHint}`} def={30}
          options={[[15, f(t.settings.secs, { n: 15 })], [30, f(t.settings.secs, { n: 30 })], [60, f(t.settings.secs, { n: 60 })], [0, t.settings.noClock]]} />
        <Choice k="agent_speed" label={t.settings.agentSpeed} def="natural" options={ent(t.settings.speeds)} />
        <Choice k="table_talk" label={t.settings.tableTalk} def="all" options={ent(t.settings.talkModes)} />
      </Card>
      <Card>
        <Toggle k="four_color_deck" label={t.settings.fourColor} hint={t.settings.fourColorHint} def={false} />
        <Toggle k="auto_muck" label={t.settings.autoMuck} />
        <Toggle k="show_hand_strength" label={t.settings.handStrength} />
        <Toggle k="sound" label={t.settings.sound} />
        <Choice k="motion" label={t.settings.motion} def="full" options={ent(t.settings.motions)} />
      </Card>
      <Card>
        <Row label={t.settings.cardBack} stack><Swatches k="card_back" def="aoi" kind="back" options={ent(t.settings.backs)} /></Row>
        <Row label={t.settings.felt} stack><Swatches k="felt" def="navy" kind="felt" options={ent(t.settings.felts)} /></Row>
      </Card>
    </>
  )
}

function AgentPrefs() {
  const { t, lang } = useI18n()
  const { prefs, save } = usePrefs()
  const [agents, setAgents] = useState<Agent[]>(ROSTER)
  useEffect(() => {
    api.get<Agent[]>('/api/agents').then((a) => Array.isArray(a) && a.length && setAgents(a)).catch(() => {})
  }, [])
  const fav: string[] = Array.isArray(prefs.favorite_agents) ? prefs.favorite_agents : []
  const flip = (id: string) => save({ preferences: { favorite_agents: fav.includes(id) ? fav.filter((x) => x !== id) : [...fav, id] } })
  return (
    <>
      <Card>
        <Choice k="agent_difficulty" label={t.settings.difficulty} def="regular" options={ent(t.settings.difficulties)} />
        <Toggle k="fill_empty_seats" label={t.settings.fill} hint={t.settings.fillHint} />
      </Card>
      <Card title={t.settings.favourites}>
        <p className="muted s-sub">{t.settings.favouritesHint}</p>
        <div className="agent-grid">
          {agents.map((a) => {
            const on = fav.includes(a.id)
            const name = lang === 'zh' ? a.name_zh || a.name : a.name
            return (
              <button key={a.id} className={`agent-pick ${on ? 'on' : ''}`} aria-pressed={on} onClick={() => flip(a.id)}>
                <AgentAvatar id={a.id} name={a.name} src={a.avatar} size={52} />
                <span className="ap-text">
                  <strong>{name}</strong>
                  <small>{lang === 'zh' ? a.title_zh || a.title : a.title}</small>
                </span>
                <span className="ap-star"><IconStar size={16} /></span>
              </button>
            )
          })}
        </div>
      </Card>
    </>
  )
}

function StudioPrefs() {
  const { t } = useI18n()
  return (
    <Card>
      <Choice k="studio_visibility" label={t.settings.studioVis} def="private" options={ent(t.studio.vis)} />
      <Choice<number> k="playtest_games" label={t.settings.playtests} hint={t.settings.playtestHint} def={200} options={[[50, '50'], [200, '200'], [500, '500']]} />
    </Card>
  )
}

function NotifyPrefs() {
  const { t } = useI18n()
  return (
    <Card>
      <Toggle k="email_table_invites" label={t.settings.emailInvites} />
      <Toggle k="email_your_turn" label={t.settings.emailTurn} />
      <Toggle k="email_build_done" label={t.settings.emailBuild} />
    </Card>
  )
}

function Appearance() {
  const { t } = useI18n()
  const { prefs, save } = usePrefs()
  const theme = prefs.theme || 'dark'
  return (
    <Card>
      <Row label={t.settings.theme} stack>
        <div className="theme-picks" role="radiogroup">
          {(['dark', 'light', 'system'] as const).map((k) => (
            <button key={k} role="radio" aria-checked={theme === k} className={`theme-pick tp-${k} ${theme === k ? 'on' : ''}`} onClick={() => theme !== k && save({ preferences: { theme: k } })}>
              <span className="tp-art"><i /><i /><i /></span>
              <small>{t.settings.themes[k]}</small>
            </button>
          ))}
        </div>
      </Row>
      <Choice k="font_size" label={t.settings.fontSize} def="medium" options={ent(t.settings.sizes)} />
    </Card>
  )
}

function Privacy() {
  const { t } = useI18n()
  const nav = useNavigate()
  const toast = useToast()
  const [mem, setMem] = useState<{ id: string; content: string; created_at: string }[] | null>(null)
  const [pw, setPw] = useState('')
  const [err, setErr] = useState('')
  const load = () => api.get<typeof mem>('/api/memories').then((m) => setMem(m || [])).catch(() => setMem([]))
  useEffect(() => { load() }, [])
  const forget = async (id?: string) => {
    if (!id && !confirm(t.settings.confirmForgetAll)) return
    try {
      await api.del(id ? `/api/memories/${id}` : '/api/memories')
      toast(t.settings.saved)
    } catch (e: any) {
      toast(e.message, 'error')
    }
    load()
  }
  const del = async () => {
    setErr('')
    try {
      await api.post('/api/account/delete', { password: pw })
      nav('/')
      location.reload()
    } catch (e: any) {
      setErr(e.message)
    }
  }
  return (
    <>
      <Card><Toggle k="memory_enabled" label={t.settings.memory} hint={t.settings.memoryHint} /></Card>
      <Card title={t.settings.memories}>
        {mem === null ? <p className="muted">{t.common.loading}</p> : mem.length === 0 ? <p className="muted">{t.settings.noMemories}</p> : (
          <ul className="mem-list">
            {mem.map((m) => <li key={m.id}><span>{m.content}</span><button className="btn btn-ghost btn-sm" onClick={() => forget(m.id)}>{t.settings.forget}</button></li>)}
          </ul>
        )}
        {mem && mem.length > 0 && <button className="btn btn-danger btn-sm" onClick={() => forget()}>{t.settings.forgetAll}</button>}
      </Card>
      <Card title={t.settings.export}>
        <p className="muted s-sub">{t.settings.exportHint} <a href="/privacy" target="_blank" rel="noopener">{t.settings.policy}</a></p>
        <a className="btn btn-soft btn-sm" href="/api/account/export" download>{t.settings.export}</a>
      </Card>
      <Card title={t.settings.deleteAccount} danger>
        <p className="muted s-sub">{t.settings.deleteHint}</p>
        {err && <div className="alert alert-error">{err}</div>}
        <div className="inline-form">
          <input className="input" type="password" placeholder={t.settings.deleteConfirm} value={pw} onChange={(e) => setPw(e.target.value)} autoComplete="current-password" />
          <button className="btn btn-danger btn-sm" disabled={!pw} onClick={del}>{t.settings.deleteAccount}</button>
        </div>
      </Card>
    </>
  )
}

function Security() {
  const { t, lang } = useI18n()
  const toast = useToast()
  const [cur, setCur] = useState('')
  const [next, setNext] = useState('')
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null)
  const [sessions, setSessions] = useState<{ id: string; user_agent: string; ip: string; last_seen_at: string; current: boolean }[] | null>(null)
  const load = () => api.get<NonNullable<typeof sessions>>('/api/account/sessions').then((s) => setSessions(s || [])).catch(() => setSessions([]))
  useEffect(() => { load() }, [])
  const change = async () => {
    setMsg(null)
    try {
      await api.post('/api/account/password', { current: cur, next })
      setMsg({ ok: true, text: t.auth.resetDone })
      setCur(''); setNext('')
      load()
    } catch (e: any) {
      setMsg({ ok: false, text: e.message })
    }
  }
  const revoke = async (path: string, post?: boolean) => {
    try {
      if (post) await api.post(path)
      else await api.del(path)
      toast(t.settings.saved)
    } catch (e: any) {
      toast(e.message, 'error')
    }
    load()
  }
  return (
    <>
      <Card title={t.settings.password}>
        {msg && <div className={`alert ${msg.ok ? 'alert-ok' : 'alert-error'}`}>{msg.text}</div>}
        <div className="stack">
          <PasswordInput label={t.settings.current} value={cur} onChange={setCur} autoComplete="current-password" />
          <PasswordInput label={t.settings.next} value={next} onChange={setNext} autoComplete="new-password" showMeter />
          <button className="btn btn-primary btn-sm" style={{ justifySelf: 'start' }} disabled={!cur || [...next].length < 10} onClick={change}>{t.settings.updatePw}</button>
        </div>
      </Card>
      <Card title={t.settings.sessions}>
        {sessions === null ? <p className="muted">{t.common.loading}</p> : (
          <ul className="sess-list">
            {sessions.map((s) => (
              <li key={s.id}>
                <div><strong>{device(s.user_agent)}</strong><small>{s.ip} · {t.settings.lastSeen} {fmtTime(s.last_seen_at, lang)}</small></div>
                {s.current ? <span className="ok-tag">{t.settings.thisDevice}</span> : <button className="btn btn-ghost btn-sm" onClick={() => revoke(`/api/account/sessions/${s.id}`)}>{t.settings.revoke}</button>}
              </li>
            ))}
          </ul>
        )}
        {sessions && sessions.length > 1 && <button className="btn btn-danger btn-sm" onClick={() => revoke('/api/account/sessions/revoke-others', true)}>{t.settings.revokeOthers}</button>}
      </Card>
    </>
  )
}

function device(ua: string) {
  const b = /Edg\//.test(ua) ? 'Edge' : /Chrome\//.test(ua) ? 'Chrome' : /Firefox\//.test(ua) ? 'Firefox' : /Safari\//.test(ua) ? 'Safari' : 'Browser'
  const o = /iPhone|iPad/.test(ua) ? 'iOS' : /Android/.test(ua) ? 'Android' : /Mac OS/.test(ua) ? 'macOS' : /Windows/.test(ua) ? 'Windows' : /Linux/.test(ua) ? 'Linux' : ''
  return `${b}${o ? ' · ' + o : ''}`
}

function Usage() {
  const { t, lang } = useI18n()
  const { prefs, save } = usePrefs()
  const [u, setU] = useState<{ tokens_today: number; tokens_month: number; goals?: { id: string; title: string; status: string; usage: any; limits: any }[] | null } | null>(null)
  const [err, setErr] = useState(false)
  useEffect(() => { api.get<NonNullable<typeof u>>('/api/account/usage').then(setU).catch(() => setErr(true)) }, [])
  const fmt = (n: number) => (n || 0).toLocaleString(lang === 'zh' ? 'zh-CN' : 'en-US')
  return (
    <>
      <Card>
        <p className="muted s-sub">{t.settings.usageHint}</p>
        {u ? (
          <div className="usage-tiles">
            <div><small>{t.settings.today}</small><b>{fmt(u.tokens_today)}</b><span>{t.settings.tokens}</span></div>
            <div><small>{t.settings.month}</small><b>{fmt(u.tokens_month)}</b><span>{t.settings.tokens}</span></div>
          </div>
        ) : <p className="muted">{err ? t.settings.offline : t.common.loading}</p>}
      </Card>
      {u?.goals && u.goals.length > 0 && (
        <Card title={t.settings.missions}>
          <ul className="sess-list">
            {u.goals.slice(0, 8).map((g) => (
              <li key={g.id}>
                <div><strong>{g.title}</strong><small>{t.mission.status[g.status as keyof typeof t.mission.status] || g.status}</small></div>
                <span className="s-value">${Number(g.usage?.cost_usd || 0).toFixed(2)}{g.limits?.max_cost_usd ? ` / $${g.limits.max_cost_usd}` : ''}</span>
              </li>
            ))}
          </ul>
        </Card>
      )}
      <Card>
        <p className="muted s-sub">{t.settings.limitsHint}</p>
        <NumPref label={t.settings.maxCost} value={prefs.goal_max_cost_usd ?? 20} min={1} max={200} onSave={(n) => save({ preferences: { goal_max_cost_usd: n } })} />
        <NumPref label={t.settings.maxDays} value={prefs.goal_max_days ?? 30} min={1} max={180} onSave={(n) => save({ preferences: { goal_max_days: n } })} />
      </Card>
    </>
  )
}

function NumPref({ label, value, min, max, onSave }: { label: string; value: number; min: number; max: number; onSave: (n: number) => void }) {
  const [v, setV] = useState(String(value))
  useEffect(() => setV(String(value)), [value])
  const commit = () => {
    const n = Math.max(min, Math.min(max, Math.round(Number(v) || min)))
    setV(String(n))
    if (n !== value) onSave(n)
  }
  return (
    <Row label={label}>
      <input className="input s-input narrow" type="number" min={min} max={max} value={v} onChange={(e) => setV(e.target.value)} onBlur={commit}
        onKeyDown={(e) => { if (e.key === 'Enter') (e.target as HTMLInputElement).blur() }} />
    </Row>
  )
}
