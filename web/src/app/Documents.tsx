import { useEffect, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { IconArrowL, IconDoc, IconUpload } from '../components/Icons'
import { api } from '../lib/api'
import { useI18n } from '../lib/i18n'
import { md } from '../lib/md'
import { useLive } from './live'
import { fmtTime } from './parts'

type Doc = { id: string; title: string; kind: string; version: number; created_at: string; goal_id: string; goal_title: string; chars: number }
type Full = { id: string; title: string; kind: string; content: string; version: number; created_at: string; sha256: string }

export default function Documents() {
  const { id } = useParams()
  const { t, lang } = useI18n()
  const { tick } = useLive()
  const nav = useNavigate()
  const [docs, setDocs] = useState<Doc[] | null>(null)
  const [doc, setDoc] = useState<Full | null>(null)
  const [msg, setMsg] = useState('')

  useEffect(() => {
    api.get<Doc[]>('/api/documents').then(setDocs).catch(() => setDocs([]))
  }, [tick])
  useEffect(() => {
    setDoc(null)
    if (id) api.get<Full>(`/api/documents/${id}`).then(setDoc).catch(() => nav('/app/documents'))
  }, [id, nav])

  const upload = async (f: File) => {
    setMsg(t.app.uploading)
    try {
      const r = await api.upload<{ id: string }>('/api/documents', f)
      setMsg('')
      nav(`/app/documents/${r.id}`)
    } catch (e: any) {
      setMsg(e.message)
    }
  }

  const download = () => {
    if (!doc) return
    const blob = new Blob([doc.content], { type: 'text/markdown;charset=utf-8' })
    const a = document.createElement('a')
    a.href = URL.createObjectURL(blob)
    a.download = doc.title.replace(/[\\/:*?"<>|]/g, '_') + (doc.title.includes('.') ? '' : '.md')
    a.click()
    URL.revokeObjectURL(a.href)
  }

  if (id && doc)
    return (
      <div className="page doc-view">
        <Link to="/app/documents" className="back"><IconArrowL size={16} /> {t.docs.title}</Link>
        <header className="page-head">
          <div>
            <small className="muted">{doc.kind} · v{doc.version} · {fmtTime(doc.created_at, lang)}</small>
            <h1>{doc.title}</h1>
          </div>
          <button className="btn btn-soft btn-sm" onClick={download}>{t.docs.download}</button>
        </header>
        <article className="paper prose" dangerouslySetInnerHTML={{ __html: md(doc.content) }} />
        <p className="muted mono">sha256 {doc.sha256.slice(0, 16)}…</p>
      </div>
    )

  return (
    <div className="page">
      <header className="page-head">
        <h1>{t.docs.title}</h1>
        <label className="btn btn-ink btn-sm">
          <IconUpload size={16} /> {t.docs.upload}
          <input type="file" hidden accept=".pdf,.docx,.txt,.md,.csv,.eml,image/*" onChange={(e) => { const f = e.target.files?.[0]; if (f) upload(f); e.target.value = '' }} />
        </label>
      </header>
      {msg && <div className="alert alert-info">{msg}</div>}
      {docs === null ? <p className="muted">{t.common.loading}</p> : docs.length === 0 ? (
        <div className="empty-card"><img src="/vera/vera-face-smile.webp" alt="" width={72} height={72} /><p>{t.docs.empty}</p></div>
      ) : (
        <div className="doc-list">
          {docs.map((d) => (
            <Link key={d.id} to={`/app/documents/${d.id}`} className="file-row">
              <IconDoc size={18} />
              <span>{d.title}<small>{d.goal_title}</small></span>
              <small>{d.kind} · v{d.version} · {fmtTime(d.created_at, lang)}</small>
            </Link>
          ))}
        </div>
      )}
    </div>
  )
}
