// permission-denial-outlet.test.ts — §865/D29：权限申请路径必须唯一，且申请后必须看结果。
//
// 缺陷背景（composables/useApprovalAlerts.ts，原文）：
//   async function ensurePermission() {
//     const status = await LocalNotifications.checkPermissions()
//     if (status.display !== 'granted' && !permissionAsked) {
//       permissionAsked = true
//       await LocalNotifications.requestPermissions()     // ⚠⚠ 不看结果
//     }
//   }
//   // setup 里：void ensurePermission()   ← 用户一进任务页就弹系统框，无任何前置说明
//
// ⇒ 三条问题：
//   ① 这是**第二条独立的申请路径**：结果不汇入 `useNotificationPermission` 的共享状态
//      （设置页 `SettingsPermissionsView` 渲染的正是那个状态）；
//   ② **申请完不检查结果** ⇒ 被拒之后「待审批 3 分钟提醒」静默失效，界面零解释；
//   ③ 触发时机是无 rationale 的系统弹窗 —— Android 上被拒率显著更高
//      （共享 composable 本来就有 `prompt-with-rationale` 这一态，这里用不上）。
//
// ⭐ 这与 §859/D24、§861/D25 同族但**换了形态**：那两次是「同一个 key 的第 2/3 个
//   消费者」，这次是「**同一个能力的第 2 套实现**」⇒ 同族的第四种形态。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const HERE = dirname(fileURLToPath(import.meta.url))
const SRC = join(HERE, '..')
const read = (rel: string) => readFileSync(join(SRC, rel), 'utf8')

const strip = (t: string): string =>
  t.replace(/\/\*[\s\S]*?\*\//g, (m) => m.replace(/[^\n]/g, ' '))
   .replace(/(^|[^:])\/\/[^\n]*/g, '$1')

const ALERTS = read('composables/useApprovalAlerts.ts')
const ALERTS_CODE = strip(ALERTS)
const PERM = strip(read('composables/useNotificationPermission.ts'))
const PERM_VIEW = strip(read('features/settings/SettingsPermissionsView.vue'))

describe('§865 · 尺子自证', () => {
  it('★ 三个文件都读到了', () => {
    assert.ok(ALERTS.includes('useApprovalAlerts'), '没读到 useApprovalAlerts')
    assert.ok(PERM.includes('useNotificationPermission'), '没读到 useNotificationPermission')
    assert.ok(PERM_VIEW.includes('useNotificationPermission'), '没读到设置页的权限视图')
  })
})

describe('§865 · D29 申请路径必须唯一（不得自带第二条实现）', () => {
  it('★★★ 审批提醒不得直接调用 requestPermissions（必须走共享 composable）', () => {
    assert.doesNotMatch(ALERTS_CODE, /LocalNotifications\.requestPermissions\s*\(/,
      'useApprovalAlerts 又自带了一条权限申请路径 ⇒ 全仓出现第 2 套实现')
    assert.match(ALERTS_CODE, /useNotificationPermission\(\)/,
      'useApprovalAlerts 没有复用共享的 useNotificationPermission')
  })

  it('★★★ 申请后必须检查结果（否则被拒 = 审批提醒静默失效且零解释）', () => {
    const body = /async function ensurePermission\(\)\s*:\s*Promise<void>\s*\{[\s\S]*?\n  \}/.exec(ALERTS_CODE)?.[0] ?? ''
    assert.ok(body, '找不到 ensurePermission')
    assert.match(body, /await\s+permission\.ensure\(\)/, 'ensurePermission 没有真正发起申请')
    assert.match(body, /!==\s*'granted'|===\s*'granted'/,
      '申请结果没有被检查 ⇒ 被拒之后提醒静默失效')
    assert.match(body, /console\.warn/,
      '申请未成功时没有任何 warn ⇒ 事后无法从日志判断这条路是否已被关掉')
  })

  it('★★ granted 时不得重复申请（Android 重复弹框会被判为滥用）', () => {
    const body = /async function ensurePermission\(\)\s*:\s*Promise<void>\s*\{[\s\S]*?\n  \}/.exec(ALERTS_CODE)?.[0] ?? ''
    const grantedAt = body.indexOf("'granted'")
    assert.ok(grantedAt >= 0)
    // 早退必须出现在真正的申请之前
    assert.ok(body.indexOf('return') < body.indexOf('permission.ensure()'),
      'granted 早退不在申请之前 ⇒ 已授权时仍会反复弹系统框')
  })
})

describe('§865 · 共享 composable 是唯一真值（样板，别把它改退化）', () => {
  it('★★ 共享状态必须同时覆盖 granted / denied / prompt-with-rationale', () => {
    assert.match(PERM, /'prompt-with-rationale'/,
      '共享 composable 不再处理 prompt-with-rationale ⇒ 无 rationale 时会被直接判 denied')
    assert.match(PERM, /canRequestAgain/,
      '共享 composable 不再提供 canRequestAgain ⇒ 设置页无法区分「能再申请」与「必须去系统设置」')
  })

  it('★★ 设置页必须渲染那个共享状态（否则「唯一的申请路径」对用户不可见）', () => {
    assert.match(PERM_VIEW, /useNotificationPermission\(\)/,
      '设置页不再使用共享 composable')
    assert.match(PERM_VIEW, /notif\.(state|label|ensure|recheck)/,
      '设置页没有消费共享状态 ⇒ 用户在设置页看不到通知权限的真实状态')
  })
})

describe('§865 · 变异负控', () => {
  it('把申请改回直连插件 ⇒ 第一组立刻红', () => {
    const mutated = ALERTS_CODE.replace(
      'await permission.ensure()',
      'await LocalNotifications.requestPermissions()')
    assert.notEqual(mutated, ALERTS_CODE, '变异没生效')
    assert.match(mutated, /LocalNotifications\.requestPermissions\s*\(/)
  })

  it('去掉「检查结果」⇒ 第二组立刻红', () => {
    const mutated = ALERTS_CODE.replace(/if \(result !== 'granted'\) \{/, 'if (false) {')
    assert.notEqual(mutated, ALERTS_CODE, '变异没生效')
    assert.doesNotMatch(
      /async function ensurePermission\(\)\s*:\s*Promise<void>\s*\{[\s\S]*?\n  \}/.exec(mutated)?.[0] ?? '',
      /!==\s*'granted'/,
    )
  })
})
