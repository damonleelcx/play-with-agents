import { useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { api, type User } from '../../lib/api'
import { useI18n } from '../../lib/i18n'
import { rememberNext, safePath, takeNext, useSession } from '../../lib/session'
import AuthLayout, { PasswordInput, useAuthError, useSubmit } from './AuthLayout'

export default function SignIn() {
  const { t } = useI18n()
  const { setUser } = useSession()
  const nav = useNavigate()
  const [q] = useSearchParams()
  const nice = useAuthError()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const next = safePath(q.get('next'))
  const { busy, error, submit } = useSubmit(async () => {
    const u = await api.post<User>('/api/auth/signin', { email: email.trim(), password })
    setUser(u)
    if (!u.email_verified) {
      if (next) rememberNext(next)
      nav('/verify-email', { replace: true })
      return
    }
    nav(next || takeNext(), { replace: true })
  })
  return (
    <AuthLayout page="signin" title={t.auth.signinTitle} sub={t.auth.signinSub} mood="wink">
      <form className="auth-form" onSubmit={submit}>
        {error && <div className="alert alert-error" role="alert">{nice(error)}</div>}
        <label className="field">
          <span>{t.auth.email}</span>
          <input className="input" type="email" value={email} onChange={(e) => setEmail(e.target.value)} autoComplete="email" required autoFocus inputMode="email" />
        </label>
        <PasswordInput label={t.auth.password} value={password} onChange={setPassword} autoComplete="current-password" />
        <div className="auth-row"><Link to="/forgot-password">{t.auth.forgot}</Link></div>
        <button className="btn btn-primary btn-lg auth-submit" disabled={busy}>{busy ? <span className="spin" /> : t.auth.signin}</button>
      </form>
      <p className="auth-switch">{t.auth.noAccount} <Link to={next ? `/signup?next=${encodeURIComponent(next)}` : '/signup'}>{t.auth.signup}</Link></p>
    </AuthLayout>
  )
}
