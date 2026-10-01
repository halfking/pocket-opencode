/**
 * account-lww.ts — 邮箱账户配置的 LWW（last-write-wins）判定，**纯函数**。
 *
 * 需求 8：「这个信息有最后修改时间，在服务端与客户端中，以最后时间为准来
 * 更新旧的一方」。
 *
 * 为什么单独抽成无依赖模块：这段判定原先只存在于 account-sync.ts，而那个
 * 模块 import 了 emailApi / localDB / lobster-init / vue，直接在 node 测试
 * 里 import 会因整条依赖链（`../../api/email` 等）无法解析而失败。于是早期
 * 测试自己在文件里**复制了一份**判定逻辑——测试全绿，但测的不是产品代码，
 * 生产逻辑改动时不会报警（假测试）。抽出纯模块后，生产与测试共用同一份
 * 实现，任何一边改动都会被另一边看到。
 *
 * 本模块不得 import 任何东西（有类型引用除外），以保持可在纯 node 环境测试。
 */

export interface AccountStamp {
  id: string
  emailAddress: string
  updatedAt: number
}

export interface AccountSyncPlan {
  /** 需要从服务端下拉到本地的账户 id（服务端更新，或本地没有）。 */
  pullIds: string[]
  /** 需要从本地上行到服务端的账户 id（本地更新，且对得上同一账户）。 */
  pushIds: string[]
}

/**
 * planAccountSync 计算双向同步计划。
 *
 *   - 服务端 updatedAt > 本地 → 下行（pull）
 *   - 本地 updatedAt > 服务端 且 id/邮箱对得上 → 上行（push）
 *   - 时间相同 → 都不做（避免抖动）
 *   - 本地独有账户不上行：镜像库不持有凭证，服务端无法据此建可用账户
 */
export function planAccountSync(local: AccountStamp[], remote: AccountStamp[]): AccountSyncPlan {
  const localById = new Map(local.map((a) => [a.id, a]))
  const remoteById = new Map(remote.map((a) => [a.id, a]))
  const remoteByEmail = new Map(remote.map((a) => [a.emailAddress.toLowerCase(), a]))
  const pullIds: string[] = []
  const pushIds: string[] = []
  for (const r of remote) {
    const l = localById.get(r.id)
    if (!l || r.updatedAt > l.updatedAt) pullIds.push(r.id)
  }
  for (const l of local) {
    const r = remoteById.get(l.id) ?? remoteByEmail.get(l.emailAddress.toLowerCase())
    if (r && l.updatedAt > r.updatedAt) pushIds.push(l.id)
  }
  return { pullIds, pushIds }
}
