import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react'
import { useParams } from 'react-router-dom'
import { api, type AuctionDetail as Detail, type Bid } from '../api'
import { useAuth } from '../auth'

const FEED_INTERVAL_MS = 1000

export function AuctionDetail() {
  const { id } = useParams()
  const auctionID = Number(id)
  const { user } = useAuth()
  const [detail, setDetail] = useState<Detail | null>(null)
  const [bids, setBids] = useState<Bid[]>([])
  const [amount, setAmount] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  // ポーリングのカーソル。詳細は created_at DESC, id DESC なので先頭が最大 id。
  const since = useRef(0)

  const load = useCallback(async () => {
    const d = await api.auction(auctionID)
    setDetail(d)
    setBids(d.bids)
    since.current = d.bids.length > 0 ? d.bids[0].id : 0
  }, [auctionID])

  useEffect(() => {
    load().catch((e) => setError(e instanceof Error ? e.message : String(e)))
  }, [load])

  useEffect(() => {
    if (!detail || detail.status !== 'live') return
    const t = setInterval(() => {
      api
        .bidsSince(auctionID, since.current)
        .then((fresh) => {
          if (fresh.length === 0) return
          // フィードは id 昇順で返る。表示は新しい順なので反転して先頭に積む。
          since.current = fresh[fresh.length - 1].id
          setBids((prev) => [...fresh.slice().reverse(), ...prev])
        })
        .catch(() => {
          /* ポーリングの失敗は画面を壊さない */
        })
    }, FEED_INTERVAL_MS)
    return () => clearInterval(t)
  }, [auctionID, detail])

  async function onBid(e: FormEvent) {
    e.preventDefault()
    setError('')
    setBusy(true)
    try {
      await api.bid(auctionID, Number(amount))
      setAmount('')
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  if (error && !detail) return <p className="error">{error}</p>
  if (!detail) return <p className="muted">読み込み中…</p>

  const current = bids.length > 0 ? bids[0].amount : detail.starting_price

  return (
    <>
      <div className="card">
        <h1 style={{ margin: '0 0 0.5rem' }}>{detail.title}</h1>
        <div className="row">
          <span className="price">{current.toLocaleString()}円</span>
          <span className="muted">{detail.status}</span>
          <span className="muted">出品者: {detail.seller.name}</span>
        </div>
        <p>{detail.description}</p>
        {detail.status === 'closed' && (
          <p className="muted">
            {detail.winner_id
              ? `落札: ${detail.winning_price?.toLocaleString()}円`
              : '入札がないまま終了しました'}
          </p>
        )}
      </div>

      {detail.status === 'live' && (
        <div className="card">
          {user ? (
            <form className="row" onSubmit={onBid}>
              <input
                type="number"
                min={current + 1}
                value={amount}
                onChange={(e) => setAmount(e.target.value)}
                placeholder={`${(current + 1).toLocaleString()}円以上`}
                required
              />
              <button type="submit" disabled={busy}>
                入札する
              </button>
            </form>
          ) : (
            <p className="muted">入札するにはログインしてください。</p>
          )}
          {error && <p className="error">{error}</p>}
        </div>
      )}

      <div className="card">
        <table>
          <thead>
            <tr>
              <th>入札者</th>
              <th>金額</th>
              <th>時刻</th>
            </tr>
          </thead>
          <tbody>
            {bids.map((b) => (
              <tr key={b.id}>
                <td>{b.user.name}</td>
                <td className="price">{b.amount.toLocaleString()}円</td>
                <td className="muted">{new Date(b.created_at).toLocaleTimeString()}</td>
              </tr>
            ))}
          </tbody>
        </table>
        {bids.length === 0 && <p className="muted">まだ入札はありません。</p>}
      </div>
    </>
  )
}
