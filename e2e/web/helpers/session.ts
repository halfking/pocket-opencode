/**
 * E2E 会话助手（session.ts）—— 在 helpers/auth.ts 的 `login(page)` 之上，
 * 补齐审批 / RSS spec 需要的「守卫感知导航 + 选中实例预种 + 后端数据探测」。
 *
 * 与 helpers/auth.ts 的分工（命名对齐）：
 *   - auth.ts `login(page)`：登录复用入口（账号密码 + 首次「创建主密码」分支，
 *     最终落达 /#/ai）。本模块 re-export，spec 统一从这里 import。
 *   - session.ts `navigateGuarded(page, path)`：登录后跳转受保护 hash 路由，
 *     覆盖 auth.ts 未处理的「解锁屏」分支 —— token 有效但本地加密库未就绪时，
 *     路由守卫（frontend/src/app/routeGuards.ts Case C）会把 requiresLobster
 *     路由（/rss、/sessions/:id 等）弹到 /login?unlock=1&returnTo=…，此时
 *     LoginView 渲染 needUnlock 表单（placeholder「输入主密码解锁」，按钮
 *     「解锁」，失败文案前缀「解锁失败（主密码错误？）」），本助手自动填
 *     auth.ts 的 E2E_MASTER_PASSWORD 完成解锁；若登录态整个丢失则回退调
 *     auth.ts 的 login() 重试。
 *   - `seedSelectedInstance(page, …)`：把「当前实例」预种进 localStorage
 *     （键名与 frontend/src/config/selected-instance.ts 常量一致）。/ai 的
 *     实例级审批视图 useInstanceApprovals.refresh() 依赖它，未选中时直接 no-op。
 *   - API 探测：`request` fixture 自带 baseURL（playwright.config.ts 指向
 *     http://127.0.0.1:4174，vite 已把 /api、/ws 同源代理到后端 8090），带
 *     Bearer 调 GET /api/mobile/approvals、GET /api/instances、GET /api/rss/*，
 *     供 spec 判定「后端有没有种子数据」，缺失时走 test.skip 降级。
 *
 * 环境变量（定义在 helpers/auth.ts，此处 re-export 保持单一来源）：
 *   E2E_USERNAME / E2E_PASSWORD / E2E_MASTER_PASSWORD / E2E_BASE_URL
 */
import type { APIRequestContext, Page } from '@playwright/test'
import { E2E_MASTER_PASSWORD, login } from './auth'

/** 登录复用 + 凭据/主密码环境变量：单一来源在 helpers/auth.ts。 */
export { E2E_MASTER_PASSWORD, login }

/** localStorage 键名，与 frontend/src/config/selected-instance.ts 的常量保持一致（勿改拼写）。 */
export const SELECTED_INSTANCE_KEY = 'selected_instance'
export const SELECTED_INSTANCE_ID_KEY = 'selected_instance_id'

export interface NavResult {
  /** 是否最终落达目标 hash 路由 */
  landed: boolean
  /** 结果说明，用于 skip / 失败信息 */
  reason: string
}

// ---------------------------------------------------------------------------
// hash 路由工具（应用使用 createWebHashHistory，见 frontend/src/app/router-mobile.ts）
// ---------------------------------------------------------------------------

/** 解析 hash 路由的 path 部分：'…/#/login?returnTo=%2Frss' → '/login' */
function hashPathOf(url: string | URL): string {
  const raw = typeof url === 'string' ? new URL(url).hash : url.hash
  const hash = raw.replace(/^#/, '')
  const path = hash.startsWith('/') ? hash : `/${hash}`
  return path.split('?')[0]
}

/** 等待页面 hash 路由落达指定 path；超时返回 false（不抛错，方便做分支判定）。 */
async function waitForHashPath(page: Page, path: string, timeout: number): Promise<boolean> {
  try {
    await page.waitForURL((u) => hashPathOf(u) === path, { timeout })
    return true
  } catch {
    return false
  }
}

/**
 * 跳转受保护 hash 路由（如 /rss、/sessions/xxx），处理三种真实页面状态
 * （选择器均出自 frontend/src/features/auth/LoginView.vue / unlock-auth.ts）：
 *   1. 直接落达（登录后的同文档 hash 导航，lobster 就绪态保持）→ landed
 *   2. 解锁屏（/login?unlock=1，needUnlock 分支）→ 填主密码点「解锁」
 *      → 成功后 initLobster 完成，router.replace(returnTo) → landed
 *   3. 登录态丢失（完整登录表单）→ 复用 auth.ts 的 login() 后重试
 *
 * 本函数不抛异常（login() 内部的断言除外），由 spec 决定 fail 还是 skip。
 */
export async function navigateGuarded(page: Page, path: string): Promise<NavResult> {
  for (let attempt = 0; attempt < 3; attempt++) {
    await page.goto(`/#${path}`)
    if (await waitForHashPath(page, path, 5000)) {
      return { landed: true, reason: 'ok' }
    }

    const current = hashPathOf(page.url())
    if (current !== '/login') {
      return { landed: false, reason: `落在意外路由 #${current}` }
    }

    // 分支 A：解锁屏（token 有效但本地加密库未就绪；requiresLobster 路由被守卫弹回）
    const unlockInput = page.getByPlaceholder('输入主密码解锁')
    if (await unlockInput.isVisible().catch(() => false)) {
      await unlockInput.fill(E2E_MASTER_PASSWORD)
      await page.getByRole('button', { name: '解锁', exact: true }).click()
      // 成功：initLobster 完成 → router.replace(returnTo)；失败：出现 .error-message
      const outcome = await Promise.race([
        page
          .locator('.error-message')
          .waitFor({ state: 'visible', timeout: 20_000 })
          .then(() => 'error' as const),
        waitForHashPath(page, path, 20_000).then((ok) => (ok ? ('nav' as const) : ('timeout' as const))),
      ]).catch(() => 'timeout' as const)
      if (outcome === 'nav') {
        return { landed: true, reason: 'ok' }
      }
      const msg = await page.locator('.error-message').textContent().catch(() => '')
      return {
        landed: false,
        reason: `主密码解锁失败：${(msg || '超时').trim()}（E2E_MASTER_PASSWORD 与既有主密码不一致？）`,
      }
    }

    // 分支 B：登录态丢失 → 复用 auth.ts 的 login（含创建主密码分支）后重试
    await login(page)
  }
  return { landed: false, reason: '重试 3 次仍未落达目标路由' }
}

/**
 * 在页面加载前把「当前实例」种进 localStorage。必须在 login(page) 之前调用
 * （addInitScript 只对后续导航生效；/ai 的 TasksView 在 onMounted 里通过
 * readSelectedInstance() 读取）。
 */
export async function seedSelectedInstance(
  page: Page,
  instanceId: string,
  displayName?: string,
): Promise<void> {
  await page.addInitScript(
    ({ id, name, key, idKey }) => {
      localStorage.setItem(key, JSON.stringify({ id, displayName: name || id }))
      localStorage.setItem(idKey, id)
    },
    {
      id: instanceId,
      name: displayName,
      key: SELECTED_INSTANCE_KEY,
      idKey: SELECTED_INSTANCE_ID_KEY,
    },
  )
}

// ---------------------------------------------------------------------------
// API 探测（与前端 http.ts 同源同鉴权：Bearer token；路径用相对形式走 baseURL）
// ---------------------------------------------------------------------------

/** 直接调 POST /api/auth/login 换 token（失败抛错：登录接口不可用属环境故障，应 fail）。 */
export async function apiLoginToken(request: APIRequestContext): Promise<string> {
  const res = await request.post('/api/auth/login', {
    data: {
      username: process.env.E2E_USERNAME ?? 'admin',
      password: process.env.E2E_PASSWORD ?? 'Veritrans&9527',
    },
  })
  if (!res.ok()) {
    throw new Error(`API 登录失败：POST /api/auth/login → ${res.status()}`)
  }
  const body = (await res.json()) as { token?: string }
  if (!body.token) throw new Error('API 登录响应缺少 token 字段')
  return body.token
}

/** 带 Bearer 的 GET；非 2xx 返回 null（由调用方决定 skip 语义），JSON 解析失败也返回 null。 */
async function apiGetJson<T>(request: APIRequestContext, token: string, path: string): Promise<T | null> {
  try {
    const res = await request.get(path, { headers: { Authorization: `Bearer ${token}` } })
    if (!res.ok()) return null
    return (await res.json()) as T
  } catch {
    return null
  }
}

/** 最小化审批数据模型（对齐 frontend/src/api/approvals.ts 的 PermissionRequest / QuestionRequest）。 */
export interface PendingApprovalsProbe {
  permissions: number
  questions: number
  total: number
  /** 第一个问答请求的第一个子问题首个选项 label（问答流程用，可能为空） */
  firstQuestionOption?: string
}

/**
 * 拉取待审批列表：GET /api/mobile/approvals[?instance_id=…]
 * （路径与查询参数来自 frontend/src/api/approvals.ts listPendingApprovals）。
 * 返回 null 表示后端不可达 / 未部署该路由。
 */
export async function fetchPendingApprovals(
  request: APIRequestContext,
  token: string,
  instanceId?: string,
): Promise<PendingApprovalsProbe | null> {
  const path = instanceId
    ? `/api/mobile/approvals?instance_id=${encodeURIComponent(instanceId)}`
    : '/api/mobile/approvals'
  const body = await apiGetJson<{
    permissions?: Array<{ id: string }>
    questions?: Array<{ id: string; questions?: Array<{ options?: Array<{ label: string }> }> }>
  }>(request, token, path)
  if (!body) return null
  const permissions = body.permissions?.length ?? 0
  const questions = body.questions?.length ?? 0
  return {
    permissions,
    questions,
    total: permissions + questions,
    firstQuestionOption: body.questions?.[0]?.questions?.[0]?.options?.[0]?.label,
  }
}

/** GET /api/instances（对齐 frontend/src/api/client.ts getInstances：{ instances: [...] }）。 */
export async function fetchInstances(
  request: APIRequestContext,
  token: string,
): Promise<Array<{ id: string; displayName: string }>> {
  const body = await apiGetJson<{ instances?: Array<{ id?: string; displayName?: string }> }>(
    request,
    token,
    '/api/instances',
  )
  return (body?.instances ?? [])
    .filter((i): i is { id: string; displayName: string } => typeof i.id === 'string' && i.id.length > 0)
    .map((i) => ({ id: i.id, displayName: i.displayName || i.id }))
}

/** GET /api/rss/sources + GET /api/rss/items 计数（对齐 frontend/src/api/rss.ts 的 { sources } / { items }）。 */
export async function probeRssCounts(
  request: APIRequestContext,
  token: string,
): Promise<{ sources: number; items: number } | null> {
  const sourcesBody = await apiGetJson<{ sources?: unknown[] }>(request, token, '/api/rss/sources')
  const itemsBody = await apiGetJson<{ items?: unknown[] }>(request, token, '/api/rss/items?limit=1')
  if (!sourcesBody || !itemsBody) return null
  return { sources: sourcesBody.sources?.length ?? 0, items: itemsBody.items?.length ?? 0 }
}
