import { useCallback, useEffect, useMemo, useState } from 'react'
import { NavLink, Route, Routes, useLocation, useNavigate } from 'react-router-dom'
import { IconChat, IconCheck, IconDoc, IconFolder, IconLogout, IconMenu, IconPlus, IconSettings, IconX, LogoMark } from '../components/Icons'
import LangSwitch from '../components/LangSwitch'
import { api, type Approval, type Conversation } from '../lib/api'
import { useI18n } from '../lib/i18n'
import { useSession } from '../lib/session'
import '../styles/app.css'
import Approvals from './Approvals'
import CaseView from './CaseView'
import Cases from './Cases'
import Chat from './Chat'
import Documents from './Documents'
import { LiveProvider, useLive } from './live'
import Settings from './Settings'

export default function AppShell() {
  return (
    <LiveProvider>
      <Shell />
    </LiveProvider>
  )
}

function Shell() {
  const { t } = useI18n()
  const { user, signOut } = useSession()
  const { tick, online } = useLive()
  const nav = useNavigate()
  const loc = useLocation()
  const [convs, setConvs] = useState<Conversation[]>([])
  const [pending, setPending] = useState(0)
  const [q, setQ] = useState('')
  const [open, setOpen] = useState(false)
  const [prefs, setPrefs] = useState<Record<string, any>>({})

  const loadConvs = useCallback(() => {
    api.get<Conversation[]>('/api/conversations').then(setConvs).catch(() => {})
    api.get<Approval[]>('/api/approvals').then((a) => setPending(a.filter((x) => x.status === 'pending' && x.can_decide).length)).catch(() => {})
  }, [])
  useEffect(loadConvs, [tick, loadConvs, loc.pathname])

  // Appearance preferences live on the server; apply them to the document.
  useEffect(() => {
    api.get<{ preferences: Record<string, any> }>('/api/settings').then((s) => setPrefs(s.preferences || {})).catch(() => {})
  }, [])
  useEffect(() => {
    const d = document.documentElement
    d.dataset.theme = prefs.theme && prefs.theme !== 'system' ? prefs.theme : ''
    if (!d.dataset.theme) delete d.dataset.theme
    d.dataset.fs = prefs.font_size || 'medium'
    if (prefs.reduce_motion) d.dataset.motion = 'reduce'
    else delete d.dataset.motion
    document.body.classList.add('themed-body')
    return () => document.body.classList.remove('themed-body')
  }, [prefs])
  useEffect(() => setOpen(false), [loc.pathname])

  const filtered = useMemo(() => {
    const s = q.trim().toLowerCase()
    return s ? convs.filter((c) => (c.title || c.last || '').toLowerCase().includes(s)) : convs
  }, [convs, q])

  const newChat = async () => {
    const c = await api.post<{ id: string }>('/api/conversations')
    nav(`/app/c/${c.id}`)
  }

  return (
    <div className="shell themed">
      <aside className={`side ${open ? 'open' : ''}`}>
        <div className="side-top">
          <NavLink to="/app" className="side-logo" end><LogoMark size={26} /><span>ACT</span></NavLink>
          <button className="btn-icon-plain mobile-only" onClick={() => setOpen(false)} aria-label={t.common.close}><IconX /></button>
        </div>
        <button className="btn btn-ink side-new" onClick={newChat}><IconPlus size={18} /> {t.app.newChat}</button>
        <nav className="side-nav">
          <NavLink to="/app/cases"><IconFolder size={18} /> {t.app.cases}</NavLink>
          <NavLink to="/app/documents"><IconDoc size={18} /> {t.app.documents}</NavLink>
          <NavLink to="/app/approvals"><IconCheck size={18} /> {t.app.approvals}{pending > 0 && <span className="badge">{pending}</span>}</NavLink>
          <NavLink to="/app/settings"><IconSettings size={18} /> {t.app.settings}</NavLink>
        </nav>
        <div className="side-search">
          <input className="input" placeholder={t.app.search} value={q} onChange={(e) => setQ(e.target.value)} aria-label={t.app.search} />
        </div>
        <div className="side-list" aria-label={t.app.chats}>
          {filtered.length === 0 && <p className="side-empty">{t.app.empty}</p>}
          {filtered.map((c) => (
            <NavLink key={c.id} to={`/app/c/${c.id}`} className="conv">
              <IconChat size={16} />
              <span>{c.title || c.last || t.app.newChat}</span>
            </NavLink>
          ))}
        </div>
        <div className="side-foot">
          <div className="me">
            <span className="me-avatar">{(user?.name || user?.email || '?').slice(0, 1).toUpperCase()}</span>
            <span className="me-text">
              <strong>{user?.name || user?.email}</strong>
              <small>{online ? <><i className="live-dot" /> {t.app.live}</> : t.app.offline}</small>
            </span>
            <button className="btn-icon-plain" title={t.auth.signout} aria-label={t.auth.signout} onClick={async () => { await signOut(); nav('/') }}><IconLogout size={18} /></button>
          </div>
          <LangSwitch />
        </div>
      </aside>
      {open && <div className="scrim" onClick={() => setOpen(false)} />}

      <main className="main">
        {/* Phones: a real top bar, so nothing scrolls underneath a floating button. */}
        <header className="mobile-bar mobile-only">
          <button className="btn-icon-plain" onClick={() => setOpen(true)} aria-label="Menu"><IconMenu />{pending > 0 && <span className="dot-badge" />}</button>
          <NavLink to="/app" className="side-logo" end><LogoMark size={24} /><span>ACT</span></NavLink>
          <button className="btn-icon-plain" onClick={newChat} aria-label={t.app.newChat}><IconPlus size={20} /></button>
        </header>
        <Routes>
          <Route index element={<Chat key="new" onChanged={loadConvs} />} />
          <Route path="c/:id" element={<Chat onChanged={loadConvs} />} />
          <Route path="cases" element={<Cases />} />
          <Route path="cases/:id" element={<CaseView />} />
          <Route path="documents" element={<Documents />} />
          <Route path="documents/:id" element={<Documents />} />
          <Route path="approvals" element={<Approvals />} />
          <Route path="settings" element={<Settings onPrefs={setPrefs} />} />
          <Route path="settings/:tab" element={<Settings onPrefs={setPrefs} />} />
        </Routes>
      </main>
    </div>
  )
}
