/**
 * emailSeed.ts — 往**浏览器本地** SQLCipher 库灌测试邮件。
 *
 * 为什么灌本地而不是后端（2026-10-06）：
 *   后端的 email store 只在 **PostgreSQL** pool 存在时注入
 *   （`cmd/pocketd/main.go`：`if pool != nil { emailStore = email.NewStore(pool) }`），
 *   本地 SQLite 起法下 `s.emailStore == nil` ⇒ `/api/emails` 固定 503
 *   "email store not configured"。为跑 e2e 而起一整套 PG 不划算。
 *
 *   而 `EmailInboxView` 的列表读的是**本地库**（`emailsStore.listEmails` →
 *   `localDB.query`），后端只负责同步。所以本地有行，列表就有行 ——
 *   这正是要验证的那条路径。
 *
 * 与 `tokenAuth.unlockLobster` 同一个手法：用 Vite dev 的动态 import 拿
 * **同一个模块实例**再调它真实导出，**不给生产代码加测试后门**。
 * 代价同前：只在 dev server 下可用。
 *
 * ⚠️ 前置条件：必须**先** `unlockLobster(page)`（localDB 未 init 时所有写入抛错）。
 */
import type { Page } from '@playwright/test'

export interface SeedEmailSpec {
  id: string
  /** 收件时间（ms）。`listEmails` 按 date DESC 排序。 */
  date: number
  subject: string
  snippet?: string
  fromName?: string
  fromAddress?: string
  isRead?: boolean
  isStarred?: boolean
  category?: string | null
  importance?: string | null
}

export const E2E_ACCOUNT_ID = 'e2e-account'

/** 造 `count` 封邮件，日期从 now 往前每封递减 1 小时。 */
export function makeEmails(count: number, base = Date.now()): SeedEmailSpec[] {
  return Array.from({ length: count }, (_, i) => ({
    id: `e2e-mail-${i}`,
    date: base - i * 3_600_000,
    subject: `E2E 测试邮件 #${i}`,
    snippet: `这是第 ${i} 封用于连续加载验证的测试邮件`,
    fromName: `发件人${i}`,
    fromAddress: `sender${i}@e2e.test`,
    isRead: false,
    isStarred: false,
    category: null,
    importance: null,
  }))
}

/**
 * 写账户 + 邮件。返回实际写入的邮件数（重复调用同一 id 不会翻倍）。
 *
 * 用 `writeAccountIfNewer` / `upsertEmail` 这两个**生产路径**的函数，
 * 而不是直接 SQL —— 这样 schema 变化会立刻暴露，而不是悄悄写坏列。
 */
export async function seedEmails(page: Page, emails: SeedEmailSpec[]): Promise<number> {
  return page.evaluate(
    async (payload: { accountId: string; rows: SeedEmailSpec[] }) => {
      // ⚠ 2026-10-30：8b34e1c3 起 `writeAccountIfNewer` 等生产写路径都会先过
      //   assertCloudModule('email')；默认策略是全 local ⇒ 播种直接被 409 拦下
      //   （e2e-web 连续 20+ 次全红的根因）。
      //   ⚠ 必须**复用 App 已装好的**激活宿主与 scope 解析器（sync-policy-runtime
      //   按 [user, workspace] 定 scope）：自己另装一个宿主会把真实宿主顶掉，
      //   且 scope 对不上 ⇒ contextCurrent 恒 false ⇒ 读了还是 'local'。
      //   unlockLobster 之后 activationStatus='ready'，宿主 captureContext 合法。
      // ⚠ 2026-10-30（续）：恢复出来的会话 userId 恒空（auth.ts 的
      //   「Persisted metadata is not a verified owner assertion」），而行归属
      //   （15cd8a09）只认 verified owner ⇒ 不补这一步，播下的行全部无归属，
      //   owner 域的 listEmails 一封都读不到（seeded=0 的第二个根因）。
      //   ⚠ 顺序必须**先激活身份再激活策略**：setAuthWithWorkspace 会作废
      //     策略的 generation ⇒ 后激活策略才能让 captureContext 拿到新代。
      const authMod = (await import(/* @vite-ignore */ '/src/stores/auth.ts')) as any
      const auth = authMod.useAuthStore()
      await auth.setAuthWithWorkspace(
        localStorage.getItem('pocket_token') ?? '', auth.user, 'e2e-user', 'e2e-ws', 'e2e-token')
      const policy = (await import(/* @vite-ignore */ '/src/stores/syncPolicyStore.ts')) as any
      const ok = await policy.useSyncPolicyStore().applyPolicy(
        { system_default: 'cloud', allow_override: true, modules: { email: 'cloud' } }, Date.now())
      if (!ok) throw new Error('e2e seed: cloud policy activation failed')
      const store = (await import(/* @vite-ignore */ '/src/features/email/emails-store.ts')) as {
        writeAccountIfNewer: (a: unknown) => Promise<boolean>
        upsertEmail: (e: unknown) => Promise<boolean>
        listEmails: (f?: unknown) => Promise<unknown[]>
      }
      await store.writeAccountIfNewer({
        id: payload.accountId,
        displayName: 'E2E 测试账户',
        emailAddress: 'e2e@example.test',
        imapHost: 'imap.example.test',
        imapPort: 993,
        authType: 'password',
        syncIntervalMin: 30,
        enabled: false,
        // ⚠ 生产契约是**秒**（writeAccountIfNewer 内部 *1000 转毫秒）；传毫秒会被
        //   new Date(μs).toISOString() 变成 5 位年份 ⇒ versionKey 的 \d{4} 不匹配
        //   ⇒ 'Invalid portal notes timestamp'（verified owner 打开了 portal 提交链路才暴露）。
        updatedAt: Math.floor(Date.now() / 1000),
      })
      let inserted = 0
      for (const r of payload.rows) {
        if (await store.upsertEmail({ ...r, accountId: payload.accountId })) inserted += 1
      }
      const total = await store.listEmails({ limit: 500 })
      return total.length
    },
    { accountId: E2E_ACCOUNT_ID, rows: emails },
  )
}
