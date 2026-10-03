import { useCallback, useEffect, useRef, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { AoiFace } from '../components/Aoi'
import { IconAlert, IconArrow, IconChat, IconEdit, IconPlay, IconPlus, IconRefresh, IconWand, IconX } from '../components/Icons'
import { api, type GameCard as Game, type GamesList, type Goal } from '../lib/api'
import { useI18n } from '../lib/i18n'
import { GameCover, LaneStrip, StatusPill, seatsLabel } from './cards'
import { useLive } from './live'
import { LIVE_STATUSES, ago, progress, workTasks } from './missionModel'
import { useToast } from './prefs'

export default function Studio() {
  const { t, f, lang } = useI18n()
  const { tick } = useLive()
  const nav = useNavigate()
  const toast = useToast()
  const [games, setGames] = useState<Game[] | null>(null)
  const [missions, setMissions] = useState<Goal[] | null>(null)
  const [err, setErr] = useState('')
  const [dialog, setDialog] = useState(false)

  const load = useCallback(async () => {
    const [g, goals] = await Promise.all([
      api.get<GamesList>('/api/games').catch((e) => { setErr(e.message); return null }),
      api.get<Goal[]>('/api/goals').catch((e) => { setErr(e.message); return null }),
    ])
    if (g) setGames(g.mine || [])
    else setGames((x) => x ?? [])
    if (goals) {
      const live = (goals || []).filter((x) => LIVE_STATUSES.includes(x.status)).slice(0, 8)
      const detailed = await Promise.all(live.map((x) => api.get<Goal>(`/api/goals/${x.id}`).catch(() => x)))
      setMissions(detailed)
    } else setMissions((x) => x ?? [])
    if (g && goals) setErr('')
  }, [])
  useEffect(() => { load() }, [load, tick])

  const setVis = async (g: Game, visibility: Game['visibility']) => {
    setGames((xs) => xs && xs.map((x) => (x.id === g.id ? { ...x, visibility } : x)))
    try {
      await api.patch(`/api/games/${g.id}`, { visibility })
      toast(t.settings.saved)
    } catch (e: any) {
      setGames((xs) => xs && xs.map((x) => (x.id === g.id ? { ...x, visibility: g.visibility } : x)))
      toast(f(t.settings.saveFailed, { e: e.message === 'offline' ? t.common.offline : e.message }), 'error')
    }
  }

  return (
    <div className="page studio-page">
      <header className="studio-hero">
        <div className="sh-text">
          <span className="app-eyebrow"><IconWand size={14} /> {t.studio.title}</span>
          <h1>{t.studio.heroA}<em>{t.studio.heroEm}</em></h1>
          <p>{t.studio.sub}</p>
          <button className="btn btn-primary" onClick={() => setDialog(true)}><IconPlus size={18} /> {t.studio.newGame}</button>
        </div>
        <div className="sh-team" aria-hidden="true">
          {(['designer', 'engineer', 'playtester', 'critic'] as const).map((r, i) => (
            <span key={r} className={`sh-role role-${r}`} style={{ ['--i' as any]: i }}>{t.roles[r]}</span>
          ))}
          <span className="sh-aoi"><AoiFace mood="wink" size={64} /></span>
        </div>
      </header>

      {err && (games?.length ?? 0) === 0 && (missions?.length ?? 0) === 0 && (
        <div className="banner banner-warn"><IconAlert size={16} /> <span>{err === 'offline' ? t.studio.offline : t.studio.loadError}</span>
          <button className="btn btn-soft btn-sm" onClick={load}><IconRefresh size={14} /> {t.common.retry}</button></div>
      )}

      <section className="studio-sec">
        <h2>{t.studio.workshop}{missions && missions.length > 0 && <span className="count">{missions.length}</span>}</h2>
        {missions === null ? <div className="skel-row"><span className="shimmer" /></div>
          : missions.length === 0 ? <p className="empty-line">{t.studio.noMissions}</p>
          : (
            <div className="mission-list">
              {missions.map((g) => {
                const p = progress(workTasks(g))
                return (
                  <Link key={g.id} to={`/app/studio/${g.id}`} className={`mission-row st-${g.status}`}>
                    <div className="mr-head">
                      <strong>{g.title}</strong>
                      <StatusPill status={g.status} />
                      <small>{ago(g.updated_at, lang)}</small>
                    </div>
                    <LaneStrip goal={g} compact />
                    <div className="mr-foot">
                      <div className="xc-bar"><i style={{ width: `${p.pct}%` }} /></div>
                      <span>{p.pct}%</span>
                      <IconArrow size={16} />
                    </div>
                  </Link>
                )
              })}
            </div>
          )}
      </section>

      <section className="studio-sec">
        <h2>{t.studio.mine}{games && games.length > 0 && <span className="count">{games.length}</span>}</h2>
        {games === null ? <div className="game-grid"><div className="game-tile skeleton"><span className="shimmer" /></div><div className="game-tile skeleton"><span className="shimmer" /></div></div>
          : games.length === 0 ? (
            <button className="empty-tile" onClick={() => setDialog(true)}>
              <IconWand size={22} />
              <span>{t.studio.noGames}</span>
              <em>{t.studio.newGame} →</em>
            </button>
          ) : (
            <div className="game-grid">
              {games.map((g) => (
                <article key={g.id} className="game-tile">
                  <GameCover game={g} />
                  <div className="gt-body">
                    <div className="gt-top">
                      <strong>{g.name}</strong>
                      <span className={`gstat gstat-${g.status}`}>{t.studio.status[g.status] || g.status}</span>
                    </div>
                    <p>{g.summary}</p>
                    <small>{seatsLabel(g, t, f)} · v{g.version}{g.plays ? ` · ${f(t.cards.plays, { n: g.plays })}` : ''}</small>
                  </div>
                  <div className="gt-actions">
                    <button className="btn btn-primary btn-sm" disabled={g.status === 'building'} onClick={() => nav(`/app/games/${g.id}`)}><IconPlay size={14} /> {t.studio.play}</button>
                    <button className="btn btn-soft btn-sm" onClick={() => nav('/app', { state: { prefill: f(t.studio.reviseText, { name: g.name }) } })}><IconEdit size={14} /> {t.studio.revise}</button>
                    <label className="vis-select" title={t.studio.visibility}>
                      <span className="sr-only">{t.studio.visibility}</span>
                      <select value={g.visibility} onChange={(e) => setVis(g, e.target.value as Game['visibility'])}>
                        {(['private', 'unlisted', 'public'] as const).map((v) => <option key={v} value={v}>{t.studio.vis[v]}</option>)}
                      </select>
                    </label>
                  </div>
                </article>
              ))}
            </div>
          )}
      </section>

      {dialog && <NewGameDialog onClose={() => setDialog(false)} />}
    </div>
  )
}

function NewGameDialog({ onClose }: { onClose: () => void }) {
  const { t } = useI18n()
  const nav = useNavigate()
  const [prompt, setPrompt] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const ref = useRef<HTMLTextAreaElement>(null)
  useEffect(() => {
    ref.current?.focus()
    const k = (e: KeyboardEvent) => e.key === 'Escape' && onClose()
    window.addEventListener('keydown', k)
    return () => window.removeEventListener('keydown', k)
  }, [onClose])
  const build = async () => {
    setBusy(true)
    setErr('')
    try {
      const r = await api.post<{ goal_id: string; game_id: string }>('/api/studio/build', { prompt: prompt.trim() })
      nav(`/app/studio/${r.goal_id}`)
    } catch (e: any) {
      setErr(e.message === 'offline' || e.status === 404 ? t.studio.offline : e.message)
      setBusy(false)
    }
  }
  const talk = () => nav('/app', { state: { prefill: prompt.trim() ? `${t.studio.starter}${prompt.trim()}` : t.studio.starter } })
  return (
    <div className="modal-scrim" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="modal" role="dialog" aria-modal="true" aria-labelledby="ng-title">
        <button className="btn-icon-plain modal-x" onClick={onClose} aria-label={t.common.close}><IconX /></button>
        <div className="modal-head">
          <AoiFace mood="smile" size={48} ring />
          <div>
            <h2 id="ng-title">{t.studio.dialogTitle}</h2>
            <p>{t.studio.dialogSub}</p>
          </div>
        </div>
        <textarea ref={ref} className="textarea" rows={4} value={prompt} maxLength={2000} placeholder={t.studio.dialogPh}
          onChange={(e) => setPrompt(e.target.value)}
          onKeyDown={(e) => { if (e.key === 'Enter' && (e.metaKey || e.ctrlKey) && prompt.trim()) build() }} />
        {err && <div className="alert alert-error">{err}</div>}
        <div className="modal-actions">
          <button className="btn btn-ghost" onClick={talk}><IconChat size={16} /> {t.studio.talk}</button>
          <button className="btn btn-primary" disabled={!prompt.trim() || busy} onClick={build}><IconWand size={16} /> {busy ? t.studio.building : t.studio.build}</button>
        </div>
      </div>
    </div>
  )
}
