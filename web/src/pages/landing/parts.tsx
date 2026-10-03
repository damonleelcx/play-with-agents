import { useEffect, type SVGProps } from 'react'

// Small inline marks and icons used only by the landing page.

export function Logo({ className }: { className?: string }) {
  // a card, a chip and a spark — three shapes, like the studio reference mark
  return (
    <svg className={className} viewBox="0 0 64 28" aria-hidden="true">
      <rect x="1" y="2" width="18" height="24" rx="4" fill="currentColor" />
      <circle cx="32" cy="14" r="12" fill="currentColor" />
      <circle cx="32" cy="14" r="6.5" fill="none" stroke="var(--logo-cut, #e6e6e4)" strokeWidth="2" strokeDasharray="3.4 3" />
      <path d="M52 2 L56 10.5 L63 14 L56 17.5 L52 26 L48 17.5 L41 14 L48 10.5 Z" fill="currentColor" />
    </svg>
  )
}

type IP = SVGProps<SVGSVGElement>
const base = (p: IP) => ({
  width: 20, height: 20, viewBox: '0 0 24 24', fill: 'none', stroke: 'currentColor',
  strokeWidth: 1.8, strokeLinecap: 'round' as const, strokeLinejoin: 'round' as const, 'aria-hidden': true, ...p,
})

export const IArrow = (p: IP) => <svg {...base(p)}><path d="M7 17 17 7M8 7h9v9" /></svg>
export const IMenu = (p: IP) => <svg {...base(p)}><path d="M5 9h14M5 15h9" /></svg>
export const IClose = (p: IP) => <svg {...base(p)}><path d="M6 6l12 12M18 6 6 18" /></svg>
export const ICheck = (p: IP) => <svg {...base(p)}><path d="m5 12.5 4.2 4L19 7" /></svg>
export const IChip = (p: IP) => (
  <svg {...base(p)}><circle cx="12" cy="12" r="8.5" /><circle cx="12" cy="12" r="4" /><path d="M12 3.5v3M12 17.5v3M3.5 12h3M17.5 12h3" /></svg>
)
export const IShuffle = (p: IP) => (
  <svg {...base(p)}><path d="M4 7h3.5c4 0 5 10 9 10H20M4 17h3.5c1.6 0 2.7-1.6 3.6-3.6M16.5 7H20m-2-2 2 2-2 2m0 6 2 2-2 2M13 9.6C13.9 8.1 15 7 16.5 7" /></svg>
)
export const IEyeOff = (p: IP) => (
  <svg {...base(p)}><path d="M3 3l18 18M10.6 5.1A9.8 9.8 0 0 1 12 5c5.5 0 9 7 9 7a16 16 0 0 1-2.6 3.4M6.6 6.6A16 16 0 0 0 3 12s3.5 7 9 7a9 9 0 0 0 4.4-1.1M9.9 9.9a3 3 0 0 0 4.2 4.2" /></svg>
)
export const IBulb = (p: IP) => (
  <svg {...base(p)}><path d="M9 18h6M10 21h4M12 3a6 6 0 0 0-3.5 10.9c.6.5 1 1.2 1 2.1h5c0-.9.4-1.6 1-2.1A6 6 0 0 0 12 3Z" /></svg>
)
export const IClock = (p: IP) => <svg {...base(p)}><circle cx="12" cy="13" r="8" /><path d="M12 9v4l2.5 2.5M9 2.5h6" /></svg>
export const ISpark = (p: IP) => (
  <svg {...base(p)}><path d="M12 3l1.9 5.1L19 10l-5.1 1.9L12 17l-1.9-5.1L5 10l5.1-1.9z" /><path d="M19 16l.8 2.2L22 19l-2.2.8L19 22l-.8-2.2L16 19l2.2-.8z" /></svg>
)
export const IChat = (p: IP) => <svg {...base(p)}><path d="M4 5h16v11H9l-5 4z" /><path d="M8 9.5h8M8 12.5h5" /></svg>
export const IDice = (p: IP) => (
  <svg {...base(p)}><rect x="4" y="4" width="16" height="16" rx="3.5" /><circle cx="9" cy="9" r="1" fill="currentColor" /><circle cx="15" cy="15" r="1" fill="currentColor" /><circle cx="15" cy="9" r="1" fill="currentColor" /><circle cx="9" cy="15" r="1" fill="currentColor" /></svg>
)
export const ISend = (p: IP) => <svg {...base(p)}><path d="M4 12 20 4l-4 16-4-7z" /><path d="m12 13 8-9" /></svg>

/** Adds `.in` to every `.rv` element as it scrolls into view. */
export function useReveal(deps: unknown[] = []) {
  useEffect(() => {
    const els = Array.from(document.querySelectorAll<HTMLElement>('.lp .rv:not(.in)'))
    const reduce = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches
    if (reduce || !('IntersectionObserver' in window)) {
      els.forEach((el) => el.classList.add('in'))
      return
    }
    const io = new IntersectionObserver(
      (entries) => {
        for (const e of entries) {
          if (e.isIntersecting) {
            e.target.classList.add('in')
            io.unobserve(e.target)
          }
        }
      },
      { rootMargin: '0px 0px -8% 0px', threshold: 0.08 },
    )
    els.forEach((el) => io.observe(el))
    return () => io.disconnect()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps)
}

export const ISun = (p: IP) => (
  <svg {...base(p)}><circle cx="12" cy="12" r="4" /><path d="M12 2.5v2M12 19.5v2M2.5 12h2M19.5 12h2M5.3 5.3l1.4 1.4M17.3 17.3l1.4 1.4M5.3 18.7l1.4-1.4M17.3 6.7l1.4-1.4" /></svg>
)
export const IMoon = (p: IP) => <svg {...base(p)}><path d="M20 14.5A8 8 0 0 1 9.5 4a8 8 0 1 0 10.5 10.5Z" /></svg>
