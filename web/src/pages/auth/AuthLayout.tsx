import { useState, type FormEvent, type ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { LogoMark } from '../../components/Icons'
import LangSwitch from '../../components/LangSwitch'
import { useI18n } from '../../lib/i18n'
import '../../styles/auth.css'

export default function AuthLayout({ title, sub, children, mood = 'smile' }: { title: string; sub?: ReactNode; children: ReactNode; mood?: 'smile' | 'think' | 'laugh' }) {
  const { t } = useI18n()
  return (
    <div className="auth">
      <section className="auth-form-side">
        <header className="auth-top">
          <Link to="/" className="auth-logo"><LogoMark /><span>ACT</span></Link>
          <LangSwitch />
        </header>
        <div className="auth-card">
          <img className="auth-face" src={`/vera/vera-face-${mood}.webp`} alt="" width={56} height={56} />
          <h1>{title}</h1>
          {sub && <p className="auth-sub">{sub}</p>}
          {children}
        </div>
        <p className="auth-foot">
          {t.footer.disclaimer}{' '}
          <Link to="/privacy">{t.footer.links[0]}</Link> · <Link to="/terms">{t.footer.links[1]}</Link>
        </p>
      </section>
      <aside className="auth-visual" aria-hidden="true">
        <div className="auth-arch" />
        <img src="/vera/vera-full.webp" alt="" className="auth-vera" />
        <div className="auth-quote">
          <strong>{t.auth.side[0]}</strong>
          <span>{t.auth.side[1]}</span>
        </div>
      </aside>
    </div>
  )
}

export function PasswordInput({ value, onChange, label, autoComplete, showMeter }: { value: string; onChange: (v: string) => void; label: string; autoComplete: string; showMeter?: boolean }) {
  const { t, lang } = useI18n()
  const [show, setShow] = useState(false)
  const score = strength(value)
  return (
    <label className="field">
      <span>{label}</span>
      <div className="pw">
        <input className="input" type={show ? 'text' : 'password'} value={value} onChange={(e) => onChange(e.target.value)} autoComplete={autoComplete} required minLength={showMeter ? 10 : 1} />
        <button type="button" className="pw-toggle" onClick={() => setShow(!show)} aria-label={show ? 'Hide password' : 'Show password'}>
          {show ? (lang === 'zh' ? '隐藏' : 'Hide') : (lang === 'zh' ? '显示' : 'Show')}
        </button>
      </div>
      {showMeter && (
        <div className="meter" aria-live="polite">
          <div className="meter-bar"><i style={{ width: `${(score + 1) * 25}%` }} className={`s${score}`} /></div>
          <small>{value ? t.auth.strength[score] : t.auth.pwHint}</small>
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
