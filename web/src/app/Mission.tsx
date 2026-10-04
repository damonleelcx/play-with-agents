import { useCallback, useEffect, useMemo, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { IconAlert, IconArrowL, IconChat, IconCheck, IconChevronD, IconClock, IconPause, IconPlay, IconRefresh, IconX } from '../components/Icons'
import { api, type GameDetail, type Task, type Timeline, type TimelineEvent } from '../lib/api'
import { useI18n } from '../lib/i18n'
import { md } from '../lib/md'
import { LaneStrip, PublishApproval, RoleBadge, StatusPill, useGoal } from './cards'
import { useLive } from './live'
import { ago, fmtTime, gameIdOf, progress, roleOf, shownStatus, stepTitle, workTasks, type Shown } from './missionModel'
import { useToast } from './prefs'

// The mission view answers: who is doing what, what happened and why, what
// changed, and what happens next, straight from the persisted goal + timeline.
export default function Mission() {
  const { goalId } = useParams()
  const { t, lang } = useI18n()
  const { tick } = useLive()
  const toast = useToast()
  const nav = useNavigate()
  const { g, err, load } = useGoal(goalId)
  const [tl, setTl] = useState<Timeline | null>(null)
  const [game, setGame] = useState<GameDetail | null>(null)
  const [tab, setTab] = useState<'steps' | 'timeline'>('steps')
  const [busy, setBusy] = useState(false)

  const loadTl = useCallback(() => {
    api.get<Timeline>(`/api/goals/${goalId}/timeline`).then(setTl).catch(() => {})
  }, [goalId])
  useEffect(loadTl, [loadTl, tick, g?.updated_at])

  const gid = gameIdOf(g)
  useEffect(() => {
    if (gid) api.get<GameDetail>(`/api/games/${gid}`).then(setGame).catch(() => {})
  }, [gid, tick])

  const act = async (a: 'pause' | 'resume' | 'cancel') => {
    if (a === 'cancel' && !confirm(t.mission.confirmCancel)) return
    setBusy(true)
    try {
      await api.post(`/api/goals/${goalId}/${a}`)
      load()
    } catch (e: any) {
      toast(e.message === 'offline' ? t.common.offline : e.message, 'error')
    } finally {
      setBusy(false)
    }
  }

  const tasks = useMemo(() => workTasks(g), [g])
  const byId = useMemo(() => new Map(tasks.map((x) => [x.id, x])), [tasks])

  if (!g)
    return (
      <div className="page mission">
        <Link to="/app/studio" className="back"><IconArrowL size={16} /> {t.mission.back}</Link>
        {err !== null ? (
          <div className="banner banner-warn"><IconAlert size={16} /> <span>{err === 404 ? t.mission.notFound : t.studio.offline}</span>
            {err !== 404 && <button className="btn btn-soft btn-sm" onClick={load}><IconRefresh size={14} /> {t.common.retry}</button>}</div>
        ) : <div className="skel-block"><span className="shimmer" /></div>}
      </div>
    )

  const p = progress(tasks)
  const approvals = g.approvals || []
  const live = g.status === 'active' || g.status === 'planning'
  const report = playtestReport(tasks, game)
  const critic = criticReview(game) || [...tasks].reverse().find((x) => roleOf(x) === 'critic' && x.summary)?.summary || ''

  return (
    <div className="page mission">
      <Link to="/app/studio" className="back"><IconArrowL size={16} /> {t.mission.back}</Link>
      <header className="mission-head">
        <div className="mh-text">
          <div className="mh-meta"><StatusPill status={g.status} /><small>{fmtTime(g.created_at, lang)}</small></div>
          <h1>{g.title}</h1>
          {g.objective && g.objective !== g.title && <p>{g.objective}</p>}
        </div>
        <div className="mh-actions">
          {gid && <button className="btn btn-primary btn-sm" onClick={() => nav(`/app/games/${gid}`)}><IconPlay size={14} /> {t.mission.playDraft}</button>}
          {g.conversation_id && <Link className="btn btn-soft btn-sm" to={`/app/c/${g.conversation_id}`}><IconChat size={15} /> {t.mission.chat}</Link>}
          {live && <button className="btn btn-soft btn-sm" disabled={busy} onClick={() => act('pause')}><IconPause size={15} /> {t.mission.pause}</button>}
          {(g.status === 'paused' || g.status === 'needs_attention') && <button className="btn btn-ember btn-sm" disabled={busy} onClick={() => act('resume')}><IconPlay size={15} /> {t.mission.resume}</button>}
          {!['completed', 'cancelled', 'failed'].includes(g.status) && <button className="btn btn-danger btn-sm" disabled={busy} onClick={() => act('cancel')}><IconX size={15} /> {t.mission.cancel}</button>}
        </div>
      </header>

      <div className="mission-progress">
        <div className="xc-bar lg"><i style={{ width: `${p.pct}%` }} /></div>
        <span>{p.done}/{p.total || '–'}</span>
      </div>

      {g.status === 'needs_attention' && (
        <div className="banner banner-attn"><IconAlert size={18} /><div><strong>{t.mission.attention}</strong><p>{g.attention_reason}</p></div></div>
      )}

      {approvals.filter((a) => a.status === 'pending').length > 0 && (
        <section className="decision">
          <h3>{t.mission.approvals}</h3>
          {approvals.filter((a) => a.status === 'pending').map((a) => <PublishApproval key={a.id} a={a} gameName={game?.name} onDone={load} />)}
        </section>
      )}

      <section className="lanes-sec">
        <h3>{t.mission.lanes}</h3>
        <LaneStrip goal={g} />
      </section>

      <div className="mission-cols">
        <div className="mc-main">
          <div className="seg" role="tablist">
            {(['steps', 'timeline'] as const).map((k) => (
              <button key={k} role="tab" aria-selected={tab === k} className={tab === k ? 'on' : ''} onClick={() => setTab(k)}>{t.mission[k]}</button>
            ))}
          </div>
          {tab === 'steps' ? (
            tasks.length === 0 ? <p className="empty-line"><span className="dots3"><i /><i /><i /></span> {t.cards.planning}</p> : (
              <ol className="stepper">
                {tasks.map((x) => <Step key={x.id} x={x} byId={byId} />)}
              </ol>
            )
          ) : (
            <div className="timeline">
              {(tl?.summaries || []).map((s) => (
                <div key={s.upto_event_id} className="tl-summary"><small>{t.mission.earlier}</small><p>{s.summary}</p></div>
              ))}
              {(tl?.events || []).length === 0 && <p className="empty-line">{t.mission.noEvents}</p>}
              {[...(tl?.events || [])].reverse().map((e) => <EventRow key={e.id} e={e} task={e.task_id ? byId.get(e.task_id) : undefined} />)}
            </div>
          )}
        </div>

        <aside className="mc-side">
          <section className="side-card">
            <h3><RoleBadge role="playtester" size={24} /> {t.mission.report}</h3>
            {report ? <div className="prose sm" dangerouslySetInnerHTML={{ __html: md(report) }} /> : <p className="muted">{t.mission.noReport}</p>}
          </section>
          <section className="side-card">
            <h3><RoleBadge role="critic" size={24} /> {t.mission.critic}</h3>
            {critic ? <div className="prose sm" dangerouslySetInnerHTML={{ __html: md(critic) }} /> : <p className="muted">{t.mission.noCritic}</p>}
          </section>
          <section className="side-card">
            <h3><IconClock size={16} /> {t.mission.next}</h3>
            {(tl?.next || []).length === 0 ? <p className="muted">{t.mission.nothingNext}</p> : (
              <ul className="next-list">
                {(tl?.next || []).map((n, i) => <li key={i}><StepDot s={n.status === 'leased' ? 'running' : (n.status as Shown)} /><span>{stepTitle(n.title, t.steps)}</span></li>)}
              </ul>
            )}
          </section>
          {g.usage && (
            <section className="side-card usage-card">
              <h3>{t.mission.usage}</h3>
              <div className="usage">
                <span><b>{g.usage.tool_calls || 0}</b> {t.mission.calls}</span>
                <span><b>${(g.usage.cost_usd || 0).toFixed(2)}</b> {t.mission.cost}{g.limits?.max_cost_usd ? ` / $${g.limits.max_cost_usd}` : ''}</span>
              </div>
            </section>
          )}
        </aside>
      </div>
    </div>
  )
}

// The critic's review stored on the newest reviewed version (a "revise"
// verdict fails its task, so the task summary alone would hide it).
function criticReview(game: GameDetail | null): string {
  for (const v of (game?.versions || []).slice().sort((a, b) => b.version - a.version)) {
    const r: any = v.report
    if (r && typeof r === 'object' && r.review && typeof r.review.markdown === 'string') return r.review.markdown
  }
  return ''
}

// The playtest report: the game's latest version report when there is one,
// else what the playtester wrote in its step summary.
function playtestReport(tasks: Task[], game: GameDetail | null): string {
  const v = (game?.versions || []).slice().sort((a, b) => b.version - a.version)[0]
  const r = v?.report
  if (typeof r === 'string' && r.trim()) return r
  if (r && typeof r === 'object') {
    if (typeof r.markdown === 'string') return r.markdown
    if (typeof r.summary === 'string' && Object.keys(r).length === 1) return r.summary
    const rows = Object.entries(r).filter(([k, x]) => k !== 'summary' && x !== null && typeof x !== 'object').map(([k, x]) => `| ${k.replace(/_/g, ' ')} | ${x} |`)
    if (rows.length) return `| | |\n|---|---|\n${rows.join('\n')}${typeof r.summary === 'string' ? `\n\n${r.summary}` : ''}`
  }
  const pt = [...tasks].reverse().find((x) => roleOf(x) === 'playtester' && x.summary)
  return pt?.summary || ''
}

export function StepDot({ s }: { s: Shown }) {
  return (
    <span className={`sdot sd-${s}`} aria-hidden="true">
      {s === 'done' ? <IconCheck size={12} /> : s === 'failed' || s === 'cancelled' ? <IconX size={12} /> : s === 'waiting_approval' ? <IconAlert size={12} /> : s === 'retrying' ? <IconRefresh size={11} /> : null}
    </span>
  )
}

function plain(s: string) {
  return s.replace(/[#*_`>|-]+/g, ' ').replace(/\s+/g, ' ').trim()
}

function Step({ x, byId }: { x: Task; byId: Map<string, Task> }) {
  const { t, f, lang } = useI18n()
  const s = shownStatus(x)
  const role = roleOf(x)
  const [open, setOpen] = useState(false)
  const deps = (x.deps || []).map((d) => stepTitle(byId.get(d)?.title, t.steps)).filter(Boolean) as string[]
  const summary = x.summary ? plain(x.summary) : ''
  return (
    <li className={`step sx-${s}`}>
      <div className="step-rail"><StepDot s={s} /></div>
      <div className="step-body">
        <div className="step-row">
          <RoleBadge role={role} size={22} />
          <strong>{stepTitle(x.title, t.steps)}</strong>
          <em className={`st-label sl-${s}`}>{t.mission.task[s] || s}</em>
        </div>
        <div className="step-meta">
          <span>{t.roles[role]}</span>
          {x.attempts > 1 && <span>{f(t.mission.attempt, { n: x.attempts })}</span>}
          {s === 'running' && x.activity && <span className="activity">{t.roles.doing[role]}… {ago(x.activity.at, lang)}</span>}
          {s === 'done' && x.finished_at && <span>{fmtTime(x.finished_at, lang)}</span>}
          {s === 'blocked' && deps.length > 0 && <span>← {deps.join(', ')}</span>}
        </div>
        {x.error && s !== 'done' && <p className="step-err">{x.error}</p>}
        {summary && (
          <div className={`step-sum ${open ? 'open' : ''}`}>
            {open ? <div className="prose sm" dangerouslySetInnerHTML={{ __html: md(x.summary) }} /> : <p>{summary.slice(0, 180)}{summary.length > 180 ? '…' : ''}</p>}
            {summary.length > 0 && <button className="linklike" onClick={() => setOpen(!open)} aria-expanded={open} aria-label={open ? '−' : '+'}><IconChevronD size={15} /></button>}
          </div>
        )}
      </div>
    </li>
  )
}

function EventRow({ e, task }: { e: TimelineEvent; task?: Task }) {
  const { t, lang } = useI18n()
  const label = t.mission.events[e.type] || e.type
  const d = e.data || {}
  const role = task ? roleOf(task) : 'coordinator'
  const detail = [stepTitle(d.task || task?.title, t.steps), d.tool, d.to && `→ ${d.to}`, d.decision].filter(Boolean).join(' · ')
  const why = d.why || d.reason || d.error
  const changed = d.changed || d.diff || d.summary
  const next = d.next
  const bad = /fail|exceeded/.test(e.type)
  return (
    <div className={`ev ${bad ? 'bad' : ''} ev-${e.type.split('.')[0]}`}>
      <span className="ev-av"><RoleBadge role={role} size={26} /></span>
      <div className="ev-body">
        <div className="ev-head"><strong>{label}</strong><small>{fmtTime(e.created_at, lang)}</small></div>
        {detail && <p className="ev-detail">{detail}</p>}
        {why && <p className="ev-why"><b>{t.mission.why}</b> {String(why).slice(0, 400)}</p>}
        {changed && <p className="ev-why"><b>{t.mission.changed}</b> {String(changed).slice(0, 400)}</p>}
        {next && <p className="ev-why"><b>{t.mission.next}</b> {String(next).slice(0, 300)}</p>}
      </div>
    </div>
  )
}
