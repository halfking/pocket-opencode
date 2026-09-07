/**
 * 用户配置 LWW 规划器：比较 local/remote 的 updatedAt（Unix 秒）。
 * 较新的一侧获胜；相等则不动。
 */
export interface ConfigStamp {
  namespace: string
  id: string
  updatedAt: number
}

export interface SyncPlan {
  pullKeys: string[]
  pushKeys: string[]
}

export function configKey(namespace: string, id: string): string {
  return `${namespace}:${id}`
}

export function nowUnixSec(nowMs = Date.now()): number {
  return Math.floor(nowMs / 1000)
}

export function planConfigSync(
  local: ConfigStamp[],
  remote: ConfigStamp[],
  opts: { pushLocalOnly?: boolean } = {},
): SyncPlan {
  const pushLocalOnly = opts.pushLocalOnly !== false
  const localByKey = new Map(local.map((s) => [configKey(s.namespace, s.id), s]))
  const remoteByKey = new Map(remote.map((s) => [configKey(s.namespace, s.id), s]))
  const pullKeys: string[] = []
  const pushKeys: string[] = []

  for (const r of remote) {
    const key = configKey(r.namespace, r.id)
    const l = localByKey.get(key)
    if (!l || r.updatedAt > l.updatedAt) pullKeys.push(key)
  }
  for (const l of local) {
    const key = configKey(l.namespace, l.id)
    const r = remoteByKey.get(key)
    if (!r) {
      if (pushLocalOnly) pushKeys.push(key)
      continue
    }
    if (l.updatedAt > r.updatedAt) pushKeys.push(key)
  }
  return { pullKeys, pushKeys }
}
