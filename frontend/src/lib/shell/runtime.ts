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
 */
function toRouterAdapter(router: RouterLike): RouterAdapter {
  return {
    async pop() {
      const before = router.currentRoute.value.fullPath
      router.back()
      // 导航是异步的；这里不假装成功——交由调用方在失败时看到 blocked。
      // 判定方式：当前路径是否真的变了。守卫阻止时保持不变。
      await new Promise((r) => setTimeout(r, 0))
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
 * 创建运行时（不自动装成单例，便于单测造多个）。
 */
export function createShellRuntime(router: RouterLike, opts: { scope?: NavigationScope } = {}): ShellRuntime {
  const diagnostics: string[] = []
  const store = new NavigationContextStore()
  const titles = new TitleResolver({ appName: 'OpenCode Pocket' })
  const back = new BackDispatcher()
  back.setRouter(toRouterAdapter(router))

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
    },
  }

  let removeAfterEach: (() => void) | undefined
  let removeBeforeEach: (() => void) | undefined

  try {
    // Vue Router 是路由真源：导航成功后原子更新上下文。
    // 失败（守卫阻止/抛错）不提交条目，cursor 不动。
    removeAfterEach = router.afterEach((to, _from, failure) => {
      if (failure) {
        const cur = store.current()
        if (cur) store.recordFailed(cur.id, 'route', String((failure as { message?: string })?.message ?? failure))
        return
      }
      const route = to as { fullPath: string; name?: string | symbol; meta?: Record<string, unknown> }
      const cur = store.current()
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
