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
  //
  // ただし「bids[0].id が真の最大id」であること自体は、created_at が id の昇順と
  // 一致している(＝データ生成器が id 順に created_at を発行している)という
  // 未保証の前提に依存しており、サーバーの ORDER BY が保証しているわけではない。
  // この前提が崩れる(生成器が created_at を id 順と無関係に発行する)と、
  // bidsSince が既知の入札を返し続ける形で下記の重複除去を素通りし、指摘1と
  // 同種の重複が再発しうる。挙動は変更しない(コメントのみの追記)。
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
          //
          // このリクエストは入札フォームの POST(→load())と並行に飛ぶことがある。
          // load() が bids を丸ごと差し替えた後に、それより前に発行されていた
          // このポーリングが遅れて解決すると、load() が既に入れた入札をもう一度
          // 先頭に積んでしまい、入札履歴が重複し React の key も衝突する
          // (postBid は行ロックで直列化される意図的に遅い実装のため、負荷下では
          // 現実に起こりうる)。setBids の関数形引数で受け取る prev は解決時点の
          // 最新state なので、それに対して id で重複除去してから積む。
          setBids((prev) => {
            const knownIDs = new Set(prev.map((b) => b.id))
            const newOnes = fresh.filter((b) => !knownIDs.has(b.id))
            if (newOnes.length === 0) return prev
            return [...newOnes.slice().reverse(), ...prev]
          })
          // since は「サーバー側で既知の最大id」を表すカーソル。重複除去で
          // 積む入札が0件になった場合でも、フィードが返した最大idまでは
          // 前進させる(戻さない)ことで次回以降の再取得範囲を縮める。
          since.current = Math.max(since.current, fresh[fresh.length - 1].id)
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
