import { useState } from 'react'
import { createPortal } from 'react-dom'
import { Link } from 'react-router-dom'
import { IconAlert, IconBrain, IconCheck, IconChevronD, IconDoc, IconPlay, IconRefresh, IconWand, IconX } from '../components/Icons'
import { api, type Design, type DesignState } from '../lib/api'
import { useI18n } from '../lib/i18n'
import { md } from '../lib/md'

// The shared notes of a game-design session, beside the conversation: what
// the design is so far, how close it is to buildable, and the way from
// "this is it" to a build: write the plan, read it, build it.
export default function DesignPanel({ convId, state, onState, busy }: {
  convId?: string
  state: DesignState | null
  onState: (s: DesignState) => void
  busy: boolean
}) {
  const { t, f } = useI18n()
  const d = t.design
  const [open, setOpen] = useState(false) // narrow screens: the notes fold away
  const [planning, setPlanning] = useState(false)
  const [building, setBuilding] = useState(false)
  const [showPlan, setShowPlan] = useState(false)
  const [err, setErr] = useState('')
  const doc: Design = state?.design || {}
  const ready = Math.max(0, Math.min(100, doc.readiness || 0))
  const hasNotes = Object.keys(doc).some((k) => k !== 'readiness')

  const makePlan = async () => {
    if (!convId || planning) return
    setPlanning(true)
    setErr('')
    try {
      onState(await api.post<DesignState>(`/api/conversations/${convId}/design/plan`))
      setShowPlan(true)
    } catch (e: any) {
      setErr(f(d.planFailed, { e: e?.message || '' }))
    } finally {
      setPlanning(false)
    }
  }
  const build = async () => {
    if (!convId || building) return
    setBuilding(true)
    setErr('')
    try {
      onState(await api.post<DesignState>(`/api/conversations/${convId}/design/build`))
      setShowPlan(false)
    } catch (e: any) {
      setErr(f(d.buildFailed, { e: e?.message || '' }))
    } finally {
      setBuilding(false)
    }
  }

  const list = (k: keyof Design, items?: string[]) =>
    items && items.length > 0 ? (
      <div className="dn-field" key={k}>
        <dt>{d.f[k as keyof typeof d.f]}</dt>
        <dd>
          <ul>{items.map((x, i) => <li key={i}>{x}</li>)}</ul>
        </dd>
      </div>
    ) : null
  const one = (k: keyof Design, v?: string) =>
    v ? (
      <div className="dn-field" key={k}>
        <dt>{d.f[k as keyof typeof d.f]}</dt>
        <dd>{v}</dd>
      </div>
    ) : null

  return (
    <aside className={`design-notes ${open ? 'is-open' : ''}`} aria-label={d.notes}>
      <button type="button" className="dn-toggle" onClick={() => setOpen((x) => !x)} aria-expanded={open}>
        <IconBrain size={16} />
        <span className="dn-toggle-title">{doc.title || d.notes}</span>
        <span className="dn-meter" aria-hidden><i style={{ width: `${ready}%` }} /></span>
        <IconChevronD size={16} className="dn-chev" />
      </button>

      <div className="dn-body">
        <header className="dn-head">
          <span className="app-eyebrow"><IconWand size={13} /> {d.notes}</span>
          <h2>{doc.title || d.title}</h2>
          {doc.pitch && <p className="dn-pitch">{doc.pitch}</p>}
          <div className="dn-ready" title={`${ready}%`}>
            <small>{d.readiness}</small>
            <span className="dn-meter"><i style={{ width: `${ready}%` }} /></span>
            <b>{ready}%</b>
          </div>
        </header>

        {!hasNotes ? (
          <p className="dn-empty">{d.notesEmpty}</p>
        ) : (
          <dl className="dn-fields">
            {one('players', doc.players)}
            {one('length', doc.length)}
            {one('board', doc.board)}
            {list('components', doc.components)}
            {list('loop', doc.loop)}
            {list('mechanics', doc.mechanics)}
            {one('twist', doc.twist)}
            {one('win', doc.win)}
            {list('open_questions', doc.open_questions)}
            {list('parked', doc.parked)}
          </dl>
        )}

        <footer className="dn-actions">
          {err && <p className="dn-err" role="alert"><IconAlert size={14} /> {err}</p>}
          {state?.goal_id ? (
            <div className="dn-built">
              <IconCheck size={16} /> <span>{d.built}</span>
              <Link className="btn btn-soft btn-sm" to={`/app/studio/${state.goal_id}`}><IconPlay size={14} /> {d.openMission}</Link>
            </div>
          ) : null}
          {state?.plan && (
            <button type="button" className="btn btn-soft btn-sm" onClick={() => setShowPlan(true)}><IconDoc size={15} /> {d.plan}</button>
          )}
          {hasNotes && ready < 60 && !state?.plan && <p className="dn-hint">{d.notReady}</p>}
          <button type="button" className="btn btn-primary" disabled={!convId || !hasNotes || planning || busy} onClick={makePlan}>
            {planning ? <><span className="spin" /> {d.making}</> : state?.plan ? <><IconRefresh size={15} /> {d.updatePlan}</> : <><IconDoc size={16} /> {d.makePlan}</>}
          </button>
        </footer>
      </div>

      {showPlan && state?.plan && createPortal(
        // In a portal: the notes panel's backdrop blur would otherwise
        // trap a fixed overlay inside its own box.
        <div className="dn-plan-wrap" role="dialog" aria-modal="true" aria-label={d.plan} onClick={(e) => e.target === e.currentTarget && setShowPlan(false)}>
          <div className="dn-plan">
            <header>
              <span className="app-eyebrow"><IconDoc size={13} /> {d.plan}</span>
              <button type="button" className="icon-btn" onClick={() => setShowPlan(false)} aria-label={d.keep}><IconX size={18} /></button>
            </header>
            {state.stale && <p className="dn-stale"><IconAlert size={14} /> {d.stale}</p>}
            <div className="prose dn-plan-md" dangerouslySetInnerHTML={{ __html: md(state.plan) }} />
            <footer>
              {err && <p className="dn-err" role="alert"><IconAlert size={14} /> {err}</p>}
              <button type="button" className="btn btn-soft" onClick={() => setShowPlan(false)}>{d.keep}</button>
              {state.stale && (
                <button type="button" className="btn btn-soft" disabled={planning} onClick={makePlan}>
                  {planning ? d.making : d.updatePlan}
                </button>
              )}
              {!state.goal_id && (
                <button type="button" className="btn btn-ember" disabled={building || planning} onClick={build}>
                  {building ? <><span className="spin" /> {d.building}</> : <><IconWand size={16} /> {d.build}</>}
                </button>
              )}
            </footer>
          </div>
        </div>,
        document.body,
      )}
    </aside>
  )
}
