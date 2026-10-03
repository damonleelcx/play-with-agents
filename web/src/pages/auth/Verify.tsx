import { useEffect, useRef, useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { api, type User } from '../../lib/api'
import { useI18n } from '../../lib/i18n'
import { takeNext, useSession } from '../../lib/session'
import AuthLayout, { useAuthError } from './AuthLayout'

// Two states: arriving from the emailed link (?token=…), or waiting for it.
// While waiting, the page polls /me, so clicking the link on a phone moves
// this tab on by itself.
export default function Verify() {
  const { t } = useI18n()
  const { user, setUser, loading, signOut, refresh } = useSession()
  const nav = useNavigate()
  const nice = useAuthError()
  const [q] = useSearchParams()
  const token = q.get('token')
  // 'confirmed': the address is verified, but this browser is not signed in
  // to that account. Verifying never signs anyone in: the link only proves
  // the mailbox, not who registered it.
  const [state, setState] = useState<'idle' | 'verifying' | 'done' | 'confirmed' | 'error'>(token ? 'verifying' : 'idle')
  const [error, setError] = useState('')
  const [cooldown, setCooldown] = useState(0)
  const [notice, setNotice] = useState<{ ok: boolean; text: string } | null>(null)
  const ran = useRef(false)

  useEffect(() => {
    if (!token || ran.current) return
    ran.current = true // StrictMode runs effects twice; a token is single-use
    api
      .post<{ verified: boolean; signed_in: boolean; user?: User }>('/api/auth/verify', { token })
      .then((r) => {
        if (r.signed_in && r.user) {
          setUser(r.user)
          setState('done')
        } else {
          setState('confirmed')
        }
      })
      .catch((e) => {
        setError(e.message)
        setState('error')
      })
  }, [token, setUser])

  useEffect(() => {
    if (!token && !loading && user?.email_verified) nav(takeNext(), { replace: true })
  }, [token, loading, user, nav])

  // Poll for a verification done elsewhere: every 5s while the tab is
  // visible, giving up after 30 minutes.
  const unverified = !token && !!user && !user.email_verified
  useEffect(() => {
    if (!unverified) return
    const started = Date.now()
    const id = window.setInterval(() => {
      if (Date.now() - started > 30 * 60 * 1000) return window.clearInterval(id)
      if (!document.hidden) refresh()
    }, 5000)
    return () => window.clearInterval(id)
  }, [unverified, refresh])

  useEffect(() => {
    if (cooldown <= 0) return
    const id = setTimeout(() => setCooldown(cooldown - 1), 1000)
    return () => clearTimeout(id)
  }, [cooldown])

  const resend = async () => {
    setNotice(null)
    try {
      await api.post('/api/auth/resend')
      setNotice({ ok: true, text: t.auth.resent })
      setCooldown(60)
    } catch (e: any) {
      setNotice({ ok: false, text: nice(e.message) })
    }
  }

  if (state === 'verifying') return <AuthLayout page="verify" title={t.auth.verifying} mood="neutral"><div className="auth-spinner" /></AuthLayout>
  if (state === 'done')
    return (
      <AuthLayout page="verify" title={t.auth.verified} sub={t.auth.verifiedSub} mood="smile">
        <button className="btn btn-ember btn-lg auth-submit" onClick={() => nav(takeNext(), { replace: true })}>{t.auth.continue}</button>
      </AuthLayout>
    )
  if (state === 'confirmed')
    return (
      <AuthLayout page="verify" title={t.auth.confirmed} sub={t.auth.confirmedSub} mood="smile">
        <Link to="/signin" className="btn btn-ember btn-lg auth-submit">{t.auth.signin}</Link>
      </AuthLayout>
    )
  if (state === 'error')
    return (
      <AuthLayout page="verify" title={t.common.error} sub={nice(error) || t.auth.linkInvalid} mood="sad">
        {user ? (
          <button className="btn btn-primary btn-lg auth-submit" onClick={resend} disabled={cooldown > 0}>{cooldown > 0 ? `${t.auth.resend} (${cooldown}s)` : t.auth.resend}</button>
        ) : (
          <Link to="/signin" className="btn btn-primary btn-lg auth-submit">{t.auth.backToSignin}</Link>
        )}
        {notice && <div className={`alert ${notice.ok ? 'alert-ok' : 'alert-error'}`} style={{ marginTop: 14 }}>{notice.text}</div>}
      </AuthLayout>
    )

  if (!loading && !user)
    return (
      <AuthLayout page="verify" title={t.auth.verifyTitle} sub={t.auth.verifyHelp} mood="neutral">
        <Link to="/signin" className="btn btn-primary btn-lg auth-submit">{t.auth.signin}</Link>
      </AuthLayout>
    )

  return (
    <AuthLayout page="verify" mood="wink" title={t.auth.verifyTitle} sub={<>{t.auth.verifySub} <strong>{user?.email}</strong>. {t.auth.verifyHelp}</>}>
      <div className="mail-art" aria-hidden="true"><span /></div>
      <p className="waiting"><span className="dots3"><i /><i /><i /></span> {t.auth.waiting}</p>
      {user && !user.mail_enabled && <div className="alert alert-info">{t.auth.mailOff}</div>}
      {notice && <div className={`alert ${notice.ok ? 'alert-ok' : 'alert-error'}`} role="status">{notice.text}</div>}
      <button className="btn btn-primary btn-lg auth-submit" onClick={resend} disabled={cooldown > 0}>
        {cooldown > 0 ? `${t.auth.resend} (${cooldown}s)` : t.auth.resend}
      </button>
      <p className="auth-switch">
        <button className="linklike" onClick={async () => { await signOut(); nav('/signin') }}>{t.auth.useOther}</button>
      </p>
    </AuthLayout>
  )
}
