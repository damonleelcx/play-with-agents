import { useCallback, useEffect, useRef, useState } from 'react'
import { NavLink, useNavigate, useParams } from 'react-router-dom'
import { PasswordInput } from '../pages/auth/AuthLayout'
import { api, type User } from '../lib/api'
import { useI18n, type Lang } from '../lib/i18n'
import { useSession } from '../lib/session'
import { fmtTime } from './parts'

type SettingsData = {
  user: User
  preferences: Record<string, any>
  licenses: { id: string; kind: string; number: string; jurisdiction: string; status: 'pending' | 'verified' | 'rejected'; created_at: string }[] | null
}

const TABS = ['profile', 'prefs', 'notify', 'privacy', 'security', 'limits', 'pro', 'look', 'admin'] as const
type Tab = (typeof TABS)[number]

const ZONES = ['America/Los_Angeles', 'America/Denver', 'America/Chicago', 'America/New_York', 'America/Anchorage', 'Pacific/Honolulu',
  'Europe/London', 'Europe/Berlin', 'Asia/Shanghai', 'Asia/Hong_Kong', 'Asia/Taipei', 'Asia/Singapore', 'Asia/Tokyo', 'Australia/Sydney', 'UTC']

export default function Settings({ onPrefs }: { onPrefs: (p: Record<string, any>) => void }) {
  const { tab = 'profile' } = useParams()
  const { t, setLang } = useI18n()
  const { user, setUser } = useSession()
  const [data, setData] = useState<SettingsData | null>(null)
  const [saved, setSaved] = useState('')
  const [err, setErr] = useState('')
  const navRef = useRef<HTMLElement>(null)
  // On phones the tab strip scrolls sideways; keep the active tab in view.
  useEffect(() => {
    navRef.current?.querySelector('.active')?.scrollIntoView({ block: 'nearest', inline: 'center' })
  }, [tab, data])

  const load = useCallback(() => {
    api.get<SettingsData>('/api/settings').then((d) => { setData(d); onPrefs(d.preferences) }).catch((e) => setErr(e.message))
  }, [onPrefs])
  useEffect(load, [load])

  const save = async (patch: { name?: string; preferences?: Record<string, any> }) => {
    setErr('')
    try {
      const d = await api.put<SettingsData>('/api/settings', patch)
      setData(d)
      onPrefs(d.preferences)
      if (patch.preferences?.language) setLang(patch.preferences.language as Lang)
      if (patch.name !== undefined && user) setUser({ ...user, name: d.user.name })
      setSaved(t.settings.saved)
      setTimeout(() => setSaved(''), 1800)
    } catch (e: any) {
      setErr(e.message)
    }
  }

  if (!data) return <div className="page"><p className="muted">{err || t.common.loading}</p></div>
  const p = data.preferences
  const current = (TABS as readonly string[]).includes(tab) ? (tab as Tab) : 'profile'
  const tabs = TABS.filter((x) => x !== 'admin' || user?.admin)

  return (
    <div className="page settings">
      <header className="page-head">
        <h1>{t.settings.title}</h1>
        {saved && <span className="saved-flag">✓ {saved}</span>}
      </header>
      <div className="settings-grid">
        <nav className="settings-nav" ref={navRef} aria-label={t.settings.title}>
          {tabs.map((k) => (
            <NavLink key={k} to={`/app/settings/${k}`} className={current === k ? 'active' : ''}>{t.settings.tabs[k]}</NavLink>
          ))}
        </nav>
        <div className="settings-body">
          {err && <div className="alert alert-error">{err}</div>}
          {current === 'profile' && <Profile data={data} save={save} />}
          {current === 'prefs' && (
            <Card>
              <Row label={t.settings.language}>
                <Seg value={p.language || 'en'} options={[['en', 'English'], ['zh', '中文']]} onChange={(v) => save({ preferences: { language: v } })} />
              </Row>
              <Row label={t.settings.tone}>
                <Seg value={p.tone || 'warm'} options={Object.entries(t.settings.tones)} onChange={(v) => save({ preferences: { tone: v } })} />
              </Row>
              <Row label={t.settings.verbosity}>
                <Seg value={p.verbosity || 'balanced'} options={Object.entries(t.settings.verbosities)} onChange={(v) => save({ preferences: { verbosity: v } })} />
              </Row>
              <Row label={t.settings.timezone}>
                <select className="select" value={p.timezone || Intl.DateTimeFormat().resolvedOptions().timeZone} onChange={(e) => save({ preferences: { timezone: e.target.value } })}>
                  {Array.from(new Set([Intl.DateTimeFormat().resolvedOptions().timeZone, ...ZONES])).map((z) => <option key={z} value={z}>{z}</option>)}
                </select>
              </Row>
              <TextPref label={t.settings.jurisdiction} hint={t.settings.jurisdictionHint} value={p.jurisdiction || ''} onSave={(v) => save({ preferences: { jurisdiction: v } })} />
            </Card>
          )}
          {current === 'notify' && (
            <Card>
              <Toggle label={t.settings.notifyEmail} value={p.notify_email !== false} onChange={(v) => save({ preferences: { notify_email: v } })} />
              <Toggle label={t.settings.notifyApprovals} value={p.notify_approvals !== false} onChange={(v) => save({ preferences: { notify_approvals: v } })} />
            </Card>
          )}
          {current === 'privacy' && <Privacy prefs={p} save={save} />}
          {current === 'security' && <Security />}
          {current === 'limits' && <Limits prefs={p} save={save} />}
          {current === 'pro' && <Pro data={data} reload={load} />}
          {current === 'look' && (
            <Card>
              <Row label={t.settings.theme}>
                <Seg value={p.theme || 'system'} options={Object.entries(t.settings.themes)} onChange={(v) => save({ preferences: { theme: v } })} />
              </Row>
              <Row label={t.settings.fontSize}>
                <Seg value={p.font_size || 'medium'} options={Object.entries(t.settings.sizes)} onChange={(v) => save({ preferences: { font_size: v } })} />
              </Row>
              <Toggle label={t.settings.enterToSend} value={p.enter_to_send !== false} onChange={(v) => save({ preferences: { enter_to_send: v } })} />
              <Toggle label={t.settings.reduceMotion} value={!!p.reduce_motion} onChange={(v) => save({ preferences: { reduce_motion: v } })} />
            </Card>
          )}
          {current === 'admin' && user?.admin && <Admin />}
        </div>
      </div>
    </div>
  )
}

function Card({ children, title, danger }: { children: React.ReactNode; title?: string; danger?: boolean }) {
  return <section className={`s-card ${danger ? 'danger' : ''}`}>{title && <h2>{title}</h2>}{children}</section>
}
function Row({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return <div className="s-row"><div><strong>{label}</strong>{hint && <small>{hint}</small>}</div><div className="s-ctl">{children}</div></div>
}
function Seg({ value, options, onChange }: { value: string; options: [string, string][]; onChange: (v: string) => void }) {
  return (
    <div className="seg small">
      {options.map(([k, label]) => <button key={k} className={value === k ? 'on' : ''} aria-pressed={value === k} onClick={() => onChange(k)}>{label}</button>)}
    </div>
  )
}
function Toggle({ label, hint, value, onChange }: { label: string; hint?: string; value: boolean; onChange: (v: boolean) => void }) {
  return (
    <div className="s-row">
      <div><strong>{label}</strong>{hint && <small>{hint}</small>}</div>
      <button className={`switch ${value ? 'on' : ''}`} role="switch" aria-checked={value} aria-label={label} onClick={() => onChange(!value)}><i /></button>
    </div>
  )
}
function TextPref({ label, hint, value, onSave }: { label: string; hint?: string; value: string; onSave: (v: string) => void }) {
  const { t } = useI18n()
  const [v, setV] = useState(value)
  return (
    <Row label={label} hint={hint}>
      <div className="inline-form">
        <input className="input" value={v} onChange={(e) => setV(e.target.value)} maxLength={80} />
        <button className="btn btn-soft btn-sm" disabled={v === value} onClick={() => onSave(v.trim())}>{t.settings.save}</button>
      </div>
    </Row>
  )
}

function Profile({ data, save }: { data: SettingsData; save: (p: { name?: string }) => void }) {
  const { t, lang } = useI18n()
  const [name, setName] = useState(data.user.name)
  const role = { client: lang === 'zh' ? '客户' : 'Client', attorney: lang === 'zh' ? '律师' : 'Attorney', physician: lang === 'zh' ? '医生' : 'Physician', admin: lang === 'zh' ? '管理员' : 'Admin' }[data.user.role]
  return (
    <Card>
      <div className="profile-head">
        <span className="me-avatar lg">{(data.user.name || data.user.email).slice(0, 1).toUpperCase()}</span>
        <div><strong>{data.user.name || data.user.email}</strong><small>{data.user.email}</small></div>
      </div>
      <Row label={t.settings.name}>
        <div className="inline-form">
          <input className="input" value={name} onChange={(e) => setName(e.target.value)} maxLength={80} />
          <button className="btn btn-ink btn-sm" disabled={name === data.user.name} onClick={() => save({ name })}>{t.settings.save}</button>
        </div>
      </Row>
      <Row label={t.settings.email}><span>{data.user.email} {data.user.email_verified && <span className="ok-tag">✓</span>}</span></Row>
      <Row label={t.settings.role}><span>{role}{data.user.licensed && <span className="ok-tag"> ✓ {t.settings.lic.verified}</span>}</span></Row>
      <Row label={t.settings.joined}><span>{fmtTime(data.user.created_at, lang)}</span></Row>
    </Card>
  )
}

function Privacy({ prefs, save }: { prefs: Record<string, any>; save: (p: { preferences: Record<string, any> }) => void }) {
  const { t } = useI18n()
  const nav = useNavigate()
  const [mem, setMem] = useState<{ id: string; content: string; created_at: string }[]>([])
  const [pw, setPw] = useState('')
  const [err, setErr] = useState('')
  const load = () => api.get<typeof mem>('/api/memories').then(setMem).catch(() => {})
  useEffect(() => { load() }, [])
  const forget = async (id?: string) => {
    if (!id && !confirm(t.settings.forgetAll + '?')) return
    await api.del(id ? `/api/memories/${id}` : '/api/memories')
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
      <Card>
        <Toggle label={t.settings.memory} hint={t.settings.memoryHint} value={prefs.memory_enabled !== false} onChange={(v) => save({ preferences: { memory_enabled: v } })} />
      </Card>
      <Card title={t.settings.memories}>
        {mem.length === 0 ? <p className="muted">{t.settings.noMemories}</p> : (
          <ul className="mem-list">
            {mem.map((m) => <li key={m.id}><span>{m.content}</span><button className="btn btn-ghost btn-sm" onClick={() => forget(m.id)}>{t.settings.forget}</button></li>)}
          </ul>
        )}
        {mem.length > 0 && <button className="btn btn-danger btn-sm" onClick={() => forget()}>{t.settings.forgetAll}</button>}
      </Card>
      <Card title={t.settings.export}>
        <p className="muted">{t.settings.exportHint} <a href="/privacy" target="_blank" rel="noopener">{t.settings.policy}</a></p>
        <a className="btn btn-soft btn-sm" href="/api/account/export" download>{t.settings.export}</a>
      </Card>
      <Card title={t.settings.deleteAccount} danger>
        <p className="muted">{t.settings.deleteHint}</p>
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
  const [cur, setCur] = useState('')
  const [next, setNext] = useState('')
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null)
  const [sessions, setSessions] = useState<{ id: string; user_agent: string; ip: string; last_seen_at: string; current: boolean }[]>([])
  const load = () => api.get<typeof sessions>('/api/account/sessions').then(setSessions).catch(() => {})
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
  return (
    <>
      <Card title={t.settings.password}>
        {msg && <div className={`alert ${msg.ok ? 'alert-ok' : 'alert-error'}`}>{msg.text}</div>}
        <div className="stack">
          <PasswordInput label={t.settings.current} value={cur} onChange={setCur} autoComplete="current-password" />
          <PasswordInput label={t.settings.next} value={next} onChange={setNext} autoComplete="new-password" showMeter />
          <button className="btn btn-ink btn-sm" style={{ justifySelf: 'start' }} disabled={!cur || [...next].length < 10} onClick={change}>{t.settings.updatePw}</button>
        </div>
      </Card>
      <Card title={t.settings.sessions}>
        <ul className="sess-list">
          {sessions.map((s) => (
            <li key={s.id}>
              <div><strong>{device(s.user_agent)}</strong><small>{s.ip} · {t.settings.lastSeen} {fmtTime(s.last_seen_at, lang)}</small></div>
              {s.current ? <span className="ok-tag">{t.settings.thisDevice}</span> : <button className="btn btn-ghost btn-sm" onClick={async () => { await api.del(`/api/account/sessions/${s.id}`); load() }}>{t.settings.revoke}</button>}
            </li>
          ))}
        </ul>
        {sessions.length > 1 && <button className="btn btn-danger btn-sm" onClick={async () => { await api.post('/api/account/sessions/revoke-others'); load() }}>{t.settings.revokeOthers}</button>}
      </Card>
    </>
  )
}

function device(ua: string) {
  const b = /Edg\//.test(ua) ? 'Edge' : /Chrome\//.test(ua) ? 'Chrome' : /Firefox\//.test(ua) ? 'Firefox' : /Safari\//.test(ua) ? 'Safari' : 'Browser'
  const o = /iPhone|iPad/.test(ua) ? 'iOS' : /Android/.test(ua) ? 'Android' : /Mac OS/.test(ua) ? 'macOS' : /Windows/.test(ua) ? 'Windows' : /Linux/.test(ua) ? 'Linux' : ''
  return `${b}${o ? ' · ' + o : ''}`
}

function Limits({ prefs, save }: { prefs: Record<string, any>; save: (p: { preferences: Record<string, any> }) => void }) {
  const { t } = useI18n()
  const [u, setU] = useState<{ tokens_today: number; tokens_month: number } | null>(null)
  const [cost, setCost] = useState(String(prefs.goal_max_cost_usd ?? 20))
  const [days, setDays] = useState(String(prefs.goal_max_days ?? 30))
  useEffect(() => { api.get<typeof u>('/api/account/usage').then(setU).catch(() => {}) }, [])
  return (
    <>
      <Card>
        <p className="muted">{t.settings.limitsHint}</p>
        <Row label={t.settings.maxCost}>
          <div className="inline-form"><input className="input narrow" type="number" min={1} max={200} value={cost} onChange={(e) => setCost(e.target.value)} />
            <button className="btn btn-soft btn-sm" onClick={() => save({ preferences: { goal_max_cost_usd: Number(cost) } })}>{t.settings.save}</button></div>
        </Row>
        <Row label={t.settings.maxDays}>
          <div className="inline-form"><input className="input narrow" type="number" min={1} max={180} value={days} onChange={(e) => setDays(e.target.value)} />
            <button className="btn btn-soft btn-sm" onClick={() => save({ preferences: { goal_max_days: Number(days) } })}>{t.settings.save}</button></div>
        </Row>
      </Card>
      {u && (
        <Card>
          <div className="usage big">
            <span><b>{u.tokens_today.toLocaleString()}</b> {t.settings.tokens}<small>{t.settings.today}</small></span>
            <span><b>{u.tokens_month.toLocaleString()}</b> {t.settings.tokens}<small>{t.settings.month}</small></span>
          </div>
        </Card>
      )}
    </>
  )
}

function Pro({ data, reload }: { data: SettingsData; reload: () => void }) {
  const { t } = useI18n()
  const [kind, setKind] = useState('bar')
  const [number, setNumber] = useState('')
  const [jur, setJur] = useState('')
  const [err, setErr] = useState('')
  const submit = async () => {
    setErr('')
    try {
      await api.post('/api/account/license', { kind, number, jurisdiction: jur })
      setNumber(''); setJur('')
      reload()
    } catch (e: any) {
      setErr(e.message)
    }
  }
  return (
    <>
      <Card title={t.settings.proTitle}>
        <p className="muted">{t.settings.proSub}</p>
        {err && <div className="alert alert-error">{err}</div>}
        <div className="stack">
          <label className="field"><span>{t.settings.kind}</span>
            <select className="select" value={kind} onChange={(e) => setKind(e.target.value)}>
              <option value="bar">{t.settings.bar}</option>
              <option value="medical">{t.settings.medical}</option>
            </select>
          </label>
          <label className="field"><span>{t.settings.number}</span><input className="input" value={number} onChange={(e) => setNumber(e.target.value)} /></label>
          <label className="field"><span>{t.settings.jur}</span><input className="input" value={jur} onChange={(e) => setJur(e.target.value)} placeholder="CA / NY / 上海" /></label>
          <button className="btn btn-ink btn-sm" style={{ justifySelf: 'start' }} disabled={!number || !jur} onClick={submit}>{t.settings.submit}</button>
        </div>
      </Card>
      {(data.licenses || []).length > 0 && (
        <Card>
          <ul className="sess-list">
            {data.licenses!.map((l) => (
              <li key={l.id}><div><strong>{l.kind === 'bar' ? t.settings.bar : t.settings.medical}</strong><small>{l.number} · {l.jurisdiction}</small></div>
                <span className={`lic lic-${l.status}`}>{t.settings.lic[l.status]}</span></li>
            ))}
          </ul>
        </Card>
      )}
    </>
  )
}

function Admin() {
  const { t } = useI18n()
  const [rows, setRows] = useState<{ id: string; kind: string; number: string; jurisdiction: string; status: 'pending' | 'verified' | 'rejected'; name: string; email: string }[]>([])
  const load = () => api.get<typeof rows>('/api/admin/licenses').then(setRows).catch(() => {})
  useEffect(() => { load() }, [])
  const decide = async (id: string, verify: boolean) => { await api.post(`/api/admin/licenses/${id}`, { verify }); load() }
  return (
    <Card title={t.settings.adminTitle}>
      <ul className="sess-list">
        {rows.map((r) => (
          <li key={r.id}>
            <div><strong>{r.name || r.email} · {r.kind === 'bar' ? t.settings.bar : t.settings.medical}</strong><small>{r.email} · {r.number} · {r.jurisdiction}</small></div>
            {r.status === 'pending' ? (
              <span className="inline-form"><button className="btn btn-ember btn-sm" onClick={() => decide(r.id, true)}>{t.settings.verify}</button>
                <button className="btn btn-ghost btn-sm" onClick={() => decide(r.id, false)}>{t.settings.reject}</button></span>
            ) : <span className={`lic lic-${r.status}`}>{t.settings.lic[r.status]}</span>}
          </li>
        ))}
      </ul>
    </Card>
  )
}
