import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { IconArrowUR } from '../components/Icons'
import { api, type Goal } from '../lib/api'
import { useI18n } from '../lib/i18n'
import { useLive } from './live'
import { StatusPill, fmtTime } from './parts'

export default function Cases() {
  const { t, f, lang } = useI18n()
  const { tick } = useLive()
  const [goals, setGoals] = useState<Goal[] | null>(null)
  useEffect(() => {
    api.get<Goal[]>('/api/goals').then(setGoals).catch(() => setGoals([]))
  }, [tick])
  return (
    <div className="page">
      <header className="page-head">
        <h1>{t.app.cases}</h1>
      </header>
      {goals === null ? (
        <p className="muted">{t.common.loading}</p>
      ) : goals.length === 0 ? (
        <div className="empty-card"><img src="/vera/vera-face-think.webp" alt="" width={72} height={72} /><p>{t.goal.none}</p></div>
      ) : (
        <div className="case-grid">
          {goals.map((g) => {
            const p = g.progress || { done: 0, total: 0 }
            return (
              <Link to={`/app/cases/${g.id}`} key={g.id} className={`case-tile domain-${g.domain}`}>
                <div className="ct-top">
                  <small>{t.goal.domain[g.domain]}</small>
                  <StatusPill status={g.status} />
                </div>
                <strong>{g.title}</strong>
                {g.status === 'needs_attention' && g.attention_reason && <p className="ct-attn">{g.attention_reason}</p>}
                <div className="gc-bar"><i style={{ width: `${p.total ? (p.done / p.total) * 100 : 0}%` }} /></div>
                <div className="ct-foot">
                  <span>{f(t.app.progress, { done: p.done, total: p.total })}</span>
                  <span>{fmtTime(g.updated_at, lang)} <IconArrowUR size={14} /></span>
                </div>
              </Link>
            )
          })}
        </div>
      )}
    </div>
  )
}
