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
const read = (rel: string) => readFileSync(join(SRC, rel), 'utf8')

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
