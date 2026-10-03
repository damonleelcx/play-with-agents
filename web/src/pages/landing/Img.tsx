import { useEffect, useState, type ReactNode } from 'react'

// An <img> that walks a list of candidate sources and, when every one fails,
// renders `fallback` (or nothing). Assets are produced in parallel with the
// page, so any of them may be missing for a while.
export default function Img({
  srcs,
  alt,
  className,
  fallback = null,
  eager = false,
  onState,
}: {
  srcs: string[]
  alt: string
  className?: string
  fallback?: ReactNode
  eager?: boolean
  onState?: (ok: boolean) => void
}) {
  const key = srcs.join('|')
  const [i, setI] = useState(0)
  useEffect(() => setI(0), [key])
  if (i >= srcs.length) return <>{fallback}</>
  return (
    <img
      src={srcs[i]}
      alt={alt}
      className={className}
      loading={eager ? 'eager' : 'lazy'}
      decoding="async"
      draggable={false}
      onLoad={() => onState?.(true)}
      onError={() => {
        if (i + 1 >= srcs.length) onState?.(false)
        setI(i + 1)
      }}
    />
  )
}
