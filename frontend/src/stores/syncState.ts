/**
 * syncState.ts — Pinia store for note synchronization state management.
 *
 * Manages sync status between local notes and memora cloud storage.
 * Tracks pending, synced, conflict, and error states for each note.
 *
 * Reference: docs/2026-11-16-p0-completion-report.md
 */
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api } from '@/api/client'

export type SyncStatus = 'pending' | 'synced' | 'syncing' | 'conflict' | 'error' | 'offline'

export interface SyncState {
  noteId: string
  status: SyncStatus
  memoraId?: string
  lastSynced?: number
  errorMessage?: string
  contentHash?: string
}

export interface SyncStatusResponse {
  pendingCount: number
  syncInterval: string
  memoraConfigured: boolean
}

export const useSyncStateStore = defineStore('syncState', () => {
  // State: Map of noteId -> SyncState
  const syncStates = ref<Map<string, SyncState>>(new Map())
  const globalStatus = ref<SyncStatusResponse | null>(null)
  const loading = ref(false)

  /**
   * Get sync state for a specific note
   */
  const getSyncState = (noteId: string): SyncState => {
    return syncStates.value.get(noteId) || {
      noteId,
      status: 'offline'
    }
  }

  /**
   * Fetch sync status from backend
   */
  const fetchSyncStatus = async (noteId?: string) => {
    try {
      loading.value = true
      const response = await api.get<SyncStatusResponse>('/api/sync/status')
      globalStatus.value = response.data

      // If specific noteId provided, fetch its state
      // (Backend doesn't have per-note status endpoint yet, 
      // so we infer from global state)
      if (noteId && globalStatus.value) {
        // Check if note is in pending list
        const isPending = globalStatus.value.pendingCount > 0
        
        const currentState = syncStates.value.get(noteId)
        if (!currentState || currentState.status === 'offline') {
          syncStates.value.set(noteId, {
            noteId,
            status: isPending ? 'pending' : 'synced',
            lastSynced: Date.now()
          })
        }
      }
    } catch (err: any) {
      console.error('Failed to fetch sync status:', err)
      // Set offline status on network error
      if (noteId) {
        syncStates.value.set(noteId, {
          noteId,
          status: 'offline',
          errorMessage: err.message
        })
      }
    } finally {
      loading.value = false
    }
  }

  /**
   * Mark note as pending sync (after local edit)
   */
  const markPending = (noteId: string) => {
    syncStates.value.set(noteId, {
      noteId,
      status: 'pending'
    })
  }

  /**
   * Mark note as syncing (during sync operation)
   */
  const markSyncing = (noteId: string) => {
    const current = syncStates.value.get(noteId)
    syncStates.value.set(noteId, {
      ...current,
      noteId,
      status: 'syncing'
    })
  }

  /**
   * Mark note as synced
   */
  const markSynced = (noteId: string, memoraId?: string) => {
    syncStates.value.set(noteId, {
      noteId,
      status: 'synced',
      memoraId,
      lastSynced: Date.now()
    })
  }

  /**
   * Mark note as having a conflict
   */
  const markConflict = (noteId: string) => {
    const current = syncStates.value.get(noteId)
    syncStates.value.set(noteId, {
      ...current,
      noteId,
      status: 'conflict'
    })
  }

  /**
   * Mark note as sync error
   */
  const markError = (noteId: string, errorMessage: string) => {
    const current = syncStates.value.get(noteId)
    syncStates.value.set(noteId, {
      ...current,
      noteId,
      status: 'error',
      errorMessage
    })
  }

  /**
   * Resolve conflict for a note
   */
  const resolveConflict = async (noteId: string, resolution: 'local' | 'remote' | 'merge') => {
    try {
      markSyncing(noteId)
      
      await api.post(`/api/sync/resolve/${noteId}`, {
        resolution
      })
      
      markSynced(noteId)
      
      return true
    } catch (err: any) {
      markError(noteId, err.message)
      throw err
    }
  }

  /**
   * Trigger manual sync pull from memora
   */
  const pullSync = async () => {
    try {
      await api.post('/api/sync/pull')
      // Refresh global status
      await fetchSyncStatus()
      return true
    } catch (err: any) {
      console.error('Failed to pull sync:', err)
      throw err
    }
  }

  /**
   * Clear sync state for a note (e.g., when note is deleted)
   */
  const clearState = (noteId: string) => {
    syncStates.value.delete(noteId)
  }

  /**
   * Clear all sync states
   */
  const clearAllStates = () => {
    syncStates.value.clear()
    globalStatus.value = null
  }

  return {
    // State
    syncStates,
    globalStatus,
    loading,

    // Getters
    getSyncState,

    // Actions
    fetchSyncStatus,
    markPending,
    markSyncing,
    markSynced,
    markConflict,
    markError,
    resolveConflict,
    pullSync,
    clearState,
    clearAllStates
  }
})
