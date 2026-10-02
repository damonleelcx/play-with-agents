import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { IconAlert, IconCheck, IconClock, IconX } from '../components/Icons'
import { api, type Approval, type Goal, type Task } from '../lib/api'
import { useI18n } from '../lib/i18n'

export type Mood = 'smile' | 'think' | 'laugh'

export function VeraFace({ mood = 'smile', size = 36, pulse }: { mood?: Mood; size?: number; pulse?: boolean }) {
  return (
    <span className={`vface ${pulse ? 'pulse' : ''}`} style={{ width: size, height: size }}>
      <img src={`/vera/vera-face-${mood}.webp`} alt="Vera" width={size} height={size} />
    </span>
  )
}

export function StatusPill({ status }: { status: Goal['status'] }) {
  const { t } = useI18n()
  return <span className={`pill pill-${status}`}>{t.goal.status[status]}</span>
}

export function TaskIcon({ status }: { status: string }) {
  if (status === 'succeeded') return <span className="ti ti-done"><IconCheck size={13} /></span>
  if (status === 'failed' || status === 'cancelled') return <span className="ti ti-fail"><IconX size={13} /></span>
  if (status === 'waiting_approval') return <span className="ti ti-wait"><IconAlert size={13} /></span>
  if (status === 'leased') return <span className="ti ti-run" />
  if (status === 'ready' ) return <span className="ti ti-next"><IconClock size={12} /></span>
  return <span className="ti ti-idle" />
}

// What a running step is doing right now, in words a client understands.
const toolDoing: Record<string, [string, string]> = {
  legal_search_cases: ['Searching case law', '检索判例'],
  legal_verify_citations: ['Checking every citation', '核对引用的判例'],
  legal_statute_lookup: ['Reading the statute', '查阅法条'],
  legal_deadline_calc: ['Calculating deadlines', '计算期限'],
  legal_conflict_check: ['Checking for conflicts', '核查利益冲突'],
  save_document: ['Saving a document to your file', '将文书存入你的档案'],
  get_document: ['Reading your documents', '阅读你的文件'],
  kb_search: ['Searching your file', '检索你的档案'],
  memory_save: ['Noting a fact to remember', '记下要点'],
  med_red_flag_check: ['Checking for warning signs', '排查危险信号'],
  med_icd10_lookup: ['Looking up medical codes', '查询疾病编码'],
  med_drug_normalize: ['Identifying medications', '识别药物'],
  med_drug_label: ['Reading the drug label', '查阅药品说明书'],
  med_interaction_check: ['Checking drug interactions', '检查药物相互作用'],
  med_dose_calc: ['Calculating a dose', '计算剂量'],
  med_pubmed_search: ['Searching medical research', '检索医学文献'],
  schedule_reminder: ['Setting a reminder', '设置提醒'],
  notify_user: ['Posting an update for you', '给你发送进展'],
  email_send: ['Preparing an email for approval', '准备待审批的邮件'],
  request_professional_signoff: ['Sending to a licensed professional', '提交持证专业人士审阅'],
}
const stateDoing: Record<string, [string, string]> = {
  'task.started': ['Getting started', '开始处理'],
  'task.resumed': ['Picking up where it left off', '从上次停下的地方继续'],
  'task.verify_failed': ['Double-checking and correcting', '复核并修正中'],
  'lease.reclaimed': ['Resuming after an interruption', '中断后正在恢复'],
  'task.lease_lost': ['Resuming after an interruption', '中断后正在恢复'],
  'task.retry': ['Retrying shortly', '稍后重试'],
}

function ago(at: string, lang: string) {
  const s = Math.max(0, Math.round((Date.now() - new Date(at).getTime()) / 1000))
  const m = Math.floor(s / 60)
  if (lang === 'zh') return m > 0 ? `${m} 分钟` : `${s} 秒`
  return m > 0 ? `${m}m` : `${s}s`
}

// Activity line for a step that is running, or waiting to be resumed.
export function Activity({ task }: { task: Task }) {
  const { lang } = useI18n()
  const [, setNow] = useState(0)
  useEffect(() => {
    const id = window.setInterval(() => setNow((n) => n + 1), 10000) // keep "for 2m" honest
    return () => window.clearInterval(id)
  }, [])
  const a = task.activity
  if (!a || !(task.status === 'leased' || (task.status === 'ready' && (a.type === 'lease.reclaimed' || a.type === 'task.lease_lost' || a.type === 'task.retry')))) return null
  const i = lang === 'zh' ? 1 : 0
  const label = (a.type === 'tool.called' ? toolDoing[a.tool]?.[i] : stateDoing[a.type]?.[i]) || stateDoing['task.started'][i]
  const recovering = a.type === 'lease.reclaimed' || a.type === 'task.lease_lost'
  return (
    <span className={`activity ${recovering ? 'recovering' : ''}`}>
      {label}… <small>{ago(a.at, lang)}</small>
    </span>
  )
}

export function workTasks(tasks?: Task[]) {
  return (tasks || []).filter((x) => x.kind === 'llm' || x.kind === 'wait')
}

// An inline card for a case opened from the conversation. It follows the plan
// live: steps tick over as the workers finish them.
export function GoalCard({ goal }: { goal: Goal }) {
  const { t, f } = useI18n()
  const tasks = workTasks(goal.tasks)
  const done = tasks.filter((x) => x.status === 'succeeded' || x.status === 'skipped').length
  const pct = tasks.length ? Math.round((done / tasks.length) * 100) : 0
  return (
    <div className={`goal-card domain-${goal.domain}`}>
      <div className="gc-head">
        <div>
          <small>{t.app.caseOpened} · {t.goal.domain[goal.domain]}</small>
          <strong>{goal.title}</strong>
        </div>
        <StatusPill status={goal.status} />
      </div>
      {goal.status === 'needs_attention' && goal.attention_reason && (
        <div className="gc-attn"><IconAlert size={15} /> {goal.attention_reason}</div>
      )}
      <div className="gc-bar"><i style={{ width: `${pct}%` }} /></div>
      {tasks.length > 0 ? (
        <ol className="gc-steps">
          {tasks.map((x) => (
            <li key={x.id} className={`st-${x.status}`}>
              <TaskIcon status={x.status} />
              <span className="gc-step">
                {x.title}
                <Activity task={x} />
              </span>
              {x.status !== 'blocked' && x.status !== 'succeeded' && <em>{t.goal.task[x.status as keyof typeof t.goal.task]}</em>}
            </li>
          ))}
        </ol>
      ) : (
        <p className="gc-planning"><span className="dots3"><i /><i /><i /></span> {t.goal.status.planning}</p>
      )}
      <div className="gc-foot">
        <span>{f(t.app.progress, { done, total: tasks.length })}</span>
        <Link to={`/app/cases/${goal.id}`}>{t.app.viewCase} →</Link>
      </div>
    </div>
  )
}

export function ApprovalCard({ a, onDone }: { a: Approval; onDone?: () => void }) {
  const { t, f, lang } = useI18n()
  const [note, setNote] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const role = a.required_role === 'attorney' ? (lang === 'zh' ? '律师' : 'attorney') : a.required_role === 'physician' ? (lang === 'zh' ? '医生' : 'physician') : a.required_role
  const decide = async (approve: boolean) => {
    setBusy(true)
    setErr('')
    try {
      await api.post(`/api/approvals/${a.id}`, { approve, note })
      onDone?.()
    } catch (e: any) {
      setErr(e.message)
    } finally {
      setBusy(false)
    }
  }
  const label = t.approval.tools[a.tool] || a.tool
  return (
    <div className={`approval ${a.status}`}>
      <div className="ap-head">
        <span className={`ap-gate ${a.gate}`}>{a.gate === 'G2' ? f(t.approval.g2, { role }) : t.approval.g1}</span>
        <strong>{label}</strong>
        <small>{a.goal_title}{a.client && ` · ${t.approval.client}: ${a.client.name || a.client.email}`}</small>
      </div>
      <pre className="ap-preview">{a.preview}</pre>
      {a.status === 'pending' ? (
        a.can_decide ? (
          <>
            <input className="input ap-note" placeholder={t.approval.note} value={note} onChange={(e) => setNote(e.target.value)} />
            {err && <div className="alert alert-error">{err}</div>}
            <div className="ap-actions">
              <button className="btn btn-ember btn-sm" disabled={busy} onClick={() => decide(true)}><IconCheck size={16} /> {t.approval.approve}</button>
              <button className="btn btn-ghost btn-sm" disabled={busy} onClick={() => decide(false)}>{t.approval.decline}</button>
            </div>
          </>
        ) : (
          <p className="ap-waiting"><IconClock size={15} /> {f(t.approval.waiting, { role })}</p>
        )
      ) : (
        <p className={`ap-result ${a.status}`}>{t.approval[a.status as 'approved' | 'rejected' | 'superseded']}{a.note && ` — “${a.note}”`}</p>
      )}
    </div>
  )
}

export function fmtTime(s: string, lang: string) {
  const d = new Date(s)
  return d.toLocaleString(lang === 'zh' ? 'zh-CN' : 'en-US', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
}
