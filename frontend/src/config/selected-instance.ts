/**
 * 当前 OpenCode 实例（pocketd /api/instances 的下游选择）。
 * 同时写 JSON + id，避免会话页只读 selected_instance_id 时丢选择。
 */

export const SELECTED_INSTANCE_KEY = 'selected_instance'
export const SELECTED_INSTANCE_ID_KEY = 'selected_instance_id'

export interface SelectedInstance {
  id: string
  displayName: string
  environment?: string
  capabilities?: string[]
  npsClientId?: number
}

function defaultStorage(): Storage | null {
  try {
    return typeof localStorage !== 'undefined' ? localStorage : null
  } catch {
    return null
  }
}

export function readSelectedInstance(storage?: Storage): SelectedInstance | null {
  const store = storage ?? defaultStorage()
  if (!store) return null
  const raw = store.getItem(SELECTED_INSTANCE_KEY)
  if (raw) {
    try {
      const parsed = JSON.parse(raw) as SelectedInstance
      if (parsed && typeof parsed.id === 'string' && parsed.id) {
        return {
          id: parsed.id,
          displayName: parsed.displayName || parsed.id,
          environment: parsed.environment,
          capabilities: parsed.capabilities,
          npsClientId: parsed.npsClientId,
        }
      }
    } catch {
      // 坏 JSON 落到 id-only
    }
  }
  const id = store.getItem(SELECTED_INSTANCE_ID_KEY)
  if (!id) return null
  return { id, displayName: id }
}

export function writeSelectedInstance(instance: SelectedInstance, storage?: Storage): void {
  if (!instance.id) throw new Error('instance-id')
  const store = storage ?? defaultStorage()
  if (!store) return
  const next: SelectedInstance = {
    id: instance.id,
    displayName: instance.displayName || instance.id,
    environment: instance.environment,
    capabilities: instance.capabilities,
    npsClientId: instance.npsClientId,
  }
  store.setItem(SELECTED_INSTANCE_KEY, JSON.stringify(next))
  store.setItem(SELECTED_INSTANCE_ID_KEY, next.id)
}

export function clearSelectedInstance(storage?: Storage): void {
  const store = storage ?? defaultStorage()
  if (!store) return
  store.removeItem(SELECTED_INSTANCE_KEY)
  store.removeItem(SELECTED_INSTANCE_ID_KEY)
}
