// API クライアント。画面からの HTTP はすべてここを通す。
// レスポンスの型は bench/model.go の JSON タグと1対1に対応させてある。

export type User = { id: number; name: string }

export type AuctionSummary = {
  id: number
  title: string
  category_id: number
  seller: User
  current_price: number
  bid_count: number
  starts_at: string
  ends_at: string
  status: 'upcoming' | 'live' | 'closed'
}

// GET /api/auctions のレスポンス(4-B)。裸の配列ではない。
// per_page は返らない。サーバ定数の20件固定で、次ページの有無は has_next が持つ。
export type AuctionList = {
  auctions: AuctionSummary[]
  total_count: number
  has_next: boolean
}

// AUCTIONS_PER_PAGE はサーバ側の定数(webapp/go/auctions.go の auctionsPerPage)を
// 画面が「何件目〜何件目を表示中か」を出すためだけに写したもの。
// リクエストには送らない(API は per_page を受け付けない)。
export const AUCTIONS_PER_PAGE = 20

export type Bid = { id: number; user: User; amount: number; created_at: string }

export type AuctionDetail = AuctionSummary & {
  description: string
  starting_price: number
  winner_id: number | null
  winning_price: number | null
  bids: Bid[]
}

export type Notification = {
  id: number
  type: string
  auction_id: number
  message: string
  is_read: boolean
  created_at: string
}

export type Stats = {
  listed_count: number
  sold_count: number
  total_sales: number
  live_count: number
}

export type AuctionCreated = {
  id: number
  title: string
  starting_price: number
  ends_at: string
  status: string
}

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message)
  }
}

async function call<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch('/api' + path, {
    ...init,
    credentials: 'same-origin',
    headers: init?.body ? { 'Content-Type': 'application/json' } : undefined,
  })
  const text = await res.text()
  if (!res.ok) {
    let msg = text
    try {
      msg = (JSON.parse(text) as { error?: string }).error ?? text
    } catch {
      // エラー応答がJSONでないこともある(nginxの502など)
    }
    throw new ApiError(res.status, msg)
  }
  return text ? (JSON.parse(text) as T) : (undefined as T)
}

// カテゴリは初期データで固定されている(webapp/sql/90_seed_phase1.sql)。
// 一覧の絞り込みと出品フォームで使う。専用エンドポイント(GET /api/categories)を
// 設けないのは意図的である: この3件は /initialize が必ず同じ内容へ復元する固定値であり、
// API 面を1本増やせばベンチにも検証を1本足す必要が出る(設計 §7.2)。
export const CATEGORIES: { id: number; name: string }[] = [
  { id: 1, name: 'オフィスチェア' },
  { id: 2, name: 'ゲーミングチェア' },
  { id: 3, name: 'アンティーク' },
]

// iconURL は出品者アイコンの URL(4-C)。
// API ルートなので静的ハンドラには届かず、ベンチのアセットマニフェストにも載らない
// (マニフェストは webapp/public を歩いて作る。設計 §7.3)。
export function iconURL(userID: number): string {
  return `/api/users/${userID}/icon`
}

export type AuctionListQuery = { page?: number; q?: string; category?: number }

export const api = {
  me: () => call<User>('/me'),
  login: (name: string, password: string) =>
    call<User>('/login', { method: 'POST', body: JSON.stringify({ name, password }) }),
  register: (name: string, password: string) =>
    call<User>('/register', { method: 'POST', body: JSON.stringify({ name, password }) }),
  // 応答は {auctions, total_count, has_next}(4-B)。裸の配列ではない。
  // page を送らないとサーバー既定の1ページ目になる。
  auctions: ({ page, q, category }: AuctionListQuery = {}) => {
    const p = new URLSearchParams()
    if (page && page > 1) p.set('page', String(page))
    if (q) p.set('q', q)
    if (category) p.set('category', String(category))
    const qs = p.toString()
    return call<AuctionList>('/auctions' + (qs ? '?' + qs : ''))
  },
  auction: (id: number) => call<AuctionDetail>(`/auctions/${id}`),
  // フィードの応答は {"bids":[...]} で包まれている(bench/client.go の GetBidFeed と同じ)。
  bidsSince: (id: number, since: number) =>
    call<{ bids: Bid[] }>(`/auctions/${id}/bids?since=${since}`).then((r) => r.bids),
  bid: (id: number, amount: number) =>
    call<{ id: number }>(`/auctions/${id}/bids`, {
      method: 'POST',
      body: JSON.stringify({ amount }),
    }),
  sell: (input: {
    title: string
    description: string
    category_id: number
    starting_price: number
    duration_seconds: number
  }) => call<AuctionCreated>('/auctions', { method: 'POST', body: JSON.stringify(input) }),
  // 通知の応答も {"notifications":[...]} で包まれている
  // (webapp/go/notifications.go / bench/client.go の GetNotifications と同じ)。
  notifications: () =>
    call<{ notifications: Notification[] }>('/notifications').then((r) => r.notifications),
  stats: () => call<Stats>('/stats/me'),
}
