import { useCallback, useEffect, useRef, useState } from 'react'
import { uiLocale } from '../../lib/i18n'
import { playApi, type GameComment } from '../../lib/playApi'
import { Avatar } from './parts'
import { usePlayT } from './strings'

const MAX = 1000

// The comment thread under a published game: newest first, a composer on
// top, older comments on demand. The author and the game's maker may remove
// a comment; a removed one keeps its place, not its text.
export default function Comments({ gameId, published }: { gameId: string; published: boolean }) {
  const { s, f } = usePlayT()
  const c = s.comments
  const [items, setItems] = useState<GameComment[] | null>(null)
  const [total, setTotal] = useState(0)
  const [more, setMore] = useState(false)
  const [text, setText] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const box = useRef<HTMLTextAreaElement>(null)

  const load = useCallback(async () => {
    try {
      const pg = await playApi.comments(gameId)
      setItems(pg.comments)
      setTotal(pg.total)
      setMore(pg.more)
    } catch {
      setItems([])
    }
  }, [gameId])
  useEffect(() => {
    if (published) load()
  }, [load, published])

  const older = async () => {
    const last = items?.[items.length - 1]
    if (!last) return
    const pg = await playApi.comments(gameId, last.id).catch(() => null)
    if (!pg) return
    setItems((xs) => [...(xs || []), ...pg.comments])
    setMore(pg.more)
  }

  const post = async () => {
    const body = text.trim()
    if (!body || busy) return
    setBusy(true)
    setErr('')
    try {
      const cm = await playApi.addComment(gameId, body)
      setItems((xs) => [cm, ...(xs || [])])
      setTotal((n) => n + 1)
      setText('')
    } catch (e: any) {
      setErr(e?.status === 429 || /slow down/i.test(e?.message || '') ? c.tooFast : f(c.failed, { e: e?.message || '' }))
    } finally {
      setBusy(false)
    }
  }

  const remove = async (cm: GameComment) => {
    if (!window.confirm(c.confirmRemove)) return
    try {
      await playApi.deleteComment(gameId, cm.id)
      setItems((xs) => (xs || []).map((x) => (x.id === cm.id ? { ...x, deleted: true, body: '', name: '', can_delete: false } : x)))
      setTotal((n) => Math.max(0, n - 1))
    } catch (e: any) {
      setErr(f(c.failed, { e: e?.message || '' }))
    }
  }

  return (
    <section className="pw-card-panel pw-comments" aria-labelledby="pw-comments-h">
      <h3 className="pw-h3" id="pw-comments-h">
        {c.title}
        {published && total > 0 && <small>{total === 1 ? c.one : f(c.count, { n: total })}</small>}
      </h3>
      {!published ? (
        <p className="pw-muted">{c.draftNote}</p>
      ) : (
        <>
          <div className="pw-comment-compose">
            <textarea
              ref={box}
              value={text}
              maxLength={MAX}
              rows={2}
              placeholder={c.placeholder}
              aria-label={c.placeholder}
              onChange={(e) => setText(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) post()
              }}
            />
            <div className="pw-comment-bar">
              <small className={text.length > MAX - 80 ? 'is-near' : ''}>{text.length > 0 ? `${text.length}/${MAX}` : ''}</small>
              <button type="button" className="pw-btn pw-btn-primary pw-btn-sm" disabled={!text.trim() || busy} onClick={post}>
                {busy ? c.posting : c.post}
              </button>
            </div>
            {err && <p className="pw-comment-err" role="alert">{err}</p>}
          </div>
          {items === null ? null : items.length === 0 ? (
            <p className="pw-muted pw-comment-none">{c.none}</p>
          ) : (
            <ul className="pw-comment-list">
              {items.map((cm) => (
                <li key={cm.id} className={`pw-comment ${cm.deleted ? 'is-deleted' : ''} ${cm.mine ? 'is-mine' : ''}`}>
                  {cm.deleted ? (
                    <p className="pw-muted">{c.removed}</p>
                  ) : (
                    <>
                      <Avatar name={cm.name} seat={0} size={32} />
                      <div className="pw-comment-body">
                        <div className="pw-comment-meta">
                          <b>{cm.name}</b>
                          {cm.by_owner && <em className="pw-comment-tag">{c.maker}</em>}
                          {cm.mine && !cm.by_owner && <em className="pw-comment-tag is-you">{c.you}</em>}
                          <time dateTime={cm.created_at}>{new Date(cm.created_at).toLocaleString(uiLocale(), { dateStyle: 'medium', timeStyle: 'short' })}</time>
                          {cm.can_delete && (
                            <button type="button" className="pw-link-btn pw-comment-del" onClick={() => remove(cm)}>
                              {c.remove}
                            </button>
                          )}
                        </div>
                        <p className="pw-comment-text">{cm.body}</p>
                      </div>
                    </>
                  )}
                </li>
              ))}
            </ul>
          )}
          {more && (
            <button type="button" className="pw-btn pw-btn-soft pw-btn-sm pw-comment-more" onClick={older}>
              {c.more}
            </button>
          )}
        </>
      )}
    </section>
  )
}
