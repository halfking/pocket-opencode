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

// BUG-G (2026-09-30)：Capacitor 的 registerPlugin() 返回**带 .then 的 thenable
// 代理**。把它从 async 函数 return、或当作 .then() 回调的返回值，promise 决议
// 都会去调它的 .then()，未实现的原生插件直接抛
//   Error: "EmailFetch.then()" is not implemented on android
// 这会让 email-fetch-run.ts 里写好的「原生不可用就静默降级为仅服务端拉取」
// 变成未捕获异常。一律用非 thenable 的盒子 { value } 装载。
type EmailFetchBox = { value: EmailFetchPlugin | null }

let plugin: EmailFetchBox | null = null
let loading: Promise<void> | null = null

async function ensurePlugin(): Promise<EmailFetchBox | null> {
  if (!Capacitor.isNativePlatform()) return null
  if (plugin) return plugin
  if (!loading) {
    // 回调不返回值：赋值结果若被当作 promise 决议值，会再次触发 thenable 陷阱
    loading = Promise.resolve().then(() => {
      let p: EmailFetchPlugin | null = null
      try {
        p = (capRegisterPlugin as <T>(name: string) => T)('EmailFetch')
      } catch {
        p = null
      }
      plugin = { value: p }
    })
  }
  await loading
  return plugin
}

export async function configureNativeEmailFetch(apiBase: string, token: string): Promise<boolean> {
  const box = await ensurePlugin()
  const p = box?.value
  if (!p || !apiBase || !token) return false
  try {
    await p.configure({ apiBase: apiBase.replace(/\/$/, ''), token })
    return true
  } catch {
    return false
  }
}

export async function scheduleNativeEmailFetch(intervalMs: number): Promise<boolean> {
  const p = (await ensurePlugin())?.value
  if (!p) return false
  try {
    await p.schedule({ intervalMs })
    return true
  } catch {
    return false
  }
}

export async function runNativeEmailFetch(): Promise<NativeEmailFetchResult> {
  const p = (await ensurePlugin())?.value
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
