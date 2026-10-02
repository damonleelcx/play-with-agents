import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Navigate, NavLink, Route, Routes, useLocation, useNavigate, useParams } from 'react-router-dom'
import { AoiFace } from '../components/Aoi'
import {
  IconArchive, IconCards, IconChat, IconChevronL, IconChevronR, IconDots, IconEdit, IconLogout, IconMenu, IconMoon, IconPlus,
  IconRefresh, IconSettings, IconStudio, IconSun, IconTrash, IconX, LogoMark,
} from '../components/Icons'
import LangSwitch from '../components/LangSwitch'
import { api, type Conversation } from '../lib/api'
import { useI18n } from '../lib/i18n'
import { useSession } from '../lib/session'
import '../styles/app.css'
import Chat from './Chat'
import { LiveProvider, useLive } from './live'
import Mission from './Mission'
import { GameDetail, JoinByCode, Lobby, TableRoom } from './play'
import { PrefsProvider, ToastProvider, resolveTheme, usePrefs } from './prefs'
import Settings from './Settings'
import Studio from './Studio'

export default function AppShell() {
  return (
    <LiveProvider>
      <ToastProvider>
        <PrefsProvider>
          <Shell />
        </PrefsProvider>
      </ToastProvider>
    </LiveProvider>
  )
}

function readCollapsed() {
  try {
    return localStorage.getItem('play.side') === 'rail'
  } catch {
    return false
  }
}

function Shell() {
  const { t } = useI18n()
  const { tick } = useLive()
  const nav = useNavigate()
  const loc = useLocation()
  const [convs, setConvs] = useState<Conversation[] | null>(null)
  const [archived, setArchived] = useState<Conversation[] | null>(null)
  const [showArchived, setShowArchived] = useState(false)
  const [convErr, setConvErr] = useState(false)
  const [q, setQ] = useState('')
  const [drawer, setDrawer] = useState(false)
  const [collapsed, setCollapsed] = useState(readCollapsed)

  const loadConvs = useCallback(() => {
    api.get<Conversation[]>('/api/conversations').then((c) => { setConvs(c || []); setConvErr(false) }).catch(() => { setConvs((x) => x ?? []); setConvErr(true) })
    if (showArchived) api.get<Conversation[]>('/api/conversations?archived=1').then((c) => setArchived(c || [])).catch(() => setArchived([]))
  }, [showArchived])
  useEffect(loadConvs, [tick, loadConvs])
  useEffect(() => setDrawer(false), [loc.pathname])

  const toggleRail = () => {
    setCollapsed((c) => {
      try { localStorage.setItem('play.side', c ? 'full' : 'rail') } catch {}
      return !c
    })
  }

  const filtered = useMemo(() => {
    const s = q.trim().toLowerCase()
    const list = convs || []
    return s ? list.filter((c) => `${c.title} ${c.last || ''}`.toLowerCase().includes(s)) : list
  }, [convs, q])

  const p = loc.pathname
  const inChat = p === '/app' || p === '/app/' || p.startsWith('/app/c/')
  const inPlay = p.startsWith('/app/games') || p.startsWith('/app/table') || p.startsWith('/app/join')
  const atTable = p.startsWith('/app/table/')
  const newChat = () => nav('/app')

  const tabs = [
    { to: '/app', label: t.app.chat, icon: <AoiFace size={22} />, on: inChat },
    { to: '/app/games', label: t.app.play, icon: <IconCards size={20} />, on: inPlay },
    { to: '/app/studio', label: t.app.studio, icon: <IconStudio size={20} />, on: p.startsWith('/app/studio') },
    { to: '/app/settings', label: t.app.settings, icon: <IconSettings size={20} />, on: p.startsWith('/app/settings') },
  ]

  return (
    <div className={`shell themed ${collapsed ? 'rail' : ''} ${atTable ? 'at-table' : ''}`}>
      <aside className={`side ${drawer ? 'open' : ''}`} aria-label={t.app.menu}>
        <div className="side-top">
          <NavLink to="/app" className="brand" end title={t.app.brand}>
            <LogoMark size={30} />
            <span className="brand-word">Play <i>with</i> Agents</span>
          </NavLink>
          <button className="btn-icon-plain desk-only rail-btn" onClick={toggleRail} aria-label={collapsed ? t.app.expand : t.app.collapse} title={collapsed ? t.app.expand : t.app.collapse}>
            {collapsed ? <IconChevronR size={18} /> : <IconChevronL size={18} />}
          </button>
          <button className="btn-icon-plain mobile-only" onClick={() => setDrawer(false)} aria-label={t.common.close}><IconX /></button>
        </div>

        <nav className="side-nav">
          {tabs.map((x) => (
            <NavLink key={x.to} to={x.to} className={`nav-item ${x.on ? 'on' : ''}`} title={x.label} aria-current={x.on ? 'page' : undefined}>
              <span className="ni-icon">{x.icon}</span>
              <span className="ni-label">{x.label}</span>
            </NavLink>
          ))}
        </nav>

        <div className="side-chats">
          <div className="sc-head">
            <span>{t.app.chats}</span>
            <button className="btn-icon-plain sm" onClick={newChat} aria-label={t.app.newChat} title={t.app.newChat}><IconPlus size={17} /></button>
          </div>
          <button className="new-chat" onClick={newChat} title={t.app.newChat}><IconPlus size={17} /><span>{t.app.newChat}</span></button>
          {(convs?.length || 0) > 6 && (
            <input className="input side-search" placeholder={t.app.search} value={q} onChange={(e) => setQ(e.target.value)} aria-label={t.app.search} />
          )}
          <div className="side-list">
            {convs === null ? <div className="side-skel"><span className="shimmer" /><span className="shimmer" /></div>
              : convErr && convs.length === 0 ? <button className="side-empty linklike" onClick={loadConvs}><IconRefresh size={13} /> {t.common.retry}</button>
              : filtered.length === 0 ? <p className="side-empty">{q ? t.app.noMatch : t.app.empty}</p>
              : filtered.map((c) => <ConvItem key={c.id} c={c} onChanged={loadConvs} />)}
            {showArchived && (archived || []).length > 0 && (
              <>
                <div className="sc-sub">{t.app.archived}</div>
                {(archived || []).map((c) => <ConvItem key={c.id} c={c} onChanged={loadConvs} />)}
              </>
            )}
          </div>
          <button className="sc-archived linklike" onClick={() => setShowArchived((s) => !s)}>
            <IconArchive size={13} /> {showArchived ? t.app.hideArchived : t.app.showArchived}
          </button>
        </div>

        <SideFoot />
      </aside>
      {drawer && <div className="scrim" onClick={() => setDrawer(false)} />}

      <main className="main">
        {!atTable && (
          <header className="mobile-bar mobile-only">
            <button className="btn-icon-plain" onClick={() => setDrawer(true)} aria-label={t.app.menu}><IconMenu /></button>
            <NavLink to="/app" className="brand" end><LogoMark size={26} /><span className="brand-word">Play <i>with</i> Agents</span></NavLink>
            <button className="btn-icon-plain" onClick={newChat} aria-label={t.app.newChat}><IconPlus size={20} /></button>
          </header>
        )}
        <div className="main-view">
          <Routes>
            <Route index element={<Chat key="new" onChanged={loadConvs} />} />
            <Route path="c/:convId" element={<KeyedChat onChanged={loadConvs} />} />
            <Route path="games" element={<Lobby />} />
            <Route path="games/:id" element={<GameDetail />} />
            <Route path="table/:id" element={<TableRoom />} />
            <Route path="join/:code" element={<JoinByCode />} />
            <Route path="studio" element={<Studio />} />
            <Route path="studio/:goalId" element={<Mission />} />
            <Route path="settings" element={<Settings />} />
            <Route path="settings/:section" element={<Settings />} />
            <Route path="*" element={<Navigate to="/app" replace />} />
          </Routes>
        </div>
        {!atTable && (
          <nav className="tabbar mobile-only" aria-label={t.app.menu}>
            {tabs.map((x) => (
              <NavLink key={x.to} to={x.to} className={x.on ? 'on' : ''} aria-current={x.on ? 'page' : undefined}>
                {x.icon}
                <span>{x.label}</span>
              </NavLink>
            ))}
          </nav>
        )}
      </main>
    </div>
  )
}

function KeyedChat({ onChanged }: { onChanged: () => void }) {
  const { convId } = useParams()
  return <Chat key={convId} onChanged={onChanged} />
}

function ConvItem({ c, onChanged }: { c: Conversation; onChanged: () => void }) {
  const { t } = useI18n()
  const nav = useNavigate()
  const loc = useLocation()
  const [menu, setMenu] = useState(false)
  const [editing, setEditing] = useState(false)
  const [title, setTitle] = useState(c.title)
  const ref = useRef<HTMLDivElement>(null)
  const active = loc.pathname === `/app/c/${c.id}`
  useEffect(() => {
    if (!menu) return
    const off = (e: MouseEvent) => { if (!ref.current?.contains(e.target as Node)) setMenu(false) }
    document.addEventListener('mousedown', off)
    return () => document.removeEventListener('mousedown', off)
  }, [menu])

  const patch = async (b: { title?: string; archived?: boolean }) => {
    setMenu(false)
    await api.patch(`/api/conversations/${c.id}`, b).catch(() => {})
    if (b.archived && active) nav('/app')
    onChanged()
  }
  const del = async () => {
    setMenu(false)
    if (!confirm(t.app.confirmDelete)) return
    await api.del(`/api/conversations/${c.id}`).catch(() => {})
    if (active) nav('/app')
    onChanged()
  }
  const label = c.title || c.last || t.app.untitled

  if (editing)
    return (
      <form className="conv editing" onSubmit={(e) => { e.preventDefault(); setEditing(false); if (title.trim() !== c.title) patch({ title: title.trim() }) }}>
        <input className="input" autoFocus value={title} maxLength={120} onChange={(e) => setTitle(e.target.value)}
          onBlur={() => { setEditing(false); if (title.trim() && title.trim() !== c.title) patch({ title: title.trim() }) }}
          onKeyDown={(e) => { if (e.key === 'Escape') { setTitle(c.title); setEditing(false) } }} />
      </form>
    )

  return (
    <div className={`conv ${active ? 'active' : ''} ${menu ? 'menu-open' : ''}`} ref={ref}>
      <NavLink to={`/app/c/${c.id}`} className="conv-link" title={label}>
        <IconChat size={15} />
        <span>{label}</span>
      </NavLink>
      <button className="conv-more" onClick={() => setMenu((m) => !m)} aria-label={t.app.more} aria-expanded={menu}><IconDots size={16} /></button>
      {menu && (
        <div className="pop conv-pop" role="menu">
          <button role="menuitem" onClick={() => { setMenu(false); setTitle(c.title || label); setEditing(true) }}><IconEdit size={15} /> {t.app.rename}</button>
          <button role="menuitem" onClick={() => patch({ archived: !c.archived })}><IconArchive size={15} /> {c.archived ? t.app.unarchive : t.app.archive}</button>
          <button role="menuitem" className="danger" onClick={del}><IconTrash size={15} /> {t.app.delete}</button>
        </div>
      )}
    </div>
  )
}

function SideFoot() {
  const { t } = useI18n()
  const { user, signOut } = useSession()
  const { online } = useLive()
  const { prefs, save } = usePrefs()
  const nav = useNavigate()
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!open) return
    const off = (e: MouseEvent) => { if (!ref.current?.contains(e.target as Node)) setOpen(false) }
    document.addEventListener('mousedown', off)
    return () => document.removeEventListener('mousedown', off)
  }, [open])
  const theme = resolveTheme(prefs.theme)
  const flipTheme = () => save({ preferences: { theme: theme === 'dark' ? 'light' : 'dark' } }, true)
  const name = user?.name || user?.email || ''
  return (
    <div className="side-foot" ref={ref}>
      {open && (
        <div className="pop me-pop" role="menu">
          <div className="mp-head"><strong>{name}</strong><small>{user?.email}</small></div>
          <button role="menuitem" onClick={() => { setOpen(false); nav('/app/settings/profile') }}><IconSettings size={15} /> {t.app.settings}</button>
          <div className="mp-row"><span>{t.app.language}</span><LangSwitch /></div>
          <button role="menuitem" onClick={async () => { await signOut(); nav('/') }}><IconLogout size={15} /> {t.app.signout}</button>
        </div>
      )}
      <button className="me" onClick={() => setOpen((o) => !o)} aria-expanded={open} aria-haspopup="menu" title={name}>
        <span className="me-avatar">{(name || '?').slice(0, 1).toUpperCase()}<i className={`live-dot ${online ? '' : 'off'}`} /></span>
        <span className="me-text">
          <strong>{name}</strong>
          <small>{online ? t.app.live : t.app.offline}</small>
        </span>
      </button>
      <button className="btn-icon-plain theme-btn" onClick={flipTheme} aria-label={t.app.theme} title={theme === 'dark' ? t.app.light : t.app.dark}>
        {theme === 'dark' ? <IconSun size={18} /> : <IconMoon size={18} />}
      </button>
    </div>
  )
}
