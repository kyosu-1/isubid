import { useState, type FormEvent } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAuth } from '../auth'

export function Login() {
  const { user, login, register } = useAuth()
  const [mode, setMode] = useState<'login' | 'register'>('login')
  const [name, setName] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const navigate = useNavigate()

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setError('')
    setBusy(true)
    try {
      if (mode === 'login') await login(name, password)
      else await register(name, password)
      navigate('/')
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  if (user) return <p>{user.name} としてログイン中です。</p>

  return (
    <div className="card" style={{ maxWidth: 420 }}>
      <div className="tabs" role="tablist">
        <button role="tab" aria-selected={mode === 'login'} onClick={() => setMode('login')}>
          ログイン
        </button>
        <button role="tab" aria-selected={mode === 'register'} onClick={() => setMode('register')}>
          新規登録
        </button>
      </div>
      <form onSubmit={onSubmit}>
        <div className="field">
          <label htmlFor="name">ユーザー名</label>
          <input id="name" value={name} onChange={(e) => setName(e.target.value)} required />
        </div>
        <div className="field">
          <label htmlFor="password">パスワード</label>
          <input
            id="password"
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
          />
        </div>
        {error && <p className="error">{error}</p>}
        <button type="submit" disabled={busy}>
          {mode === 'login' ? 'ログイン' : '登録'}
        </button>
      </form>
    </div>
  )
}
