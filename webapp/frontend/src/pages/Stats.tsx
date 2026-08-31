import { useEffect, useState } from 'react'
import { api, type Stats as StatsData } from '../api'
import { useAuth } from '../auth'

export function Stats() {
  const { user } = useAuth()
  const [stats, setStats] = useState<StatsData | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    if (!user) return
    api
      .stats()
      .then(setStats)
      .catch((e) => setError(e instanceof Error ? e.message : String(e)))
      .finally(() => setLoading(false))
  }, [user])

  if (!user) return <p className="muted">ログインしてください。</p>
  if (loading) return <p className="muted">読み込み中…</p>
  if (error) return <p className="error">{error}</p>
  if (!stats) return null

  return (
    <div className="row" style={{ flexWrap: 'wrap', gap: '0.75rem' }}>
      <div className="card" style={{ flex: '1 1 160px' }}>
        <p className="muted" style={{ margin: 0 }}>
          出品数
        </p>
        <p className="price" style={{ margin: 0, fontSize: '1.4rem' }}>
          {stats.listed_count.toLocaleString()}
        </p>
      </div>
      <div className="card" style={{ flex: '1 1 160px' }}>
        <p className="muted" style={{ margin: 0 }}>
          開催中
        </p>
        <p className="price" style={{ margin: 0, fontSize: '1.4rem' }}>
          {stats.live_count.toLocaleString()}
        </p>
      </div>
      <div className="card" style={{ flex: '1 1 160px' }}>
        <p className="muted" style={{ margin: 0 }}>
          落札成立
        </p>
        <p className="price" style={{ margin: 0, fontSize: '1.4rem' }}>
          {stats.sold_count.toLocaleString()}
        </p>
      </div>
      <div className="card" style={{ flex: '1 1 160px' }}>
        <p className="muted" style={{ margin: 0 }}>
          売上合計
        </p>
        <p className="price" style={{ margin: 0, fontSize: '1.4rem' }}>
          {stats.total_sales.toLocaleString()}円
        </p>
      </div>
    </div>
  )
}
