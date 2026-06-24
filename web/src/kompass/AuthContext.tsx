import { createContext, useCallback, useContext, useEffect, useState, ReactNode } from 'react'
import { fetchMe, login as apiLogin, logout as apiLogout, KompassUser } from './api'

interface AuthContextValue {
  user: KompassUser | null
  loading: boolean
  login: (username: string, password: string) => Promise<void>
  logout: () => Promise<void>
  refresh: () => Promise<void>
}

const AuthContext = createContext<AuthContextValue | null>(null)

export function useKompassAuth(): AuthContextValue {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useKompassAuth must be used within KompassAuthProvider')
  return ctx
}

export function KompassAuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<KompassUser | null>(null)
  const [loading, setLoading] = useState(true)

  const refresh = useCallback(async () => {
    const me = await fetchMe()
    setUser(me?.user ?? null)
    setLoading(false)
  }, [])

  useEffect(() => {
    void refresh()
  }, [refresh])

  const login = useCallback(async (username: string, password: string) => {
    const me = await apiLogin(username, password)
    setUser(me.user)
    setLoading(false)
  }, [])

  const logout = useCallback(async () => {
    await apiLogout()
    setUser(null)
  }, [])

  return (
    <AuthContext.Provider value={{ user, loading, login, logout, refresh }}>
      {children}
    </AuthContext.Provider>
  )
}
