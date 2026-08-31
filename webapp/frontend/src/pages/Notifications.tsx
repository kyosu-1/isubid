import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, type Notification } from '../api'
import { useAuth } from '../auth'

export function Notifications() {
  const { user } = useAuth()
  const [items, setItems] = useState<Notification[]>([])
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    if (!user) return
    // マウント時に1回だけ取得する。通知の追従はベンチが単発呼び出しで検証しており、
    // ここでポーリングすると画面が余計な負荷を作るだけになる。
    api
      .notifications()
      .then(setItems)
      .catch((e) => setError(e instanceof Error ? e.message : String(e)))
      .finally(() => setLoading(false))
  }, [user])

  if (!user) return <p className="muted">ログインしてください。</p>
  if (loading) return <p className="muted">読み込み中…</p>
  if (error) return <p className="error">{error}</p>
  if (items.length === 0) return <p className="muted">通知はありません。</p>

  return (
    <>
      {items.map((n) => (
        <div className="card" key={n.id}>
          <div className="row">
            <span>{n.message}</span>
            <span className="muted">{n.type}</span>
          </div>
          <div className="row muted">
            <span>{new Date(n.created_at).toLocaleString()}</span>
            <Link to={`/auctions/${n.auction_id}`}>オークションを見る</Link>
          </div>
        </div>
      ))}
    </>
  )
}
