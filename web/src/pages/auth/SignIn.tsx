import { useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { api, type User } from '../../lib/api'
import { useI18n } from '../../lib/i18n'
import { useSession } from '../../lib/session'
import AuthLayout, { PasswordInput, useSubmit } from './AuthLayout'

export default function SignIn() {
  const { t, lang } = useI18n()
  const { setUser } = useSession()
  const nav = useNavigate()
  const [q] = useSearchParams()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const { busy, error, submit } = useSubmit(async () => {
    const u = await api.post<User>('/api/auth/signin', { email, password })
    setUser(u)
    const next = q.get('next')
    // Only same-site paths: an open redirect here would be a phishing aid.
    const safe = next && next.startsWith('/') && !next.startsWith('//') ? next : '/app'
    nav(u.email_verified ? safe : '/verify-email', { replace: true })
  })
  return (
    <AuthLayout title={t.auth.signinTitle} sub={t.auth.signinSub}>
      <form className="auth-form" onSubmit={submit}>
        {error && <div className="alert alert-error" role="alert">{lang === 'zh' && error.includes('invalid') ? '邮箱或密码不正确' : error}</div>}
        <label className="field">
          <span>{t.auth.email}</span>
          <input className="input" type="email" value={email} onChange={(e) => setEmail(e.target.value)} autoComplete="email" required autoFocus />
        </label>
        <PasswordInput label={t.auth.password} value={password} onChange={setPassword} autoComplete="current-password" />
        <div className="auth-row"><Link to="/forgot-password">{t.auth.forgot}</Link></div>
        <button className="btn btn-ink btn-lg auth-submit" disabled={busy}>{busy ? t.common.loading : t.auth.signin}</button>
      </form>
      <p className="auth-switch">{t.auth.noAccount} <Link to="/signup">{t.auth.signup}</Link></p>
    </AuthLayout>
  )
}
