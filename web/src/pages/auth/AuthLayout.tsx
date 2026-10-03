import { useState, type FormEvent, type ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { AoiFace, Img, type Mood } from '../../components/Aoi'
import { IconCheck, IconEye, IconEyeOff, LogoMark } from '../../components/Icons'
import LangSwitch from '../../components/LangSwitch'
import { useI18n } from '../../lib/i18n'
import '../../styles/auth.css'

type Page = 'signin' | 'signup' | 'verify' | 'forgot' | 'reset'

// The split sign-in layout: Aoi's art and one line from her on one side, an
// unhurried form on the other. On phones the art becomes a band above the form.
export default function AuthLayout({ title, sub, children, mood = 'smile', page }: { title: string; sub?: ReactNode; children: ReactNode; mood?: Mood; page: Page }) {
  const { t } = useI18n()
  const line = t.auth.lines[page]
  return (
    <div className="auth themed">
      <aside className="auth-art" aria-hidden="true">
        <div className="aa-glow" />
        <div className="aa-grid" />
        <div className="aa-suits"><span>♠</span><span>♥</span><span>♦</span><span>♣</span></div>
        <Img className="aa-img" srcs={['/play/aoi/aoi-full.webp', '/play/aoi/aoi-portrait.webp']}
          fallback={<div className="aa-fallback"><span className="aaf-ring" /><span className="aaf-kanji">葵</span></div>} />
        <div className="aa-quote">
          <AoiFace mood={mood} size={44} ring />
          <div>
            <p>{line}</p>
            <small>{t.auth.tagline}</small>
          </div>
        </div>
      </aside>

      <section className="auth-side">
        <header className="auth-top">
          <Link to="/" className="auth-brand"><LogoMark size={30} /><span>Play <i>with</i> Agents</span></Link>
          <LangSwitch />
        </header>
        <div className="auth-band" aria-hidden="true">
          <AoiFace mood={mood} size={52} ring />
          <p>{line}</p>
        </div>
        <div className="auth-card">
          <h1>{title}</h1>
          {sub && <p className="auth-sub">{sub}</p>}
          {children}
        </div>
        <p className="auth-foot">
          {t.legal.note}{' '}
          <Link to="/terms">{t.auth.termsLink}</Link> · <Link to="/privacy">{t.auth.privacyLink}</Link>
        </p>
      </section>
    </div>
  )
}

export function PasswordInput({ value, onChange, label, autoComplete, showMeter, autoFocus }: { value: string; onChange: (v: string) => void; label: string; autoComplete: string; showMeter?: boolean; autoFocus?: boolean }) {
  const { t } = useI18n()
  const [show, setShow] = useState(false)
  const score = strength(value)
  const tips = [[...value].length >= 10, /[a-z]/.test(value) && /[A-Z]/.test(value), /[\d\W_]/.test(value)]
  return (
    <label className="field">
      <span>{label}</span>
      <div className="pw">
        <input className="input" type={show ? 'text' : 'password'} value={value} onChange={(e) => onChange(e.target.value)} autoComplete={autoComplete}
          required minLength={showMeter ? 10 : 1} autoFocus={autoFocus} spellCheck={false} />
        <button type="button" className="pw-toggle" onClick={() => setShow(!show)} aria-label={show ? t.auth.hidePw : t.auth.showPw} title={show ? t.auth.hidePw : t.auth.showPw}>
          {show ? <IconEyeOff size={18} /> : <IconEye size={18} />}
        </button>
      </div>
      {showMeter && (
        <div className="meter" aria-live="polite">
          <div className="meter-bar">{[0, 1, 2, 3].map((i) => <i key={i} className={value && i <= score ? `s${score}` : ''} />)}</div>
          <div className="meter-row">
            <ul className="pw-tips">
              {t.auth.pwTips.map((x, i) => <li key={x} className={tips[i] ? 'ok' : ''}>{tips[i] ? <IconCheck size={11} /> : <i />}{x}</li>)}
            </ul>
            {value && <small className={`ms s${score}`}>{t.auth.strength[score]}</small>}
          </div>
        </div>
      )}
    </label>
  )
}

// A rough strength estimate: length dominates, variety helps.
function strength(pw: string): 0 | 1 | 2 | 3 {
  if ([...pw].length < 10) return 0
  let v = 0
  if (/[a-z]/.test(pw)) v++
  if (/[A-Z]/.test(pw)) v++
  if (/\d/.test(pw)) v++
  if (/[^A-Za-z0-9]/.test(pw)) v++
  if (/\s/.test(pw) || [...pw].length >= 16) v++
  return v >= 4 ? 3 : v >= 2 ? 2 : 1
}

export function useSubmit<T>(fn: () => Promise<T>) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const submit = async (e?: FormEvent) => {
    e?.preventDefault()
    setBusy(true)
    setError('')
    try {
      return await fn()
    } catch (err: any) {
      setError(err?.message || 'Error')
    } finally {
      setBusy(false)
    }
  }
  return { busy, error, submit, setError }
}

// Server errors come in English; show the friendly local version when known.
export function useAuthError() {
  const { t } = useI18n()
  return (e: string) => {
    if (!e) return ''
    if (e === 'offline') return t.auth.offline
    if (/invalid email or password|invalid credentials/i.test(e)) return t.auth.badCreds
    if (/expired|invalid token|token/i.test(e)) return t.auth.linkInvalid
    return t.auth.serverErrors[e] || e
  }
}
