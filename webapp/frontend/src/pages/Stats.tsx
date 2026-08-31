import { useEffect, useState } from 'react'
import { api, type Stats as StatsData } from '../api'
import { useAuth } from '../auth'

export function Stats() {
  const { user, loading: authLoading } = useAuth()
  const [stats, setStats] = useState<StatsData | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    if (!user) return
    api
      .stats()
      .then(setStats)
      .catch((e) => setError((e instanceof Error ? e.message : String(e)) || '不明なエラーが発生しました'))
      .finally(() => setLoading(false))
  }, [user])

  // useAuth().loading の間は身元がまだ確定していない。ここを見ずに !user だけで
  // 判定すると、ログイン済みでもハードリロード直後は GET /api/me の往復が終わるまでの間
  // 一瞬「ログインしてください。」が出てしまう。
  if (authLoading) return <p className="muted">読み込み中…</p>
  if (!user) return <p className="muted">ログインしてください。</p>
  if (loading) return <p className="muted">読み込み中…</p>
  if (error) return <p className="error">{error}</p>
  // ここに来るのは取得成功時のみのはず(失敗時は catch 側で error が必ず非空になる)。
  // それでも stats が未設定のまま抜けてきた場合に備え、白画面ではなくメッセージを出す。
  if (!stats) return <p className="muted">データを取得できませんでした。</p>

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
