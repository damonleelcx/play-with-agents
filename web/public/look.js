// Apply the cached look before the first paint, so a light-theme visitor
// never sees the dark page flash (src/app/prefs.tsx and the landing's theme
// switch write play.look; the server copy wins once settings load).
// The app is dark unless asked otherwise; the landing ("/") follows the OS
// when nothing is saved (src/pages/landing/theme.ts). Loaded as a file:
// the CSP forbids inline scripts.
try {
  var look = JSON.parse(localStorage.getItem('play.look') || '{}') || {}
  var osLight = function () { return !!(window.matchMedia && matchMedia('(prefers-color-scheme: light)').matches) }
  if (location.pathname.indexOf('/app') === 0) {
    var light = look.theme === 'light' || (look.theme === 'system' && osLight())
    document.documentElement.dataset.theme = light ? 'light' : 'dark'
    if (look.font_size) document.documentElement.dataset.fs = look.font_size
    document.body.classList.add('themed-body')
  } else if (location.pathname === '/') {
    var lp = look.theme === 'light' || (look.theme !== 'dark' && osLight())
    document.documentElement.dataset.theme = lp ? 'light' : 'dark'
    document.body.classList.add('lp-body') // landing.css paints the page canvas from this
    if (lp) {
      var m = document.querySelector('meta[name="theme-color"]')
      if (m) m.setAttribute('content', '#f4f7fc')
    }
  }
} catch (e) {}

// Set <html lang> before the first paint too, so CJK text gets the right font
// stack straight away (src/lib/i18n.tsx owns it once React starts: the saved
// choice, else the browser's languages).
try {
  var tags = { en: 'en', zh: 'zh-CN', ko: 'ko', ja: 'ja' }
  var saved = localStorage.getItem('play.lang')
  var pick = tags[saved] ? saved : null
  if (!pick) {
    var prefs = navigator.languages && navigator.languages.length ? navigator.languages : [navigator.language || '']
    for (var i = 0; i < prefs.length && !pick; i++) {
      var p = String(prefs[i] || '').toLowerCase().slice(0, 2)
      if (tags[p]) pick = p
    }
  }
  document.documentElement.lang = tags[pick || 'en']
} catch (e) {}
