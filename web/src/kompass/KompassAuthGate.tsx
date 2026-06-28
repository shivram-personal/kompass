import { ReactNode } from 'react'
import { AccountChip } from './AccountChip'
import { KompassAuthProvider, useKompassAuth } from './AuthContext'
import { ChangePassword } from './ChangePassword'
import { ChatDock } from './ChatDock'
import { Login } from './Login'

function Gate({ children }: { children: ReactNode }) {
  const { user, loading } = useKompassAuth()

  if (loading) {
    return (
      <div className="min-h-screen flex items-center justify-center bg-theme-bg">
        <img src="/images/kompass/kompass-icon.svg" alt="" aria-hidden className="w-10 h-10 animate-pulse" />
      </div>
    )
  }
  if (!user) return <Login />
  if (user.must_change_password) return <ChangePassword />

  return (
    <>
      {children}
      <ChatDock />
      <AccountChip />
    </>
  )
}

// Wraps the whole application: nothing renders the engine UI until a Kompass
// session exists and any forced password change is complete.
export function KompassAuthGate({ children }: { children: ReactNode }) {
  return (
    <KompassAuthProvider>
      <Gate>{children}</Gate>
    </KompassAuthProvider>
  )
}
