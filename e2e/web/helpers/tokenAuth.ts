/**
 * tokenAuth.ts — 用**本地签发的真 JWT** 建立登录态。
 *
 * 2026-10-06 之前，本仓登录后页面的 e2e 一直是「取不到证」的：路由守卫只查
 * localStorage，注入一枚**假** token 的话，首屏确实能渲染，随后真后端回 401，
 * `forceReauth()` 清本地态并把用户踢回 `/#/login?reason=expired`；而把
 * `/api/**` 整个拦掉又只能渲染出空内容。表现是「时好时坏」，比失败更糟。
 *
 * 这条路径绕开了假 token：后端仓里有 `cmd/gen-jwt`，用**与 pocketd 同一个
 * `POCKET_JWT_SECRET`** 签一枚真 token，打真后端 `/api/auth/me` 返回 200，
 * 于是页面是在**真的**认证下渲染的。
 *
 * 为什么不走 `helpers/auth.ts` 的 UI 登录：那条路要真账号 + 首次主密码创建，
 * 在无凭据环境里跑不起来（`E2E_PASSWORD` 缺省为空）。签 token 只需 secret。
 *
 * 守卫契约（`frontend/src/app/routeGuards.ts` + `stores/auth.ts`）：
 *   - `isAuthenticated = Boolean(token) && Boolean(user)` ⇒ **两个 key 都要写**；
 *     只写 token 会被判未登录并 redirectLogin。
 *   - 用 `addInitScript` 写入，而不是 goto 之后再 evaluate：localStorage 必须
 *     在应用第一行脚本之前就位，否则 Pinia 的 state 初始化读到空串。
 *
 * 环境变量：
 *   E2E_JWT_TOKEN   —— 直接给一枚现成 token（优先于自动签发）
 *   E2E_JWT_SECRET  —— 用于调用 backend/cmd/gen-jwt 签发；缺省用 dev 脚本里的
 *                      那个（backend/start-dev.sh 的 POCKET_JWT_SECRET）
 *   E2E_USER / E2E_WORKSPACE —— 覆盖 claim
 */
import { execFileSync } from 'node:child_process'
import { existsSync } from 'node:fs'
import { join, resolve } from 'node:path'
import type { Page } from '@playwright/test'

/**
 * Playwright 把本 spec 转成 CJS，`import.meta` 不可用（实测报
 * "Cannot use 'import.meta' outside a module"）⇒ 用 __dirname。
 * 写 `?? ''` 兜住 bundler 场景下 __dirname 可能缺失。
 */
const HERE = __dirname ?? ''
/** e2e/web/helpers → 仓根 */
const REPO_ROOT = resolve(HERE, '..', '..', '..')

/** 与 backend/start-dev.sh 里的开发 secret 保持一致。 */
export const DEFAULT_E2E_JWT_SECRET = 'test-secret-key-for-phase7-validation'

export const E2E_USER = process.env.E2E_USER ?? 'e2e-user'
export const E2E_WORKSPACE = process.env.E2E_WORKSPACE ?? 'e2e-ws'

/** 走一次真后端，拿到一枚**已验证可用**的 token。 */
function mintViaBackend(secret: string): string {
  const backend = join(REPO_ROOT, 'backend')
  if (!existsSync(backend)) {
    throw new Error(`找不到 backend 目录：${backend}（E2E_JWT_TOKEN 可绕过签发）`)
  }
  const out = execFileSync(
    'go',
    [
      'run',
      './cmd/gen-jwt',
      '--user', E2E_USER,
      '--role', 'tenant_admin',
      '--workspace', E2E_WORKSPACE,
      '--ttl', '1h',
    ],
    {
      cwd: backend,
      env: { ...process.env, POCKET_JWT_SECRET: secret },
      encoding: 'utf8',
      stdio: ['ignore', 'pipe', 'pipe'],
    },
  )
  const tok = out.trim().split('\n').pop() ?? ''
  if (!tok || tok.split('.').length !== 3) {
    throw new Error(`gen-jwt 没有产出合法 JWT：${out.slice(0, 200)}`)
  }
  return tok
}

let cached: string | null = null

/**
 * 取 token：环境变量优先，否则调 backend/cmd/gen-jwt 现场签。
 * 结果进程内缓存——每个用例重新 `go run` 会把套件拖慢一个数量级。
 */
export function e2eToken(): string {
  if (cached) return cached
  const fromEnv = process.env.E2E_JWT_TOKEN?.trim()
  if (fromEnv) {
    cached = fromEnv
    return cached
  }
  cached = mintViaBackend(process.env.E2E_JWT_SECRET ?? DEFAULT_E2E_JWT_SECRET)
  return cached
}

export interface TokenAuthSeed {
  token: string
  user: string
  workspaceId: string
  authMethod: string
}

/**
 * 把登录态写进 localStorage。**必须在 goto 之前调用。**
 *
 * `pocket_user` 存的是**字符串**（store 的 state 初始值就是
 * `localStorage.getItem(USER_KEY) || ''`），不是 JSON——`isAuthenticated`
 * 只判 truthy，所以给一个用户名就够；但给 JSON 更贴近真实登录产物。
 */
export async function seedTokenAuth(page: Page, seed?: Partial<TokenAuthSeed>): Promise<void> {
  const data: TokenAuthSeed = {
    token: seed?.token ?? e2eToken(),
    user: seed?.user ?? JSON.stringify({ id: E2E_USER, name: E2E_USER }),
    workspaceId: seed?.workspaceId ?? E2E_WORKSPACE,
    authMethod: seed?.authMethod ?? 'e2e-token',
  }
  await page.addInitScript((s: TokenAuthSeed) => {
    localStorage.setItem('pocket_token', s.token)
    localStorage.setItem('pocket_user', s.user)
    localStorage.setItem('pocket_workspace_id', s.workspaceId)
    localStorage.setItem('pocket_auth_method', s.authMethod)
  }, data)
}

/** 真打一次后端，证明这枚 token 不是「长得像 JWT」而是真能认证。 */
export async function assertTokenWorks(apiBase = 'http://127.0.0.1:8088'): Promise<void> {
  const res = await fetch(`${apiBase}/api/auth/me`, {
    headers: { Authorization: `Bearer ${e2eToken()}` },
  })
  if (res.status !== 200) {
    throw new Error(
      `/api/auth/me 返回 ${res.status}：token 没被后端接受。` +
        `检查 VITE_API_PROXY 指向的后端与 POCKET_JWT_SECRET 是否与签发时一致。`,
    )
  }
}

/** 与 backend/cmd/gen-jwt 的 --ttl 同量级；e2e 跑得慢，别给太短。 */
export const E2E_MASTER_PASSWORD = process.env.E2E_MASTER_PASSWORD ?? 'e2e-master-pass-123'

/**
 * 走**真实** `initLobster()` 解锁龙虾硬壳。
 *
 * 为什么需要：`routeGuards.ts` Case C 对 `requiresLobster` 的路由
 * （email / notes / vault / meetings / pkm）要求 `isLobsterReady()`，
 * 否则 `redirectUnlock` → 落到 `/#/login?...&unlock=1`。而 `_ready`
 * 是 `lobster-init.ts` 的模块级 ref，只由 `initLobster()` 置真。
 *
 * ⚠️ 这里**不**给生产代码加测试后门，而是用 Vite dev 的动态 import
 * 拿到**同一个**模块实例再调它真实导出——走的是产品代码路径，不是假的。
 * 代价：**只在 dev server 下可用**（生产构建里没有 `/src/...` 路径）。
 * e2e 本来就跑 dev server，所以成立；若将来要测生产包需另设方案。
 *
 * @returns 是否真的 ready。false 表示解锁失败（页面会停在 unlock 流程）。
 */
export async function unlockLobster(page: Page, masterPassword = E2E_MASTER_PASSWORD): Promise<boolean> {
  return page.evaluate(async (pw: string) => {
    try {
      const mod = (await import(/* @vite-ignore */ '/src/native/lobster-init.ts')) as {
        initLobster: (p: string) => Promise<void>
        isLobsterReady: () => boolean
      }
      await mod.initLobster(pw)
      return mod.isLobsterReady()
    } catch (e) {
      console.warn('[e2e] initLobster 失败：', e)
      return false
    }
  }, masterPassword)
}

/**
 * 一站式进入某个受保护路由：写登录态 → 停在安全页 → 解锁 → 再导航过去。
 *
 * 顺序不能换：
 *   1. localStorage 必须在应用首行脚本前就位（addInitScript）；
 *   2. `initLobster` 是异步的，导航守卫是同步的 ⇒ 必须先解锁再导航，
 *      否则第一次导航就被 Case C 弹去 unlock，来回都白跑。
 */
export async function gotoAuthenticated(
  page: Page,
  hash: string,
  opts: { unlock?: boolean } = {},
): Promise<void> {
  await seedTokenAuth(page)
  await page.goto('/#/ai')
  await page.waitForLoadState('domcontentloaded')
  if (opts.unlock !== false) {
    const ok = await unlockLobster(page)
    if (!ok) throw new Error('initLobster 未成功：requiresLobster 路由进不去')
  }
  await page.goto(`/#${hash}`)
  // 等懒加载 chunk + 首屏请求落定；断言交给用例自己写。
  await page.waitForLoadState('domcontentloaded')
}
