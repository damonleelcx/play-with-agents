import { useEffect, useRef, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { playApi } from '../../lib/playApi'
import { Spinner } from './parts'
import { usePlayT } from './strings'

export default function JoinByCode() {
  const { code = '' } = useParams()
  const { s } = usePlayT()
  const nav = useNavigate()
  const [err, setErr] = useState('')
  const [manual, setManual] = useState(code.toUpperCase())
  const tried = useRef<string | null>(null)

  const join = (c: string) => {
    setErr('')
    playApi
      .join(c.trim().toUpperCase())
      .then((t) => nav(`/app/table/${t.id}`, { replace: true }))
      .catch((e) => setErr(e?.message || s.join.failed))
  }
  useEffect(() => {
    if (!code || tried.current === code) return
    tried.current = code
    join(code)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [code])

  return (
    <div className="pw-page">
      <div className="pw-center-note pw-join">
        {!err ? (
          <>
            <Spinner />
            <p>{s.join.joining}</p>
            <code className="pw-code">{code.toUpperCase()}</code>
          </>
        ) : (
          <>
            <h2>{s.join.title}</h2>
            <p className="pw-alert">
              {s.join.failed} {err !== s.join.failed && <small>({err})</small>}
            </p>
            <form
              className="pw-joinform"
              onSubmit={(e) => {
                e.preventDefault()
                if (manual.trim()) join(manual)
              }}
            >
              <input
                className="pw-input"
                value={manual}
                maxLength={8}
                placeholder={s.join.ph}
                onChange={(e) => setManual(e.target.value.toUpperCase().replace(/[^A-Z0-9]/g, ''))}
              />
              <button type="submit" className="pw-btn pw-btn-primary">
                {s.join.go}
              </button>
            </form>
            <Link to="/app/games" className="pw-link-btn">
              {s.join.back}
            </Link>
          </>
        )}
      </div>
    </div>
  )
}
