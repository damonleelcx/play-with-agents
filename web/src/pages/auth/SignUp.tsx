import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { api, type User } from '../../lib/api'
import { useI18n } from '../../lib/i18n'
import { useSession } from '../../lib/session'
import AuthLayout, { PasswordInput, useSubmit } from './AuthLayout'

const zhErrors: Record<string, string> = {
  'an account with this email already exists': '该邮箱已注册账户',
  'that email address does not look valid': '邮箱地址格式不正确',
  'password must be at least 10 characters': '密码至少需要 10 个字符',
  'password must not be your email address': '密码不能与邮箱相同',
}

export default function SignUp() {
  const { t, lang } = useI18n()
  const { setUser } = useSession()
  const nav = useNavigate()
  const [name, setName] = useState('')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const { busy, error, submit } = useSubmit(async () => {
    const u = await api.post<User>('/api/auth/signup', { name, email, password, language: lang })
    setUser(u)
    nav('/verify-email', { replace: true })
  })
  return (
    <AuthLayout title={t.auth.signupTitle} sub={t.auth.signupSub} mood="laugh">
      <form className="auth-form" onSubmit={submit}>
        {error && <div className="alert alert-error" role="alert">{(lang === 'zh' && zhErrors[error]) || error}</div>}
        <label className="field">
          <span>{t.auth.name}</span>
          <input className="input" value={name} onChange={(e) => setName(e.target.value)} placeholder={t.auth.namePh} autoComplete="name" maxLength={80} autoFocus />
        </label>
        <label className="field">
          <span>{t.auth.email}</span>
          <input className="input" type="email" value={email} onChange={(e) => setEmail(e.target.value)} autoComplete="email" required />
        </label>
        <PasswordInput label={t.auth.password} value={password} onChange={setPassword} autoComplete="new-password" showMeter />
        <p className="auth-fine">
          {t.auth.agree.split(/(\{terms\}|\{privacy\})/).map((part, i) =>
            part === '{terms}' ? <Link key={i} to="/terms" target="_blank">{t.auth.termsLink}</Link>
            : part === '{privacy}' ? <Link key={i} to="/privacy" target="_blank">{t.auth.privacyLink}</Link>
            : part)}
        </p>
        <button className="btn btn-ink btn-lg auth-submit" disabled={busy || [...password].length < 10}>{busy ? t.common.loading : t.auth.signup}</button>
      </form>
      <p className="auth-switch">{t.auth.haveAccount} <Link to="/signin">{t.auth.signin}</Link></p>
    </AuthLayout>
  )
}
