import { useCallback, useEffect, useState } from 'react'
import { api, type Approval } from '../lib/api'
import { useI18n } from '../lib/i18n'
import { useSession } from '../lib/session'
import { useLive } from './live'
import { ApprovalCard } from './parts'

export default function Approvals() {
  const { t } = useI18n()
  const { user } = useSession()
  const { tick } = useLive()
  const [items, setItems] = useState<Approval[] | null>(null)
  const load = useCallback(() => {
    api.get<Approval[]>('/api/approvals').then(setItems).catch(() => setItems([]))
  }, [])
  useEffect(load, [load, tick])
  const own = (items || []).filter((a) => a.own)
  const queue = (items || []).filter((a) => !a.own)
  return (
    <div className="page">
      <header className="page-head"><h1>{t.app.approvals}</h1></header>
      {items === null && <p className="muted">{t.common.loading}</p>}
      {items && own.length === 0 && queue.length === 0 && (
        <div className="empty-card"><img src="/vera/vera-face-laugh.webp" alt="" width={72} height={72} /><p>{t.approval.none}</p></div>
      )}
      <div className="ap-list">{own.map((a) => <ApprovalCard key={a.id} a={a} onDone={load} />)}</div>
      {user?.licensed && (
        <>
          <h2 className="section-title">{t.approval.queue}</h2>
          <div className="ap-list">{queue.map((a) => <ApprovalCard key={a.id} a={a} onDone={load} />)}</div>
          {queue.length === 0 && <p className="muted">{t.approval.none}</p>}
        </>
      )}
    </div>
  )
}
