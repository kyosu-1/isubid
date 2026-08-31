import { useState, type FormEvent } from 'react'
import { useNavigate } from 'react-router-dom'
import { api, CATEGORIES } from '../api'
import { useAuth } from '../auth'

// 既定の公開時間(秒)。webapp/go/auctions.go の postAuction が受け付ける範囲は10〜300。
const DEFAULT_DURATION_SECONDS = 60

export function Sell() {
  const { user, loading: authLoading } = useAuth()
  const navigate = useNavigate()

  const [title, setTitle] = useState('')
  const [description, setDescription] = useState('')
  const [categoryID, setCategoryID] = useState('')
  const [startingPrice, setStartingPrice] = useState('')
  const [durationSeconds, setDurationSeconds] = useState(String(DEFAULT_DURATION_SECONDS))
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setError('')
    setBusy(true)
    try {
      const created = await api.sell({
        title,
        description,
        category_id: Number(categoryID),
        starting_price: Number(startingPrice),
        duration_seconds: Number(durationSeconds),
      })
      // 遷移先は自分で組み立てた数値IDのパス。外部由来の文字列は使わない。
      navigate(`/auctions/${created.id}`)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  // useAuth().loading の間は身元がまだ確定していない。ここを見ずに !user だけで
  // 判定すると、ログイン済みでもハードリロード直後は GET /api/me の往復が終わるまでの間
  // 一瞬「ログインしてください。」が出てしまう。
  if (authLoading) return <p className="muted">読み込み中…</p>
  if (!user) return <p className="muted">ログインしてください。</p>

  return (
    <div className="card" style={{ maxWidth: 480 }}>
      <form onSubmit={onSubmit}>
        <div className="field">
          <label htmlFor="title">タイトル</label>
          <input
            id="title"
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            minLength={1}
            maxLength={255}
            required
          />
        </div>
        <div className="field">
          <label htmlFor="description">説明</label>
          <textarea
            id="description"
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            rows={4}
          />
        </div>
        <div className="field">
          <label htmlFor="category">カテゴリ</label>
          <select
            id="category"
            value={categoryID}
            onChange={(e) => setCategoryID(e.target.value)}
            required
          >
            <option value="" disabled>
              選択してください
            </option>
            {CATEGORIES.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
              </option>
            ))}
          </select>
        </div>
        <div className="field">
          <label htmlFor="starting_price">開始価格(円)</label>
          <input
            id="starting_price"
            type="number"
            min={1}
            value={startingPrice}
            onChange={(e) => setStartingPrice(e.target.value)}
            required
          />
        </div>
        <div className="field">
          <label htmlFor="duration_seconds">公開時間(秒、10〜300)</label>
          <input
            id="duration_seconds"
            type="number"
            min={10}
            max={300}
            value={durationSeconds}
            onChange={(e) => setDurationSeconds(e.target.value)}
            required
          />
        </div>
        {error && <p className="error">{error}</p>}
        <button type="submit" disabled={busy}>
          出品する
        </button>
      </form>
    </div>
  )
}
