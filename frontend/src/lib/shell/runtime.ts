/**
 * shell/runtime.ts — Hyper 运行时单例与路由适配。
 *
 * 把 NavigationContextStore / TitleResolver / BackDispatcher 绑成一个整体，
 * 并把 **四个返回来源**（顶栏返回钮、键盘 Esc、Android 系统返回、内容区手势）
 * 统一交给 BackDispatcher 裁决。之前它们各自判断"要不要先关弹窗"，
 * 于是出现过「关了两层」「关了弹窗又跳了路由」。
 *
 * ⚠️ 两条降级纪律：
 *
 *  1. **安装失败不得阻断启动。** 这个模块是增强层，不是前置依赖。
 *     任何一步抛错都退回「旧行为」并记一条诊断，而不是让白屏。
 *     对应 UI规范 06 §8：旧宿主缺能力时用 Web 菜单/手动关闭/页面内刷新。
 *  2. **no-op 不得冒充 success。** 未处理的返回一律显式返回
 *     `handed-to-system` / `blocked`，调用方据此决定是否退出应用。
 */

import { NavigationContextStore } from './navigationContext.ts'
import { TitleResolver } from './titleResolver.ts'
import { BackDispatcher, type RouterAdapter } from './backDispatcher.ts'
import type { BackOutcome, NavigationScope } from './types.ts'

/** vue-router 的最小形状。本模块不 import vue-router，便于单测。 */
export interface RouterLike {
  currentRoute: { value: { fullPath: string; name?: string | symbol; meta?: Record<string, unknown> } }
  push(to: unknown): Promise<unknown>
  replace(to: unknown): Promise<unknown>
  back(): void
  afterEach(hook: (to: unknown, from: unknown, failure: unknown) => unknown): () => void
  beforeEach(hook: (to: unknown, from: unknown) => unknown): () => void
}

export interface ShellRuntime {
  store: NavigationContextStore
  titles: TitleResolver
  back: BackDispatcher
  /** 当前账号域。换账号/登出时必须换，否则会跨账号恢复标题与筛选。 */
  setScope(scope: NavigationScope): void
  scope(): NavigationScope
  /** 注销路由监听。 */
  dispose(): void
  /** 安装过程中的问题（不抛异常，只记录）。 */
  diagnostics: string[]
}

let singleton: ShellRuntime | null = null
const UNSET_SCOPE: NavigationScope = { serverId: '', accountId: '' }

function metaTitle(to: { meta?: Record<string, unknown> } | null | undefined): string | undefined {
  const t = to?.meta?.title
  return typeof t === 'string' && t.length > 0 ? t : undefined
}

/**
 * 适配 vue-router：pop 失败（被守卫阻止）时返回 false，
 * 让 BackDispatcher 记 blocked 而不是假装成功。
 *
 * ⚠️ 这里**不能**用「等一个宏任务再看 currentRoute 变没变」判定成功
 * （原实现是 `await setTimeout(r, 0)`）。本仓 72 个路由**全部**是
 * `import()` 懒加载（见 app/router-mobile.ts），vue-router 导航必须
 * await 组件 chunk 加载与守卫链，必然跨多个宏任务。只等一个宏任务，
 * 路径一定还没变 ⇒ pop() 恒返回 false ⇒ 每次返回都被记成
 * `blocked: router-guard` ⇒ **用户按返回键完全没反应**。
 * 实测：导航耗时 2/4/8 个宏任务时全部误报 blocked（耗时 1 个才"正常"，
 * 而真实的懒加载路由不可能 1 个宏任务就绪）。
 *
 * 正确判据是**等 vue-router 自己的导航结果 Promise**：
 * `router.back()` 之后，`router.afterEach` 一定会被调用一次
 * （成功时 failure 为空，被阻止/取消时 failure 非空），据此判定。
 */
function toRouterAdapter(
  router: RouterLike,
  settle: (timeoutMs: number) => Promise<{ failure: unknown } | null>,
): RouterAdapter {
  return {
    async pop() {
      const before = router.currentRoute.value.fullPath
      const waited = settle(NAV_SETTLE_TIMEOUT_MS)
      router.back()
      const result = await waited
      // 没等到导航落定（超时）也算没移动 —— 宁可报 blocked，
      // 也不能假装成功：BackDispatcher 靠这个返回值决定是否记 page-popped。
      if (!result) return false
      if (result.failure) return false
      return router.currentRoute.value.fullPath !== before
    },
    async replace(fallbackPath: string) {
      try {
        await router.replace(fallbackPath)
        return true
      } catch {
        return false
      }
    },
  }
}

/**
 * 导航落定的等待上限。
 *
 * 真机冷启动后首次进入一个未缓存的懒加载路由要拉 chunk，弱网下可能到秒级。
 * 1.5s 是「宁可等久一点也不要误判」的折中：超时后按 blocked 处理，
 * 用户再按一次返回即可，绝不假装成功。
 */
const NAV_SETTLE_TIMEOUT_MS = 1500

/**
 * 创建运行时（不自动装成单例，便于单测造多个）。
 */
export function createShellRuntime(router: RouterLike, opts: { scope?: NavigationScope } = {}): ShellRuntime {
  const diagnostics: string[] = []
  const store = new NavigationContextStore()
  const titles = new TitleResolver({ appName: 'OpenCode Pocket' })
  const back = new BackDispatcher()

  /**
   * 导航落定通知器：pop() 调用它来等「这一次 back() 的导航结果」。
   *
   * 为什么不用 setTimeout：见 toRouterAdapter 上方的注释——72 个懒加载
   * 路由的导航必然跨多个宏任务。这里改成「afterEach 兑现一次」，
   * 于是判定依据是 vue-router 自己的结论，而不是我们猜的时长。
   */
  let pendingSettle: ((r: { failure: unknown } | null) => void) | null = null
  let settleTimer: ReturnType<typeof setTimeout> | null = null

  const settle = (timeoutMs: number): Promise<{ failure: unknown } | null> =>
    new Promise((resolve) => {
      // 只保留一个在途等待者：pop() 是单飞的（BackDispatcher 保证），
      // 但仍要防御性地清掉旧的，避免旧 timer 让新 Promise 提前兑现。
      if (settleTimer) clearTimeout(settleTimer)
      pendingSettle = resolve
      settleTimer = setTimeout(() => {
        settleTimer = null
        pendingSettle = null
        resolve(null) // 超时 = 没等到落定
      }, timeoutMs)
    })

  const notifySettle = (failure: unknown) => {
    if (!pendingSettle) return
    if (settleTimer) clearTimeout(settleTimer)
    settleTimer = null
    const resolve = pendingSettle
    pendingSettle = null
    resolve({ failure })
  }

  back.setRouter(toRouterAdapter(router, settle))

  let scope: NavigationScope = opts.scope ?? UNSET_SCOPE

  const runtime: ShellRuntime = {
    store,
    titles,
    back,
    diagnostics,
    setScope(next) {
      const prev = scope
      const unchanged = prev.serverId === next.serverId && prev.accountId === next.accountId
      scope = next
      // ⚠️ 必须比较「是否变化」，而不是「是否非空」。
      // 早期实现只在非空时清理，于是**登出**（scope 变回空）不触发清理，
      // 上一位用户的标题与导航条目会留在内存里等下一个人登录。
      // 「登出要清空」比「登录要清空」更重要，因为前者是隐私边界。
      if (!unchanged && (prev.serverId !== '' || prev.accountId !== '' || next.serverId !== '' || next.accountId !== '')) {
        store.reset()
        titles.clearAll()
        back.reset()
      }
    },
    scope: () => scope,
    dispose() {
      removeAfterEach?.()
      removeBeforeEach?.()
      back.reset()
      // 清掉在途的落定等待：afterEach 已注销，不会再有人兑现它，
      // 留着就是个悬空 timer（最长 1.5s）。
      if (settleTimer) clearTimeout(settleTimer)
      settleTimer = null
      pendingSettle = null
    },
  }

  let removeAfterEach: (() => void) | undefined
  let removeBeforeEach: (() => void) | undefined

  try {
    // Vue Router 是路由真源：导航成功后原子更新上下文。
    // 失败（守卫阻止/抛错）不提交条目，cursor 不动。
    removeAfterEach = router.afterEach((to, _from, failure) => {
      // 任何一次导航落定都要兑现在途的 pop() 等待——成功与被阻止都要，
      // 否则 pop() 只能等到超时（1.5s）才返回，用户会感到「返回卡了一下」。
      notifySettle(failure)

      if (failure) {
        const cur = store.current()
        if (cur) store.recordFailed(cur.id, 'route', String((failure as { message?: string })?.message ?? failure))
        return
      }
      const route = to as { fullPath: string; name?: string | symbol; meta?: Record<string, unknown> }
      const cur = store.current()

      // 后退落定：命中 cursor 之前的既有页面条目 ⇒ 这次是「返回」而不是
      // 「前进」。vue-router v4 的 afterEach 不给方向，只能这样判。
      //
      // 不做这一步的后果（实测）：每次返回都被记成 push，entries 单调增长
      // ——前进 3 步按 2 次返回，栈从 3 条涨到 5 条、cursor 从 2 涨到 4。
      // 于是 backDispatcher 的 `ctx.cursor > 0` 恒真，永远判「有页面前驱」，
      // 永远到不了 §5 安全 fallback 与 §6 交还系统 ⇒ 返回表现为没反应。
      const popped = cur ? store.popTo(route.fullPath) : undefined
      if (popped) {
        titles.beginRender(popped.id)
        const meta = metaTitle(route)
        if (meta) titles.register(popped.id, meta)
        return
      }

      // 同一个页面内的 query 变化按 replace 处理，不产生可回退的新条目。
      const sameEntry = cur && cur.fullPath.split('?')[0] === route.fullPath.split('?')[0]
      const entry = store.open({
        fullPath: route.fullPath,
        presentation: 'page',
        openedBy: sameEntry ? 'replace' : 'push',
        scope,
        routeName: typeof route.name === 'string' ? route.name : undefined,
        title: metaTitle(route),
        titleSource: metaTitle(route) ? 'route' : 'registered',
      })
      // 每次页面激活推进 render epoch：旧页的异步标题从此被守门拒绝。
      titles.beginRender(entry.id)
      titles.register(entry.id, entry.title)
    })
  } catch (e) {
    diagnostics.push(`afterEach 安装失败：${(e as Error).message}`)
  }

  try {
    // 守卫阻止/用户取消：留在原页面，记 cancelled。
    removeBeforeEach = router.beforeEach((to, from) => {
      const cur = store.current()
      if (cur) {
        store.recordCancelled(cur.id, 'guard', `→ ${(to as { fullPath?: string })?.fullPath ?? '?'}`)
      }
      void from
      return true
    })
  } catch (e) {
    diagnostics.push(`beforeEach 安装失败：${(e as Error).message}`)
  }

  return runtime
}

/** 取得/建立进程内单例。 */
export function getShellRuntime(router: RouterLike, opts: { scope?: NavigationScope } = {}): ShellRuntime {
  if (!singleton) singleton = createShellRuntime(router, opts)
  return singleton
}

/** 仅供测试：清掉单例。 */
export function __resetShellRuntimeForTest(): void {
  singleton?.dispose()
  singleton = null
}

/**
 * 统一返回入口。
 *
 * @returns 永不返回 undefined：调用方据此决定是否把意图交还系统。
 */
export async function dispatchBack(runtime: ShellRuntime): Promise<BackOutcome> {
  try {
    return await runtime.back.back(runtime.store.snapshot())
  } catch (e) {
    runtime.diagnostics.push(`返回裁决异常：${(e as Error).message}`)
    return { kind: 'blocked', reason: 'exception' }
  }
}
