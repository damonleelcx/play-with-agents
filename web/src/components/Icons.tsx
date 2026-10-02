// Stroke icons drawn at 24×24, inheriting currentColor.
type P = { size?: number; className?: string }
const S = ({ size = 20, className, children }: P & { children: React.ReactNode }) => (
  <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.7}
    strokeLinecap="round" strokeLinejoin="round" className={className} aria-hidden="true">{children}</svg>
)

export const IconScale = (p: P) => <S {...p}><path d="M12 3v18M7 21h10M5 7h14M5 7l-3 6a3 3 0 0 0 6 0L5 7zm14 0-3 6a3 3 0 0 0 6 0l-3-6z" /></S>
export const IconHeart = (p: P) => <S {...p}><path d="M20.8 5.6a5 5 0 0 0-7.1 0L12 7.3l-1.7-1.7a5 5 0 1 0-7.1 7.1L12 21.5l8.8-8.8a5 5 0 0 0 0-7.1z" /><path d="M3.5 12h4l2-3 3 6 2-3h6" /></S>
export const IconCross = (p: P) => <S {...p}><path d="M9 3h6v6h6v6h-6v6H9v-6H3V9h6z" /></S>
export const IconDoc = (p: P) => <S {...p}><path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z" /><path d="M14 3v5h5M9 13h6M9 17h4" /></S>
export const IconCalendar = (p: P) => <S {...p}><rect x="3" y="5" width="18" height="16" rx="2" /><path d="M16 3v4M8 3v4M3 10h18" /></S>
export const IconShield = (p: P) => <S {...p}><path d="M12 3 4 6v6c0 5 3.5 8 8 9 4.5-1 8-4 8-9V6z" /><path d="m9 12 2 2 4-4" /></S>
export const IconLock = (p: P) => <S {...p}><rect x="4" y="11" width="16" height="10" rx="2" /><path d="M8 11V8a4 4 0 0 1 8 0v3" /></S>
export const IconBell = (p: P) => <S {...p}><path d="M6 8a6 6 0 1 1 12 0c0 7 3 8 3 8H3s3-1 3-8" /><path d="M10 20a2 2 0 0 0 4 0" /></S>
export const IconSpark = (p: P) => <S {...p}><path d="M12 3v4M12 17v4M3 12h4M17 12h4M6 6l2.5 2.5M15.5 15.5 18 18M6 18l2.5-2.5M15.5 8.5 18 6" /></S>
export const IconUser = (p: P) => <S {...p}><circle cx="12" cy="8" r="4" /><path d="M4 21a8 8 0 0 1 16 0" /></S>
export const IconPill = (p: P) => <S {...p}><rect x="2.5" y="8.5" width="19" height="7" rx="3.5" transform="rotate(-45 12 12)" /><path d="m8.5 8.5 7 7" /></S>
export const IconFlask = (p: P) => <S {...p}><path d="M9 3h6M10 3v6L4.5 18.5A2 2 0 0 0 6.2 21.5h11.6a2 2 0 0 0 1.7-3L14 9V3" /><path d="M7 15h10" /></S>
export const IconCar = (p: P) => <S {...p}><path d="M5 17h14M5 17a2 2 0 1 0 4 0M15 17a2 2 0 1 0 4 0M3 17v-4l2-5h14l2 5v4" /><path d="M3 13h18" /></S>
export const IconBriefcase = (p: P) => <S {...p}><rect x="3" y="7" width="18" height="13" rx="2" /><path d="M9 7V5a2 2 0 0 1 2-2h2a2 2 0 0 1 2 2v2M3 13h18" /></S>
export const IconFolder = (p: P) => <S {...p}><path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z" /></S>
export const IconArrow = (p: P) => <S {...p}><path d="M5 12h14M13 6l6 6-6 6" /></S>
export const IconArrowL = (p: P) => <S {...p}><path d="M19 12H5M11 6l-6 6 6 6" /></S>
export const IconArrowUR = (p: P) => <S {...p}><path d="M7 17 17 7M8 7h9v9" /></S>
export const IconPlus = (p: P) => <S {...p}><path d="M12 5v14M5 12h14" /></S>
export const IconSend = (p: P) => <S {...p}><path d="M5 12h13M12 5l7 7-7 7" /></S>
export const IconStop = (p: P) => <S {...p}><rect x="6" y="6" width="12" height="12" rx="2" /></S>
export const IconClip = (p: P) => <S {...p}><path d="m21 11-8.6 8.6a5 5 0 0 1-7-7l8.5-8.6a3.4 3.4 0 0 1 4.8 4.8L10.2 17.4a1.7 1.7 0 0 1-2.4-2.4l7.9-7.9" /></S>
export const IconChat = (p: P) => <S {...p}><path d="M21 12a8 8 0 0 1-11.8 7L4 20l1.1-4.6A8 8 0 1 1 21 12z" /></S>
export const IconCheck = (p: P) => <S {...p}><path d="m5 12 5 5L20 7" /></S>
export const IconX = (p: P) => <S {...p}><path d="M6 6l12 12M18 6 6 18" /></S>
export const IconMenu = (p: P) => <S {...p}><path d="M4 7h16M4 12h16M4 17h16" /></S>
export const IconSettings = (p: P) => <S {...p}><circle cx="12" cy="12" r="3" /><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 0 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 0 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 0 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 0 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z" /></S>
export const IconPause = (p: P) => <S {...p}><path d="M9 5v14M15 5v14" /></S>
export const IconPlay = (p: P) => <S {...p}><path d="m7 4 13 8-13 8z" /></S>
export const IconClock = (p: P) => <S {...p}><circle cx="12" cy="12" r="9" /><path d="M12 7v5l3 2" /></S>
export const IconAlert = (p: P) => <S {...p}><path d="M12 3 2 20h20z" /><path d="M12 10v4M12 17.5v.5" /></S>
export const IconUpload = (p: P) => <S {...p}><path d="M12 16V4M6 10l6-6 6 6M4 20h16" /></S>
export const IconGlobe = (p: P) => <S {...p}><circle cx="12" cy="12" r="9" /><path d="M3 12h18M12 3c3 3.5 3 14.5 0 18M12 3c-3 3.5-3 14.5 0 18" /></S>
export const IconLogout = (p: P) => <S {...p}><path d="M15 12H4M8 8l-4 4 4 4M13 4h5a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2h-5" /></S>

export function LogoMark({ size = 28 }: { size?: number }) {
  // An "A" whose crossbar is a balance beam, set in an ember tile.
  return (
    <svg width={size} height={size} viewBox="0 0 32 32" aria-hidden="true">
      <rect width="32" height="32" rx="9" fill="#1c1a17" />
      <path d="M9 24 16 7l7 17" fill="none" stroke="#fbf6ee" strokeWidth="2.6" strokeLinecap="round" strokeLinejoin="round" />
      <path d="M10.5 18.5h11" stroke="#e8662a" strokeWidth="2.6" strokeLinecap="round" />
      <circle cx="16" cy="18.5" r="1.9" fill="#e8662a" />
    </svg>
  )
}
