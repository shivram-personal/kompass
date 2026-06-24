import { useState } from 'react'
import { useKompassAuth } from './AuthContext'
import { AdminUsers } from './AdminUsers'
import { ClustersAdmin } from './ClustersAdmin'

// Self-contained Kompass account control. Floats so it doesn't require surgery
// on the engine's own top bar; opens user management for admins and handles
// sign-out for everyone.
export function AccountChip() {
  const { user, logout } = useKompassAuth()
  const [open, setOpen] = useState(false)
  const [showAdmin, setShowAdmin] = useState(false)
  const [showClusters, setShowClusters] = useState(false)
  if (!user) return null

  return (
    <>
      <div className="fixed bottom-4 right-4 z-50">
        {open && (
          <div className="mb-2 w-56 rounded-xl border border-theme-border bg-theme-surface shadow-lg p-3 text-sm">
            <div className="px-1 pb-2 border-b border-theme-border">
              <div className="font-medium text-theme-text-primary">{user.username}</div>
              <div className="text-xs text-theme-text-tertiary capitalize">{user.role}</div>
            </div>
            {user.role === 'admin' && (
              <>
                <button
                  onClick={() => {
                    setShowAdmin(true)
                    setOpen(false)
                  }}
                  className="w-full text-left mt-2 px-1 py-1.5 rounded hover:bg-theme-bg text-theme-text-secondary"
                >
                  Manage users
                </button>
                <button
                  onClick={() => {
                    setShowClusters(true)
                    setOpen(false)
                  }}
                  className="w-full text-left px-1 py-1.5 rounded hover:bg-theme-bg text-theme-text-secondary"
                >
                  Manage clusters
                </button>
              </>
            )}
            <button
              onClick={() => void logout()}
              className="w-full text-left px-1 py-1.5 rounded hover:bg-theme-bg text-red-500"
            >
              Sign out
            </button>
          </div>
        )}
        <button
          onClick={() => setOpen((v) => !v)}
          className="flex items-center gap-2 rounded-full border border-theme-border bg-theme-surface shadow px-3 py-1.5 text-sm text-theme-text-primary hover:border-emerald-500"
        >
          <span className="w-2 h-2 rounded-full bg-emerald-500" />
          {user.username}
        </button>
      </div>
      {showAdmin && <AdminUsers onClose={() => setShowAdmin(false)} />}
      {showClusters && <ClustersAdmin onClose={() => setShowClusters(false)} />}
    </>
  )
}
