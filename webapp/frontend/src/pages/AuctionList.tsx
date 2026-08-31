import { useEffect, useState, type FormEvent } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { api, AUCTIONS_PER_PAGE, CATEGORIES, iconURL, type AuctionList as List } from '../api'

function remaining(endsAt: string): string {
  const ms = new Date(endsAt).getTime() - Date.now()
  if (ms <= 0) return '終了'
  const s = Math.floor(ms / 1000)
  if (s < 60) return `残り${s}秒`
  const m = Math.floor(s / 60)
  if (m < 60) return `残り${m}分`
  return `残り${Math.floor(m / 60)}時間`
}

// parsePage は ?page= を1以上の整数に正規化する。
// 不正値でサーバーに400を撃たせないよう、画面側で1へ丸める。
function parsePage(raw: string | null): number {
  const n = Number(raw)
  return Number.isInteger(n) && n >= 1 ? n : 1
}

export function AuctionList() {
  // 検索条件もページ番号も URL のクエリに置く。コンポーネントの state に留めると、
  // ページを送っても URL が変わらないためリロードと「戻る」が壊れ、
  // 共有リンクも同じ結果を再現しない(設計 §7.2)。
  const [params, setParams] = useSearchParams()
  const q = params.get('q') ?? ''
  const category = params.get('category') ?? ''
  const page = parsePage(params.get('page'))

  const [list, setList] = useState<List | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    setLoading(true)
    setError('')
    api
      .auctions({ page, q: q || undefined, category: category ? Number(category) : undefined })
      .then(setList)
      .catch((e) => setError(e instanceof Error ? e.message : String(e)))
      .finally(() => setLoading(false))
  }, [page, q, category])

  // 検索条件を変えたらページは1へ戻す(3ページ目のまま絞り込むと空振りするため)。
  function submitSearch(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    const f = new FormData(e.currentTarget)
    const next = new URLSearchParams()
    const nq = String(f.get('q') ?? '')
    const nc = String(f.get('category') ?? '')
    if (nq) next.set('q', nq)
    if (nc) next.set('category', nc)
    setParams(next)
  }

  function goToPage(next: number) {
    const p = new URLSearchParams(params)
    if (next <= 1) p.delete('page')
    else p.set('page', String(next))
    setParams(p)
  }

  const items = list?.auctions ?? []
  const total = list?.total_count ?? 0
  const first = total === 0 ? 0 : (page - 1) * AUCTIONS_PER_PAGE + 1
  const last = (page - 1) * AUCTIONS_PER_PAGE + items.length

  return (
    <>
      <form className="row" style={{ marginBottom: '1rem' }} onSubmit={submitSearch}>
        <input name="q" defaultValue={q} placeholder="キーワードで検索" />
        <select name="category" defaultValue={category} style={{ width: 200 }}>
          <option value="">すべてのカテゴリ</option>
          {CATEGORIES.map((c) => (
            <option key={c.id} value={c.id}>
              {c.name}
            </option>
          ))}
        </select>
        <button type="submit">検索</button>
      </form>

      {error && <p className="error">{error}</p>}
      {loading && <p className="muted">読み込み中…</p>}
      {!loading && !error && (
        <p className="muted">
          {total === 0 ? '該当するオークションはありません。' : `全${total.toLocaleString()}件中 ${first}〜${last}件`}
        </p>
      )}

      {items.map((a) => (
        <div className="card" key={a.id}>
          <div className="row">
            <Link to={`/auctions/${a.id}`} style={{ fontWeight: 600 }}>
              {a.title}
            </Link>
            <span className="price">{a.current_price.toLocaleString()}円</span>
            <span className="muted">{a.bid_count}件の入札</span>
            <span className="muted" style={{ marginLeft: 'auto' }}>
              {remaining(a.ends_at)}
            </span>
          </div>
          <div className="row muted" style={{ gap: '0.4rem' }}>
            {/* アイコンは API ルート(/api/users/:id/icon)。未設定なら404が返るので
                alt を出さない空文字にして、壊れた画像アイコンだけを表示させる。 */}
            <img className="icon" src={iconURL(a.seller.id)} alt="" width={24} height={24} />
            <span>出品者: {a.seller.name}</span>
          </div>
        </div>
      ))}

      {!loading && total > 0 && (
        <div className="row" style={{ justifyContent: 'center', marginTop: '1rem' }}>
          <button type="button" onClick={() => goToPage(page - 1)} disabled={page <= 1}>
            前へ
          </button>
          <span className="muted">{page}ページ目</span>
          <button type="button" onClick={() => goToPage(page + 1)} disabled={!list?.has_next}>
            次へ
          </button>
        </div>
      )}
    </>
  )
}
