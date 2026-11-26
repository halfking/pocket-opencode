import { isHarmonyWebView } from './runtime-platform'

export class WebDatabaseLockError extends Error {
  constructor(reason: 'busy' | 'unavailable') {
    super(reason === 'busy'
      ? '本地数据正在另一个标签页使用。请关闭该标签页后重试。'
      : '当前浏览器环境无法协调本地数据库，请在支持 Web Locks 的安全页面中打开。')
    this.name = 'WebDatabaseLockError'
  }
}

/** SQL.js persists the entire snapshot, so every browser tab on this origin shares one lock. */
export async function acquireWebDatabaseLock(name: string): Promise<(() => Promise<void>) | undefined> {
  if (typeof window === 'undefined' || isHarmonyWebView()) return undefined
  const locks = window.navigator.locks
  if (!locks) throw new WebDatabaseLockError('unavailable')

  return new Promise<() => Promise<void>>((resolve, reject) => {
    let release!: () => void
    const held = new Promise<void>(done => { release = done })
    const request = locks.request(`openpocket:sqlite:${name}`, { mode: 'exclusive', ifAvailable: true }, lock => {
      if (!lock) throw new WebDatabaseLockError('busy')
      resolve(async () => {
        release()
        await request
      })
      return held
    })
    void request.catch(error => reject(error instanceof WebDatabaseLockError ? error : new WebDatabaseLockError('unavailable')))
  })
}
