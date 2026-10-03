import { StrictMode, Suspense, lazy } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, Navigate, Route, Routes, useLocation, useParams } from 'react-router-dom'
import { I18nProvider } from './lib/i18n'
import { SessionProvider, rememberNext, useSession } from './lib/session'
import './styles/tokens.css'
import './styles/base.css'

// The landing page loads eagerly (it is the first paint for most visitors);
// the app and auth pages are split so the landing does not carry them.
import Landing from './pages/Landing'
import { landingMode } from './pages/landing/theme'
const SignIn = lazy(() => import('./pages/auth/SignIn'))
const SignUp = lazy(() => import('./pages/auth/SignUp'))
const Verify = lazy(() => import('./pages/auth/Verify'))
const Forgot = lazy(() => import('./pages/auth/Forgot'))
const Reset = lazy(() => import('./pages/auth/Reset'))
const AppShell = lazy(() => import('./app/AppShell'))
const Legal = lazy(() => import('./pages/legal/Legal'))

// Paint the cached theme before anything else, so a light-theme reload
// doesn't flash navy. The landing follows the OS when nothing is saved
// (pages/landing/theme.ts); the rest of the site stays dark by default.
try {
  const look = JSON.parse(localStorage.getItem('play.look') || '{}')
  const light =
    location.pathname === '/'
      ? landingMode(look.theme) === 'light'
      : look.theme === 'light' || (look.theme === 'system' && window.matchMedia?.('(prefers-color-scheme: light)').matches)
  document.documentElement.dataset.theme = light ? 'light' : 'dark'
  if (look.font_size) document.documentElement.dataset.fs = look.font_size
} catch {}

const Boot = () => <div className="boot"><span className="boot-dot" /></div>

function RequireAuth({ children }: { children: JSX.Element }) {
  const { user, loading } = useSession()
  const loc = useLocation()
  if (loading) return <Boot />
  const here = loc.pathname + loc.search
  if (!user) {
    rememberNext(here)
    return <Navigate to={`/signin?next=${encodeURIComponent(here)}`} replace />
  }
  if (!user.email_verified) {
    rememberNext(here)
    return <Navigate to="/verify-email" replace />
  }
  return children
}

// /join/ABC123: an invite link. The app route does the joining; RequireAuth
// sends a stranger through sign-in (or sign-up) first and back here after.
function JoinLink() {
  const { code = '' } = useParams()
  return <Navigate to={`/app/join/${encodeURIComponent(code.trim().toUpperCase())}`} replace />
}

function App() {
  return (
    <Suspense fallback={<Boot />}>
      <Routes>
        <Route path="/" element={<Landing />} />
        <Route path="/signin" element={<SignIn />} />
        <Route path="/signup" element={<SignUp />} />
        <Route path="/verify-email" element={<Verify />} />
        <Route path="/forgot-password" element={<Forgot />} />
        <Route path="/reset-password" element={<Reset />} />
        <Route path="/terms" element={<Legal kind="terms" />} />
        <Route path="/privacy" element={<Legal kind="privacy" />} />
        <Route path="/join/:code" element={<JoinLink />} />
        <Route path="/app/*" element={<RequireAuth><AppShell /></RequireAuth>} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </Suspense>
  )
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <I18nProvider>
      <SessionProvider>
        <BrowserRouter>
          <App />
        </BrowserRouter>
      </SessionProvider>
    </I18nProvider>
  </StrictMode>,
)
