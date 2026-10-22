// local-storage-remove-failure.test.ts — §870：本地存储的**删除**侧。
//
// §866–§869 四节全在扫 `setItem`（写入）。`removeItem` / `clear` 这条
// **删除**侧从未扫过，而它牵着「退出登录」这个安全动作。
//
// 核心口径（来自 §866 的族规律，但要反过来用一次）：
//   写失败 = 用户的会话被弄丢（丢东西）；
//   **删失败 = 会话被留在盘上** —— 用户明确点了「退出」，而系统其实还在记着他。
// ⇒ 同一个「静默」，两个方向，后果严重度不同。
//
// 缺陷背景：
//   D39 auth.clearLocal() 四条裸 removeItem
//      ⇒ 内存态已清、本次会话看着登出成功；但 state 初始化会在下次启动
//        把旧 token 读回来 ⇒ 共享设备上「退出登录」等于没退。
//      且抛错会冒泡出 clearLocal()（logout() 没有包 try/catch）。
//   D40 SettingsView::handleLogout 只删了 pocket_user + selected_server
//      ⇒ token 没删、内存没清、也没打后端撤销；而路由守卫判的是**内存**里的
//        auth.isAuthenticated ⇒ 被推到登录页后**后退即可回到应用**，
//        仍持有效 token ⇒ 「退出登录」在本次会话根本没发生。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const HERE = dirname(fileURLToPath(import.meta.url))
const SRC = join(HERE, '..')
// ★ 读入口归一 CRLF → LF（2026-10-22 Windows 实测修掉）
//
//   本仓的 .ts/.vue 在 Windows 检出是 CRLF（core.autocrlf=true），
//   而下面这些用例的「变异锚点」正则里写死了 \n ⇒ 锚点匹配不上
//   ⇒ 变异根本没发生 ⇒ 断言报「变异没生效（锚点与真源码不匹配）」
//   ⇒ 该报红的那条反而报红成「锚点没命中」，看起来像源码变了，其实没变。
//   ⇒ Linux 上源文件是 LF ⇒ 锚点命中 ⇒ **Linux 一直绿**。
//
//   归一在**读入口**（一处盖住全部锚点），不是逐条改正则
//   —— 与 D6–D10、§88 同族纪律，也与仓内既有先例一致：
//   src/features/email/__tests__/account-read-path-normalization.test.mjs:46-48。

const read = (rel: string) => readFileSync(join(SRC, rel), 'utf8').replace(/\r\n/g, '\n')

/** ⚠ HTML 注释必须**先**去掉（§868 记的事故：`/api/*` 这类字面路径会启动块注释）。 */
const strip = (t: string): string =>
  t.replace(/<!--[\s\S]*?-->/g, (m) => m.replace(/[^\n]/g, ' '))
   .replace(/\/\*[\s\S]*?\*\//g, (m) => m.replace(/[^\n]/g, ' '))
   .replace(/(^|[^:])\/\/[^\n]*/g, '$1')

const AUTH = strip(read('stores/auth.ts'))
/** ⚠ 先抽 `<script>`，再定位、再剥注释（§868：先定位再剥会把范围毁掉）。 */
const SETTINGS_SCRIPT = strip(
  /<script[^>]*>([\s\S]*?)<\/script>/.exec(read('features/settings/SettingsView.vue'))?.[1] ?? '',
)

/**
 * 取函数体：匹配**锚到行首**。
 * ⚠ 取件器事故已累计 4 次：前缀字符守卫挡不住调用点 ——
 *   `return clearLocal()` 前面就是一个空格、且调用点可能出现在定义之前。
 */
function fnBody(rawCode: string, name: string): string {
  const code = strip(rawCode)
  const m = new RegExp(
    `(?:^|\\n)\\s*(?:async\\s+)?(?:function\\s+)?${name}\\s*(?:<[^>]*>)?\\s*\\(`,
  ).exec(code)
  if (!m) return ''
  let d = 0
  let after = -1
  for (let i = m.index + m[0].length - 1; i < code.length; i++) {
    if (code[i] === '(') d++
    else if (code[i] === ')') { d--; if (d === 0) { after = i + 1; break } }
  }
  if (after < 0) return ''
  const start = code.indexOf('{', after)
  if (start < 0) return ''
  let depth = 0
  for (let i = start; i < code.length; i++) {
    if (code[i] === '{') depth++
    else if (code[i] === '}') { depth--; if (depth === 0) return code.slice(start, i + 1) }
  }
  return ''
}

/** D39：删除必须逐个进行、不得冒泡、且必须报告删失败的键。 */
function clearLocalIsComplete(src: string) {
  const body = fnBody(src, 'clearLocal')
  if (!body) return { ok: false, why: '找不到 clearLocal' }
  const delAt = body.indexOf('localStorage.removeItem')
  if (delAt < 0) return { ok: false, why: 'clearLocal 里没有删除动作' }
  if (!/for\s*\(/.test(body)) return { ok: false, why: '删除没有逐个进行 ⇒ 第一个键删失败会连坐后面的键' }
  if (!/try\s*\{/.test(body)) return { ok: false, why: '删除没有 try ⇒ 失败会冒泡出 clearLocal（logout 没有包）' }
  if (!/catch/.test(body)) return { ok: false, why: '删除没有 catch' }
  if (!/console\.warn/.test(body)) return { ok: false, why: '删除失败没有任何日志 ⇒ 「退出没退干净」完全不可诊断' }
  if (!/sessionClearFailed\s*=\s*failed\.length\s*>\s*0/.test(body)) {
    return { ok: false, why: '删除失败没有置位 ⇒ 下次启动静默地把旧 token 读回来' }
  }
  if (!/sessionClearMissing\s*=\s*failed/.test(body)) return { ok: false, why: '未删掉的键名没有被记录' }
  return { ok: true }
}

/** D40：设置页的「退出登录」必须真的退出（走 store 的正规登出）。 */
function settingsLogoutReallyLogsOut(src: string) {
  const body = fnBody(src, 'handleLogout')
  if (!body) return { ok: false, why: '找不到 handleLogout' }
  if (!/\.logout\(\)/.test(body)) {
    return { ok: false, why: '设置页的退出没有走 auth.logout() ⇒ token 没删、内存没清、后端没撤销' }
  }
  if (/removeItem\(\s*'pocket_user'\s*\)/.test(body)) {
    return { ok: false, why: '仍在只删 pocket_user —— 那是「内存还登录着、磁盘只剩残留 token」的半吊子登出' }
  }
  // 守卫判的是内存 isAuthenticated ⇒ 必须显式提醒这条依赖写进注释
  return { ok: true }
}

const ruler = () => ({
  clear: clearLocalIsComplete(AUTH),
  logout: settingsLogoutReallyLogsOut(SETTINGS_SCRIPT),
})

/**
 * D41：syncFromStorage 必须读到每一个「落盘了的」会话键。
 *
 * ⚠⚠ 取件器事故（本会话累计第 5 次）：第一版拿**字面量** `'pocket_token'`
 *   去匹配函数体，而源码用的是**常量** `TOKEN_KEY` ⇒ 恒红，读起来像
 *   「修复没生效」，其实是我把判据钉在字面量上。⇒ 锚到符号名，
 *   并单独断言「符号名 → 字面量」的映射仍然正确，两头都不放松。
 */
const KEY_CONSTS = {
  'pocket_token': 'TOKEN_KEY',
  'pocket_user': 'USER_KEY',
  'pocket_workspace_id': 'WS_KEY',
  'pocket_auth_method': 'METHOD_KEY',
} as const

function syncCoversAllPersistedKeys(src: string, keys: readonly string[]) {
  const body = fnBody(src, 'syncFromStorage')
  if (!body) return { ok: false, why: '找不到 syncFromStorage' }
  // 先确认「符号名 → 字面量」这条映射本身没漂
  for (const [literal, konst] of Object.entries(KEY_CONSTS)) {
    const decl = new RegExp(`const\\s+${konst}\\s*=\\s*'${literal}'`).exec(src)
    if (!decl) return { ok: false, why: `常量 ${konst} 不再等于 '${literal}' ⇒ 键名漂了，判据要跟着改` }
  }
  for (const literal of keys) {
    const konst = KEY_CONSTS[literal as keyof typeof KEY_CONSTS]
    if (!body.includes(konst)) {
      return { ok: false, why: `syncFromStorage 没有读 ${konst}('${literal}') ⇒ 该键在任何「从存储刷新」路径上都不会更新` }
    }
  }
  // 读到了还不够：必须真的赋值给对应的 state 字段
  for (const field of ['token', 'user', 'workspaceId', 'authMethod']) {
    if (!new RegExp(`this\\.${field}\\s*=\\s*stored`).test(body)) {
      return { ok: false, why: `syncFromStorage 读了却没有回写 this.${field}` }
    }
  }
  return { ok: true }
}

/** D41 的另一半：触发同步的那个 storage 监听也必须覆盖全部落盘键。 */
function storageListenerCoversAllKeys(src: string) {
  const m = /addEventListener\(\s*'storage'[\s\S]*?\n\s*\}\)/.exec(src)
  if (!m) return { ok: false, why: '找不到 storage 监听' }
  const body = m[0]
  for (const konst of Object.values(KEY_CONSTS)) {
    if (!body.includes(konst)) {
      return { ok: false, why: `storage 监听没认 ${konst} ⇒ 别的标签改了它，本标签收不到事件、根本不会触发同步` }
    }
  }
  return { ok: true }
}

describe('§870 · 尺子自证', () => {
  it('★★ 两个文件都读到了', () => {
    assert.ok(AUTH.includes('clearLocal'), '没读到 auth.clearLocal')
    assert.ok(SETTINGS_SCRIPT.includes('handleLogout'), '没读到设置页的退出动作')
  })

  it('★★ 先证明这把尺子在真源码上是绿的', () => {
    const v = ruler()
    assert.ok(v.clear.ok, v.clear.why)
    assert.ok(v.logout.ok, v.logout.why)
  })
})

describe('§870 · D39 删除失败必须可诊断且不得冒泡', () => {
  it('★★★ 四个键必须逐个删除、失败被记录', () => {
    const v = ruler()
    assert.ok(v.clear.ok, v.clear.why)
  })

  it('★★ 状态标志必须在 state 里声明（否则不是响应式的）', () => {
    const st = /state: \(\) => \(\{[\s\S]*?\n  \}\),/.exec(AUTH)?.[0] ?? ''
    assert.match(st, /sessionClearFailed/, 'state 里没有 sessionClearFailed')
    assert.match(st, /sessionClearMissing/, 'state 里没有 sessionClearMissing')
  })
})

describe('§870 · D40 设置页的「退出登录」必须真的退出', () => {
  it('★★★ 必须走 store 的正规登出', () => {
    const v = ruler()
    assert.ok(v.logout.ok, v.logout.why)
  })
})

describe('§870 · 变异负控', () => {
  it('把 clearLocal 改回四条裸 removeItem ⇒ D39 立刻红', () => {
    const body = fnBody(AUTH, 'clearLocal')
    assert.ok(body, '取不到 clearLocal 的函数体')
    const defective = body.replace(
      /const failed: string\[\] = \[[\s\S]*?this\.sessionClearMissing = failed\n/,
      `localStorage.removeItem('pocket_token')
      localStorage.removeItem('pocket_user')
      localStorage.removeItem('pocket_workspace_id')
      localStorage.removeItem('pocket_auth_method')
      this.sessionClearFailed = false
      this.sessionClearMissing = []
`,
    )
    assert.notEqual(defective, body, '变异没生效（锚点与真源码不匹配）')
    assert.equal(clearLocalIsComplete(AUTH.replace(body, defective)).ok, false,
      '尺子坏了：把缺陷形态喂回去竟然还判绿')
  })

  it('把设置页的退出改回「只删 pocket_user」⇒ D40 立刻红', () => {
    const body = fnBody(SETTINGS_SCRIPT, 'handleLogout')
    assert.ok(body, '取不到 handleLogout 的函数体')
    const defective = body.replace(
      /await authStore\.logout\(\)/,
      "localStorage.removeItem('pocket_user')",
    )
    assert.notEqual(defective, body, '变异没生效（锚点与真源码不匹配）')
    assert.equal(settingsLogoutReallyLogsOut(SETTINGS_SCRIPT.replace(body, defective)).ok, false,
      '尺子坏了：把缺陷形态喂回去竟然还判绿')
  })
})

/**
 * §871：「从存储刷新会话」这条路径必须覆盖**全部落盘的键**。
 *
 * ⚠ 它不是只在 storage 事件里被调用 —— `app/routeGuards.ts:55-56` 在
 * **每次导航**都调它。所以它漏掉的键，在整个应用生命周期里都不会被刷新。
 *
 * 有消费者的后果：
 *   composables/usePendingApprovals.ts:80 → workspaceId: auth.workspaceId || 'default'
 *   app/routeGuards.ts:87 → 缺 workspaceId 时要求补齐
 * ⇒ 跨标签登录到另一个 workspace 后，本标签仍按**旧 workspace** 取待审批。
 *
 * ⚠ `userId` **刻意不在**这个列表里：它从来就不落盘（state 里写死 `''`，
 *   全仓没有 `pocket_user_id` 键）—— 实测它是哑雷（`scope()` 零外部消费者，
 *   而 reset 只依赖「值变化与否」，workspaceId 变化已足够触发），只登记不修。
 */
const PERSISTED_KEYS = ['pocket_token', 'pocket_user', 'pocket_workspace_id', 'pocket_auth_method'] as const

describe('§871 · D41 syncFromStorage 必须覆盖全部落盘的会话键', () => {
  it('★★ 先证明这把尺子在真源码上是绿的', () => {
    const v = syncCoversAllPersistedKeys(AUTH, PERSISTED_KEYS)
    assert.ok(v.ok, v.why)
  })

  it('★★★ 四个落盘键都必须在 syncFromStorage 里被读到', () => {
    const v = syncCoversAllPersistedKeys(AUTH, PERSISTED_KEYS)
    assert.ok(v.ok, v.why)
  })

  it('★★ 触发同步的 storage 监听也必须覆盖全部落盘键（只修 syncFromStorage 是**不完整**的）', () => {
    const v = storageListenerCoversAllKeys(AUTH)
    assert.ok(v.ok, v.why)
  })

  it('把 workspaceId / authMethod 的刷新删掉 ⇒ D41 立刻红', () => {
    const body = fnBody(AUTH, 'syncFromStorage')
    assert.ok(body, '取不到 syncFromStorage 的函数体')
    const defective = body.replace(
      /if \(this\.workspaceId !== storedWorkspace\) \{[\s\S]*?\n      \}\n      if \(this\.authMethod !== storedMethod\) \{[\s\S]*?\n      \}\n/,
      '',
    )
    assert.notEqual(defective, body, '变异没生效（锚点与真源码不匹配）')
    assert.equal(syncCoversAllPersistedKeys(AUTH.replace(body, defective), PERSISTED_KEYS).ok, false,
      '尺子坏了：把缺陷形态喂回去竟然还判绿')
  })
})

/**
 * §872/D42：会话写入必须**原子** —— 5 个落盘键同进同出。
 *
 * 缺陷形态：原来 `setAuth` 写 3 个键、`setAuthWithWorkspace` **另一次**调用
 * 再写第 4 个 ⇒ 两段各自 try/catch ⇒ 撕裂态（磁盘有 token/user/method、
 * 唯独缺 workspace_id）。刷新后 workspaceId 永久为空。
 *
 * 有消费者的后果：`composables/usePendingApprovals.ts:80` 的离线分支
 *   → `enqueueApprovalReplyLocally({ workspaceId: auth.workspaceId || 'default' })`
 * ⇒ 离线回复的**审批决策**被打上 `'default'`，记进错误的 workspace。
 * 路由守卫 `app/routeGuards.ts:87` 只 console.debug 不拦截 ⇒ 这条链
 * **不会**被守卫挡住。
 *
 * ⭐ 判据钉「写入只有**一个入口**」这个机械性质，而不是钉某个函数签名 ——
 *   任何「第二次 writeSession」都会重新引入撕裂态。
 */
/** D42：登录路径上的 5 个落盘键必须同进同出。 */
function sessionWriteIsAtomic(src: string) {
  // ⚠ 第一版判据写的是「全文件 writeSession 调用点唯一」，被 `setLegacyUser`
  //   判红 —— 但那是**独立**的 legacy 入口（只存 user、不存 token），
  //   本来就该自己落盘。⇒ 属性必须收窄到「登录路径」，
  //   否则门禁会逼人把合法的独立入口合并掉。
  const setAuth = fnBody(src, 'setAuth')
  if (!setAuth) return { ok: false, why: '找不到 setAuth' }
  const n = (setAuth.match(/this\.writeSession\(/g) ?? []).length
  if (n !== 1) return { ok: false, why: `setAuth 里有 ${n} 次落盘 ⇒ 登录路径被拆成多段（§872/D42）` }
  if (!/\.\.\.extra/.test(setAuth)) return { ok: false, why: 'setAuth 没有把额外键并入同一次写入' }
  const body = fnBody(src, 'setAuthWithWorkspace')
  if (!body) return { ok: false, why: '找不到 setAuthWithWorkspace' }
  if (/this\.writeSession\(/.test(body)) {
    return { ok: false, why: 'setAuthWithWorkspace 单独落盘 ⇒ workspace_id 与 token 分两次写入' }
  }
  if (!/this\.setAuth\(token, user, method, \[\[WS_KEY, workspaceId\]\]/.test(body)) {
    return { ok: false, why: 'setAuthWithWorkspace 没有把 workspace 键并入同一次写入' }
  }
  return { ok: true }
}

describe('§872 · D42 会话写入必须原子（不得有第二次落盘）', () => {
  it('★★ 先证明这把尺子在真源码上是绿的', () => {
    const v = sessionWriteIsAtomic(AUTH)
    assert.ok(v.ok, v.why)
  })

  it('★★★ setAuth 内部只有一次落盘（登录路径不得被拆成多段）', () => {
    const v = sessionWriteIsAtomic(AUTH)
    assert.ok(v.ok, v.why)
  })

  it('★★★ setAuthWithWorkspace 不得自己再落盘一次', () => {
    const body = fnBody(AUTH, 'setAuthWithWorkspace')
    assert.ok(body, '找不到 setAuthWithWorkspace')
    assert.doesNotMatch(body, /this\.writeSession\(/,
      'setAuthWithWorkspace 仍在单独落盘 ⇒ workspace_id 可能与 token 分两次写入')
    assert.match(body, /this\.setAuth\(/,
      'setAuthWithWorkspace 没有把 workspace 键并入同一次写入')
  })

  it('★★★ 把 workspace 键退回「第二次写入」⇒ D42 立刻红', () => {
    const body = fnBody(AUTH, 'setAuthWithWorkspace')
    assert.ok(body, '取不到 setAuthWithWorkspace 的函数体')
    const defective = body.replace(
      /this\.setAuth\(token, user, method, \[\[WS_KEY, workspaceId\]\]\)/,
      "this.setAuth(token, user, method)\n      this.writeSession([[WS_KEY, workspaceId]])",
    )
    assert.notEqual(defective, body, '变异没生效（锚点与真源码不匹配）')
    const v = sessionWriteIsAtomic(AUTH.replace(body, defective))
    assert.equal(v.ok, false, '尺子坏了：把缺陷形态喂回去竟然还判绿')
  })
})
