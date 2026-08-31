import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, Link, Route, Routes } from 'react-router-dom'
import { AuthProvider, useAuth } from './auth'
import { AuctionDetail } from './pages/AuctionDetail'
import { AuctionList } from './pages/AuctionList'
import { Login } from './pages/Login'
import { Notifications } from './pages/Notifications'
import { Sell } from './pages/Sell'
import { Stats } from './pages/Stats'
import './styles.css'

function Header() {
  const { user } = useAuth()
  return (
    <header className="header">
      <Link to="/" className="brand">
        ISUBID
      </Link>
      <nav>
        <Link to="/">一覧</Link>
        <Link to="/sell">出品</Link>
        <Link to="/notifications">通知</Link>
        <Link to="/stats">売上</Link>
        {user ? <span className="me">{user.name}</span> : <Link to="/login">ログイン</Link>}
      </nav>
    </header>
  )
}

function App() {
  return (
    <BrowserRouter>
      <AuthProvider>
        <Header />
        <main className="main">
          <Routes>
            <Route path="/" element={<AuctionList />} />
            <Route path="/auctions/:id" element={<AuctionDetail />} />
            <Route path="/sell" element={<Sell />} />
            <Route path="/notifications" element={<Notifications />} />
            <Route path="/stats" element={<Stats />} />
            <Route path="/login" element={<Login />} />
            <Route path="*" element={<p className="muted">ページが見つかりません。</p>} />
          </Routes>
        </main>
      </AuthProvider>
    </BrowserRouter>
  )
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
