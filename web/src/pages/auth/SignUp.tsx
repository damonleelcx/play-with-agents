import { useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { api, type User } from '../../lib/api'
import { useI18n } from '../../lib/i18n'
import { rememberNext, safePath, useSession } from '../../lib/session'
import AuthLayout, { PasswordInput, useAuthError, useSubmit } from './AuthLayout'

export default function SignUp() {
  const { t, lang } = useI18n()
  const { setUser } = useSession()
  const nav = useNavigate()
  const [q] = useSearchParams()
  const nice = useAuthError()
  const [name, setName] = useState('')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const next = safePath(q.get('next'))
  const { busy, error, submit } = useSubmit(async () => {
    const u = await api.post<User>('/api/auth/signup', { name: name.trim(), email: email.trim(), password, language: lang })
    setUser(u)
    if (next) rememberNext(next)
    nav('/verify-email', { replace: true })
  })
  return (
    <AuthLayout page="signup" title={t.auth.signupTitle} sub={t.auth.signupSub} mood="smile">
      <form className="auth-form" onSubmit={submit}>
        {error && <div className="alert alert-error" role="alert">{nice(error)}</div>}
        <label className="field">
          <span>{t.auth.name}</span>
          <input className="input" value={name} onChange={(e) => setName(e.target.value)} placeholder={t.auth.namePh} autoComplete="name" maxLength={80} autoFocus />
        </label>
        <label className="field">
          <span>{t.auth.email}</span>
          <input className="input" type="email" value={email} onChange={(e) => setEmail(e.target.value)} autoComplete="email" required inputMode="email" />
        </label>
        <PasswordInput label={t.auth.password} value={password} onChange={setPassword} autoComplete="new-password" showMeter />
        <p className="auth-fine">
          {t.auth.agree.split(/(\{terms\}|\{privacy\})/).map((part, i) =>
            part === '{terms}' ? <Link key={i} to="/terms" target="_blank">{t.auth.termsLink}</Link>
            : part === '{privacy}' ? <Link key={i} to="/privacy" target="_blank">{t.auth.privacyLink}</Link>
            : part)}
        </p>
        <button className="btn btn-primary btn-lg auth-submit" disabled={busy || [...password].length < 10 || !email}>{busy ? <span className="spin" /> : t.auth.signup}</button>
      </form>
      <p className="auth-switch">{t.auth.haveAccount} <Link to={next ? `/signin?next=${encodeURIComponent(next)}` : '/signin'}>{t.auth.signin}</Link></p>
    </AuthLayout>
  )
}
