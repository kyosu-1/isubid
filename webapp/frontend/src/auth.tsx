import { createContext, useContext, useEffect, useState, type ReactNode } from 'react'
import { api, ApiError, type User } from './api'

type AuthState = {
  user: User | null
  loading: boolean
  login: (name: string, password: string) => Promise<void>
  register: (name: string, password: string) => Promise<void>
}

const Ctx = createContext<AuthState | null>(null)

// セッションは httpOnly Cookie なので JS からは読めない。
// 起動時に GET /api/me を1回だけ叩いて身元を解決する。
export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User | null>(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    api
      .me()
      .then(setUser)
      .catch((e) => {
        if (!(e instanceof ApiError && e.status === 401)) {
          console.error(e)
        }
        setUser(null)
      })
      .finally(() => setLoading(false))
  }, [])

  const value: AuthState = {
    user,
    loading,
    login: async (name, password) => setUser(await api.login(name, password)),
    register: async (name, password) => setUser(await api.register(name, password)),
  }
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}

export function useAuth(): AuthState {
  const v = useContext(Ctx)
  if (!v) throw new Error('useAuth は AuthProvider の内側でしか使えない')
  return v
}
