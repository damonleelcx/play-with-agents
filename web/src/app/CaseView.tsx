import { useCallback, useEffect, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { IconAlert, IconArrowL, IconChat, IconDoc, IconPause, IconPlay, IconX } from '../components/Icons'
import { api, type Goal, type TimelineEvent } from '../lib/api'
import { useI18n } from '../lib/i18n'
import { md } from '../lib/md'
import { useLive } from './live'
import { Activity, ApprovalCard, StatusPill, TaskIcon, fmtTime, workTasks } from './parts'

type Timeline = {
  events: TimelineEvent[]
  summaries: { summary: string; upto_event_id: number; created_at: string }[] | null
  next: { title: string; status: string; at: string }[] | null
  reminders: { due_at: string; text: string }[] | null
}

// The case view answers four questions: what happened, why, when, and what
// happens next — straight from the persisted timeline.
export default function CaseView() {
  const { id } = useParams()
  const { t, lang } = useI18n()
  const { tick } = useLive()
  const [g, setG] = useState<Goal | null>(null)
  const [tl, setTl] = useState<Timeline | null>(null)
  const [tab, setTab] = useState<'plan' | 'timeline' | 'files'>('plan')
  const [err, setErr] = useState('')

  const load = useCallback(() => {
    api.get<Goal>(`/api/goals/${id}`).then(setG).catch((e) => setErr(e.message))
    api.get<Timeline>(`/api/goals/${id}/timeline`).then(setTl).catch(() => {})
  }, [id])
  useEffect(load, [load, tick])

  const act = async (a: 'pause' | 'resume' | 'cancel') => {
    if (a === 'cancel' && !confirm(t.goal.confirmCancel)) return
    await api.post(`/api/goals/${id}/${a}`).catch((e) => alert(e.message))
    load()
  }

  if (err) return <div className="page"><div className="alert alert-error">{err}</div></div>
  if (!g) return <div className="page"><p className="muted">{t.common.loading}</p></div>
  const tasks = workTasks(g.tasks)
  const pending = (g.approvals || []).filter((a) => a.status === 'pending')
  const live = g.status === 'active' || g.status === 'planning'

  return (
    <div className="page case">
      <Link to="/app/cases" className="back"><IconArrowL size={16} /> {t.app.cases}</Link>
      <header className="case-head">
        <div>
          <small>{t.goal.domain[g.domain]} · {fmtTime(g.created_at, lang)}</small>
          <h1>{g.title}</h1>
          <StatusPill status={g.status} />
        </div>
        <div className="case-actions">
          {g.conversation_id && <Link className="btn btn-soft btn-sm" to={`/app/c/${g.conversation_id}`}><IconChat size={16} /> Vera</Link>}
          {live && <button className="btn btn-soft btn-sm" onClick={() => act('pause')}><IconPause size={16} /> {t.goal.pause}</button>}
          {(g.status === 'paused' || g.status === 'needs_attention') && <button className="btn btn-ember btn-sm" onClick={() => act('resume')}><IconPlay size={16} /> {t.goal.resume}</button>}
          {g.status !== 'completed' && g.status !== 'cancelled' && <button className="btn btn-danger btn-sm" onClick={() => act('cancel')}><IconX size={16} /> {t.goal.cancel}</button>}
        </div>
      </header>

      {g.status === 'needs_attention' && (
        <div className="attn-banner"><IconAlert size={18} /><div><strong>{t.goal.attention}</strong><p>{g.attention_reason}</p></div></div>
      )}
      {pending.map((a) => <ApprovalCard key={a.id} a={a} onDone={load} />)}

      <div className="case-cols">
        <div>
          <div className="seg" role="tablist">
            {(['plan', 'timeline', 'files'] as const).map((k) => (
              <button key={k} role="tab" aria-selected={tab === k} className={tab === k ? 'on' : ''} onClick={() => setTab(k)}>{t.goal[k]}</button>
            ))}
          </div>

          {tab === 'plan' && (
            <ol className="plan">
              {tasks.map((x) => (
                <li key={x.id} className={`st-${x.status}`}>
                  <TaskIcon status={x.status} />
                  <div>
                    <div className="plan-row"><strong>{x.title}</strong><em>{t.goal.task[x.status as keyof typeof t.goal.task]}</em></div>
                    <Activity task={x} />
                    {x.summary && <details className="plan-sum"><summary>{plain(x.summary).slice(0, 160)}{plain(x.summary).length > 160 ? '…' : ''}</summary><div className="prose" dangerouslySetInnerHTML={{ __html: md(x.summary) }} /></details>}
                    {x.error && x.status !== 'succeeded' && <p className="plan-err">{x.error}</p>}
                    {x.kind === 'wait' && x.status === 'ready' && <p className="muted">⏰ {fmtTime(x.run_after, lang)}</p>}
                  </div>
                </li>
              ))}
            </ol>
          )}

          {tab === 'timeline' && tl && (
            <div className="timeline">
              {(tl.summaries || []).map((s) => (
                <div key={s.upto_event_id} className="tl-summary"><small>{t.goal.earlier}</small><p>{s.summary}</p></div>
              ))}
              {[...tl.events].reverse().map((e) => <EventRow key={e.id} e={e} />)}
            </div>
          )}

          {tab === 'files' && (
            <div className="files">
              {(g.documents || []).length === 0 && <p className="muted">{t.docs.empty}</p>}
              {(g.documents || []).map((d) => (
                <Link to={`/app/documents/${d.id}`} key={d.id} className="file-row">
                  <IconDoc size={18} />
                  <span>{d.title}</span>
                  <small>{d.kind} · v{d.version}</small>
                </Link>
              ))}
            </div>
          )}
        </div>

        <aside className="case-side">
          <section>
            <h3>{t.goal.next}</h3>
            {(tl?.next || []).length === 0 && <p className="muted">{t.goal.nothingNext}</p>}
            <ul className="next-list">
              {(tl?.next || []).map((n, i) => (
                <li key={i}><TaskIcon status={n.status} /><span>{n.title}</span></li>
              ))}
            </ul>
          </section>
          {(tl?.reminders || []).length > 0 && (
            <section>
              <h3>{t.goal.reminders}</h3>
              <ul className="rem-list">
                {tl!.reminders!.map((r, i) => <li key={i}><small>{fmtTime(r.due_at, lang)}</small><span>{r.text}</span></li>)}
              </ul>
            </section>
          )}
          <section>
            <h3>{t.goal.criteria}</h3>
            <ul className="crit">{g.criteria.map((c) => <li key={c}>{c}</li>)}</ul>
          </section>
          <section>
            <h3>{t.goal.usage}</h3>
            <div className="usage">
              <span><b>{g.usage.iterations || 0}</b> {t.goal.steps}</span>
              <span><b>{g.usage.tool_calls || 0}</b> {t.goal.tools}</span>
              <span><b>${(g.usage.cost_usd || 0).toFixed(2)}</b> {t.goal.cost} / ${g.limits.max_cost_usd}</span>
            </div>
          </section>
        </aside>
      </div>
    </div>
  )
}

// First readable line of a Markdown summary, for the collapsed view.
function plain(s: string) {
  return s.replace(/[#*_`>|-]+/g, ' ').replace(/\s+/g, ' ').trim()
}

const eventText: Record<string, [string, string]> = {
  'goal.created': ['Case opened', '案件已建立'],
  'goal.planned': ['Plan made', '已制定计划'],
  'goal.replanned': ['Plan adjusted', '计划已调整'],
  'goal.status': ['Status changed', '状态变更'],
  'task.started': ['Started', '开始'],
  'task.resumed': ['Resumed from checkpoint', '从检查点继续'],
  'task.succeeded': ['Finished', '完成'],
  'task.verified': ['Checked and verified', '已核验'],
  'task.verify_failed': ['Check failed — correcting', '核验未通过，正在修正'],
  'task.retry': ['Will retry', '将重试'],
  'task.failed': ['Failed', '失败'],
  'task.released': ['Paused safely', '已安全暂停'],
  'task.lease_lost': ['Handed over', '已交接'],
  'lease.reclaimed': ['Recovered after an interruption', '中断后已恢复'],
  'tool.called': ['Action', '操作'],
  'approval.requested': ['Approval requested', '请求审批'],
  'approval.decided': ['Approval decided', '审批结果'],
  'budget.exceeded': ['Limit reached', '达到上限'],
  'reminder.sent': ['Reminder sent', '已发送提醒'],
  emergency: ['Emergency guidance shown', '已显示紧急指引'],
}

function EventRow({ e }: { e: TimelineEvent }) {
  const { lang } = useI18n()
  const [label] = [eventText[e.type]?.[lang === 'zh' ? 1 : 0] || e.type]
  const d = e.data || {}
  const detail = [d.task, d.tool, d.to && `→ ${d.to}`, d.decision].filter(Boolean).join(' · ')
  const why = d.why || d.reason || d.error || d.summary
  return (
    <div className={`ev ev-${e.type.split('.')[0]} ${e.type.includes('fail') ? 'bad' : ''}`}>
      <span className="ev-dot" />
      <div>
        <div className="ev-head"><strong>{label}</strong><small>{fmtTime(e.created_at, lang)}</small></div>
        {detail && <p className="ev-detail">{detail}</p>}
        {why && <p className="ev-why">{String(why).slice(0, 400)}</p>}
      </div>
    </div>
  )
}
