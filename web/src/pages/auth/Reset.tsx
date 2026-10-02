import { useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { api, type User } from '../../lib/api'
import { useI18n } from '../../lib/i18n'
import { useSession } from '../../lib/session'
import AuthLayout, { PasswordInput, useSubmit } from './AuthLayout'

export default function Reset() {
  const { t, lang } = useI18n()
  const { setUser } = useSession()
  const nav = useNavigate()
  const [q] = useSearchParams()
  const token = q.get('token') || ''
  const [pw, setPw] = useState('')
  const [pw2, setPw2] = useState('')
  const { busy, error, submit, setError } = useSubmit(async () => {
    if (pw !== pw2) {
      setError(t.auth.mismatch)
      return
    }
    const u = await api.post<User>('/api/auth/reset', { token, password: pw })
    setUser(u)
    nav('/app', { replace: true })
  })
  if (!token)
    return (
      <AuthLayout title={t.common.error} sub={lang === 'zh' ? '此链接无效或已过期。' : 'This link is invalid or has expired.'} mood="think">
        <Link to="/forgot-password" className="btn btn-ink btn-lg auth-submit">{t.auth.sendLink}</Link>
      </AuthLayout>
    )
  return (
    <AuthLayout title={t.auth.resetTitle} sub={t.auth.resetSub}>
      <form className="auth-form" onSubmit={submit}>
        {error && (
          <div className="alert alert-error" role="alert">
            {lang === 'zh' && error.includes('expired') ? '此链接无效或已过期。' : error}
            {error.includes('expired') && <> <Link to="/forgot-password">{t.auth.sendLink}</Link></>}
          </div>
        )}
        <PasswordInput label={t.auth.newPassword} value={pw} onChange={setPw} autoComplete="new-password" showMeter />
        <PasswordInput label={t.auth.confirm} value={pw2} onChange={setPw2} autoComplete="new-password" />
        <button className="btn btn-ink btn-lg auth-submit" disabled={busy || [...pw].length < 10}>{busy ? t.common.loading : t.auth.reset}</button>
      </form>
    </AuthLayout>
  )
}
