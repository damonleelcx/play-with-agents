import { Component, useCallback, useEffect, useState, type ReactNode } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { AoiFace, Img } from '../components/Aoi'
import { IconAlert, IconArrow, IconBolt, IconCards, IconCheck, IconEye, IconPalette, IconPlay, IconX } from '../components/Icons'
import { api, type Approval, type GameCard as Game, type Goal } from '../lib/api'
import { useI18n } from '../lib/i18n'
import { useLive } from './live'
import { lanes, progress, workTasks, type Lane, type Role } from './missionModel'

export const ROLE_ICON: Record<Role, (p: { size?: number }) => JSX.Element> = {
  designer: IconPalette, engineer: IconBolt, playtester: IconCards, critic: IconEye, coordinator: IconCards,
}

export function RoleBadge({ role, size = 30, state }: { role: Role; size?: number; state?: Lane['state'] }) {
  const I = ROLE_ICON[role]
  if (role === 'coordinator') return <AoiFace size={size} />
  return <span className={`role-badge role-${role} ${state ? 'st-' + state : ''}`} style={{ width: size, height: size }}><I size={Math.round(size * 0.52)} /></span>
}

export function StatusPill({ status }: { status: Goal['status'] }) {
  const { t } = useI18n()
  return <span className={`spill spill-${status}`}>{t.mission.status[status] || status}</span>
}

// The four studio roles, each with a progress meter: who's done, who's busy.
export function LaneStrip({ goal, compact }: { goal: Goal; compact?: boolean }) {
  const { t } = useI18n()
  const ls = lanes(workTasks(goal))
  return (
    <div className={`lane-strip ${compact ? 'compact' : ''}`}>
      {ls.map((l) => (
        <div key={l.role} className={`lane-chip st-${l.state}`} title={l.current?.title || ''}>
          <RoleBadge role={l.role} size={compact ? 26 : 30} state={l.state} />
          <div className="lane-text">
            <strong>{t.roles[l.role]}</strong>
            <small>{l.state === 'working' ? t.roles.doing[l.role] : l.total ? `${l.done}/${l.total}` : t.mission.idle}</small>
          </div>
          <span className="lane-meter"><i style={{ width: `${l.total ? (l.done / l.total) * 100 : 0}%` }} /></span>
        </div>
      ))}
    </div>
  )
}

export function PublishApproval({ a, gameName, onDone }: { a: Approval; gameName?: string; onDone?: () => void }) {
  const { t, f } = useI18n()
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const decide = async (approve: boolean) => {
    setBusy(true)
    setErr('')
    try {
      await api.post(`/api/approvals/${a.id}`, { approve, note: '' })
      onDone?.()
    } catch (e: any) {
      setErr(e.message)
    } finally {
      setBusy(false)
    }
  }
  if (a.status !== 'pending')
    return <p className={`ap-result ${a.status}`}>{a.status === 'approved' ? <><IconCheck size={14} /> {t.cards.published}</> : t.cards.declined}</p>
  return (
    <div className="publish-q">
      <div className="pq-text">
        <strong>{t.cards.publishQ}</strong>
        <span>{a.preview || f(t.cards.publishHint, { name: gameName || a.goal_title })}</span>
      </div>
      {err && <div className="alert alert-error">{err}</div>}
      {a.can_decide && (
        <div className="pq-actions">
          <button className="btn btn-ember btn-sm" disabled={busy} onClick={() => decide(true)}><IconCheck size={15} /> {t.cards.publish}</button>
          <button className="btn btn-ghost btn-sm" disabled={busy} onClick={() => decide(false)}><IconX size={15} /> {t.cards.notYet}</button>
        </div>
      )}
    </div>
  )
}

export function useGoal(goalId: string | undefined) {
  const { tick } = useLive()
  const [g, setG] = useState<Goal | null>(null)
  const [err, setErr] = useState<number | null>(null)
  const load = useCallback(() => {
    if (!goalId) return
    api.get<Goal>(`/api/goals/${goalId}`).then((x) => { setG(x); setErr(null) }).catch((e) => setErr(e.status ?? 0))
  }, [goalId])
  useEffect(load, [load, tick])
  // Between SSE nudges, a running mission is polled gently.
  useEffect(() => {
    if (!g || !['planning', 'active'].includes(g.status)) return
    const id = window.setInterval(load, 8000)
    return () => window.clearInterval(id)
  }, [g, load])
  return { g, err, load }
}

export function MissionCard({ goalId }: { goalId: string }) {
  const { t, f } = useI18n()
  const { g, err, load } = useGoal(goalId)
  if (!g) return <div className="xcard mission-card skeleton">{err !== null ? <p className="muted">{t.cards.unavailable}</p> : <span className="shimmer" />}</div>
  const tasks = workTasks(g)
  const p = progress(tasks)
  const decision = (g.approvals || []).filter((a) => a.status === 'pending' || a.status === 'approved').slice(0, 1)
  return (
    <div className={`xcard mission-card st-${g.status}`}>
      <div className="xc-head">
        <span className="xc-kicker"><IconPalette size={14} /> {t.cards.mission}</span>
        <StatusPill status={g.status} />
      </div>
      <strong className="xc-title">{g.title}</strong>
      {g.status === 'needs_attention' && g.attention_reason && <div className="xc-attn"><IconAlert size={15} /> {g.attention_reason}</div>}
      <LaneStrip goal={g} compact />
      <div className="xc-bar"><i style={{ width: `${p.pct}%` }} /></div>
      {decision.map((a) => <PublishApproval key={a.id} a={a} onDone={load} />)}
      <div className="xc-foot">
        <span>{tasks.length ? f(t.cards.step, { done: p.done, total: p.total }) : t.cards.planning}</span>
        <Link to={`/app/studio/${g.id}`}>{t.cards.viewMission} <IconArrow size={14} /></Link>
      </div>
    </div>
  )
}

export function GameCover({ game, size = 'md' }: { game: Pick<Game, 'id' | 'name' | 'cover'>; size?: 'sm' | 'md' }) {
  const hue = [...game.id].reduce((h, c) => Math.imul(h ^ c.charCodeAt(0), 16777619) >>> 0, 2166136261) % 360
  const initials = game.name.split(/\s+/).map((w) => [...w][0] || '').join('').slice(0, 2).toUpperCase()
  const fallback = (
    <span className="cover-fallback" style={{ ['--h' as any]: hue }}>
      {game.id === 'holdem' ? <span className="cover-suits">♠<i>♥</i></span> : <span>{initials}</span>}
    </span>
  )
  return (
    <span className={`game-cover ${size}`}>
      <Img srcs={game.cover ? [game.cover] : []} alt="" fallback={fallback} />
    </span>
  )
}

export function seatsLabel(g: Pick<Game, 'min_seats' | 'max_seats'>, t: any, f: (s: string, v: Record<string, string | number>) => string) {
  return g.min_seats === g.max_seats ? f(t.cards.seatsOne, { n: g.min_seats }) : f(t.cards.seats, { min: g.min_seats, max: g.max_seats })
}

export function GameCard({ gameId }: { gameId: string }) {
  const { t, f } = useI18n()
  const nav = useNavigate()
  const [g, setG] = useState<Game | null>(null)
  const [err, setErr] = useState(false)
  useEffect(() => {
    api.get<Game>(`/api/games/${gameId}`).then(setG).catch(() => setErr(true))
  }, [gameId])
  if (!g) return <div className="xcard game-card skeleton">{err ? <p className="muted">{t.cards.unavailable}</p> : <span className="shimmer" />}</div>
  return (
    <div className="xcard game-card">
      <GameCover game={g} />
      <div className="gc-body">
        <span className="xc-kicker"><IconCards size={14} /> {t.cards.game}{g.status !== 'published' && <em className={`gstat gstat-${g.status}`}>{t.studio.status[g.status]}</em>}</span>
        <strong className="xc-title">{g.name}</strong>
        <p>{g.summary}</p>
        <div className="gc-meta">
          <span>{seatsLabel(g, t, f)}</span>
          {g.plays > 0 && <span>{f(t.cards.plays, { n: g.plays })}</span>}
        </div>
      </div>
      <button className="btn btn-primary btn-sm gc-play" onClick={() => nav(`/app/games/${g.id}`)} disabled={g.status === 'building'}><IconPlay size={14} /> {t.cards.play}</button>
    </div>
  )
}

// One broken card (a shape the server changed, a module still being written)
// must not take the whole conversation down with it.
export class CardBoundary extends Component<{ children: ReactNode; fallback: ReactNode }, { failed: boolean }> {
  state = { failed: false }
  static getDerivedStateFromError() {
    return { failed: true }
  }
  componentDidCatch(e: unknown) {
    console.warn('chat card failed to render', e)
  }
  render() {
    return this.state.failed ? this.props.fallback : this.props.children
  }
}
