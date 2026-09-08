/**
 * 原生收信桥：Android 在后台线程 POST pocketd，不走 WebView IMAP。
 */
import { Capacitor, registerPlugin as capRegisterPlugin } from '@capacitor/core'

export interface NativeEmailFetchResult {
  used: boolean
  synced: number
  newCount: number
  classified: number
}

interface EmailFetchPlugin {
  configure(opts: { apiBase: string; token: string }): Promise<void>
  runNow(): Promise<{ synced?: number; newCount?: number; classified?: number }>
  schedule(opts: { intervalMs: number }): Promise<void>
}

let plugin: EmailFetchPlugin | null = null
let loading: Promise<void> | null = null

async function ensurePlugin(): Promise<EmailFetchPlugin | null> {
  if (!Capacitor.isNativePlatform()) return null
  if (plugin) return plugin
  if (!loading) {
    loading = Promise.resolve().then(() => {
      try {
        plugin = (capRegisterPlugin as <T>(name: string) => T)('EmailFetch')
      } catch {
        plugin = null
      }
    })
  }
  await loading
  return plugin
}

export async function configureNativeEmailFetch(apiBase: string, token: string): Promise<boolean> {
  const p = await ensurePlugin()
  if (!p || !apiBase || !token) return false
  try {
    await p.configure({ apiBase: apiBase.replace(/\/$/, ''), token })
    return true
  } catch {
    return false
  }
}

export async function scheduleNativeEmailFetch(intervalMs: number): Promise<boolean> {
  const p = await ensurePlugin()
  if (!p) return false
  try {
    await p.schedule({ intervalMs })
    return true
  } catch {
    return false
  }
}

export async function runNativeEmailFetch(): Promise<NativeEmailFetchResult> {
  const p = await ensurePlugin()
  if (!p) return { used: false, synced: 0, newCount: 0, classified: 0 }
  try {
    const r = await p.runNow()
    return {
      used: true,
      synced: r.synced ?? 0,
      newCount: r.newCount ?? 0,
      classified: r.classified ?? 0,
    }
  } catch {
    return { used: false, synced: 0, newCount: 0, classified: 0 }
  }
}
