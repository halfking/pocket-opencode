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
 * 毫秒/秒的分界。与服务端 server_since.go 的 parseSinceQuery / stampAfterSince
 * 用同一个阈值（1e12），避免两侧对「这是秒还是毫秒」的判断不一致。
 */
const MS_THRESHOLD = 1_000_000_000_000

/**
 * normalizeAccountStamp 把账户配置时间戳统一成 **Unix 秒**。
 *
 * ## 为什么需要它
 *
 * 契约上 `local_email_accounts.updated_at` 是秒（下行写的是服务端的秒值，
 * `updateAccount` 写的也是 `Math.floor(Date.now()/1000)`）。但 `saveAccount`
 * 写的是 `Date.now()`，即**毫秒**。读回路径原先是 `updated_at ?? 0`，
 * 不做任何归一，于是同一列里混着两种单位。
 *
 * ## 后果（实测，见 handoff §7dg）
 *
 * `planAccountSync` 拿这个值与服务端的秒值比大小，再把它当作 base 版本上行；
 * 服务端守卫是 `updated_at <= base`。毫秒值比秒值大约 1000 倍，于是
 * `base` 恒大于服务端现存值，**无论服务端那份是不是更新的，写都会被接受** ——
 * 需求 8 要防的「旧的一方覆盖新的一方」被静默架空。
 *
 * ## 放在读侧而不是只放写侧
 *
 * 只改 `saveAccount` 救不了**已经写进库的历史行**。读侧归一是唯一能同时
 * 覆盖存量与增量的位置，所以 `rowToAccount` 必须调它。
 *
 * ## 不要推广到 last_synced_at
 *
 * `updateSyncState` 往 `last_synced_at` 写的 `Date.now()` 是**对的** ——
 * 那一列本来就是毫秒语义，且不参与 LWW 比较。把本函数当通病全库套用
 * 会把那一列改坏。
 */
export function normalizeAccountStamp(v: number | null | undefined): number {
  if (v === null || v === undefined) return 0
  if (typeof v !== 'number' || !Number.isFinite(v) || v <= 0) return 0
  return v > MS_THRESHOLD ? Math.floor(v / 1000) : v
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
  const localByEmail = new Map(local.map((a) => [a.emailAddress.toLowerCase(), a]))
  const remoteById = new Map(remote.map((a) => [a.id, a]))
  const remoteByEmail = new Map(remote.map((a) => [a.emailAddress.toLowerCase(), a]))
  const pullIds: string[] = []
  const pushIds: string[] = []
  for (const r of remote) {
    // 配对必须与上行**同一套规则**：先 id，再邮箱。
    //
    // 2026-10-01 修复。此前下行只按 id 找本地（localById.get(r.id)），配不上就
    // 直接 pull——哪怕本地有同一邮箱、且**本地更新**，也会被判成「本地没有这
    // 个账户」而下行覆盖，把用户较新的本地改动冲掉，同时上行又按邮箱配上了
    // 同一条，同一账户既 pull 又 push，结果取决于执行顺序：这不是 LWW，
    // 是「谁后执行谁赢」。
    //
    // 场景是真实的：服务端重建账户 id 后，客户端仍留着旧 id。
    const l = localById.get(r.id) ?? localByEmail.get(r.emailAddress.toLowerCase())
    if (!l || r.updatedAt > l.updatedAt) pullIds.push(r.id)
  }
  const pulledRemotes = new Set(pullIds)
  for (const l of local) {
    const r = remoteById.get(l.id) ?? remoteByEmail.get(l.emailAddress.toLowerCase())
    if (r && pulledRemotes.has(r.id)) continue
    if (r && l.updatedAt > r.updatedAt) pushIds.push(l.id)
  }
  return { pullIds, pushIds }
}
