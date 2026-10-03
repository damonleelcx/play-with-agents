// Apply the app's cached look before the first paint, so a light-theme
// player never sees the dark loading screen flash (src/app/prefs.tsx
// writes play.look; the server copy wins once settings load).
try {
  if (location.pathname.indexOf('/app') === 0) {
    var look = JSON.parse(localStorage.getItem('play.look') || '{}')
    var light = look.theme === 'light' || (look.theme === 'system' && matchMedia('(prefers-color-scheme: light)').matches)
    document.documentElement.dataset.theme = light ? 'light' : 'dark'
    if (look.font_size) document.documentElement.dataset.fs = look.font_size
    document.body.classList.add('themed-body')
  }
} catch (e) {}
