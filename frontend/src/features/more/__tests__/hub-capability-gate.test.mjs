// hub-capability-gate.test.mjs
//
// 密码箱入口的能力门控（2026-10-03）。
//
// 缺陷：/vault 在 9 宫格里是无条件的一等公民入口，和「对话 / 邮箱」平级，
// 但 Android 原生侧**没有** KeystorePlugin.java，StubKeystore 的 12 个方法
// 全部 reject —— 也就是说点进去必然只看到「功能不可用」。
// 仓库自己在 native/keystore.ts:144 就定过原则：「宁可少显示一个入口，
// 也不要给一个必然失败的操作」。这次让入口遵守它。
//
// 本判据跑的是**真实决策函数** applyCapabilityGates（从 MoreHubView 抽出来的
// 纯函数），不是对着 helper 断言「helper 被调用了」——那种护栏在实现退化成
// 什么都不筛的时候照样绿。
//
// 负控（见文件末尾，必须逐字节还原源文件）：
//   把 MoreHubView 的门控去掉 → 「目录里的 /vault 受门控」转红。

import assert from 'node:assert/strict'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

import { applyCapabilityGates } from '../hubItems.ts'
import { readVueCode } from '../../../__tests__/source-scan.mjs'

const here = dirname(fileURLToPath(import.meta.url))
// 必须用剥掉注释的代码：MoreHubView 的说明注释里就写着 keystore_v1
// （解释「为什么不用这个静态开关」）。拿原文来判，判据会把自己的说明
// 当成「实现用了静态开关」的证据 —— 第一版就是这么红的。
const moreView = readVueCode(join(here, '..', 'MoreHubView.vue')).code

/** 只取 <template> 段：判断「渲染的是什么」必须看模板，不能看脚本。 */
function templateOf(code) {
  const start = code.indexOf('<template>')
  const end = code.indexOf('<script setup')
  assert.ok(start !== -1 && end > start, '没能切出 template 段')
  return code.slice(start, end)
}

/** 从 MoreHubView 的目录字面量里取出真实存在的 route（而不是我手抄一份）。 */
function catalogRoutes() {
  const block = moreView.match(/const mainFeatures = computed<HubItem\[\]>\(\(\) => \[([\s\S]*?)\n\]\)/)
  assert.ok(block, '没能从 MoreHubView.vue 里解析出 mainFeatures 目录字面量')
  return [...block[1].matchAll(/to:\s*'([^']+)'/g)].map((m) => m[1])
}

const CATALOG = catalogRoutes()
const GATED = '/vault'

const item = (to) => ({ to, icon: 'lock', label: to })

describe('hub capability gate', () => {
  it('防空跑：目录确实扫到了东西，且 /vault 真的在里面', () => {
    // 没有这条，下面两条会「因为集合里没有 /vault」而绿 ——
    // 而那正是「门控悄悄不起作用」时的样子。
    assert.ok(CATALOG.length >= 8, '只扫到 ' + CATALOG.length + ' 个目录项，路径/正则可能不对')
    assert.ok(CATALOG.includes(GATED), '目录里已经没有 ' + GATED + '，门控将无事可做')
  })

  it('探针说能用 → 入口保留', () => {
    const out = applyCapabilityGates(CATALOG.map(item), { [GATED]: true })
    assert.ok(out.some((i) => i.to === GATED), '探针为 true 时 /vault 被筛掉了')
  })

  it('探针说不能用 → 入口撤掉', () => {
    const out = applyCapabilityGates(CATALOG.map(item), { [GATED]: false })
    assert.ok(!out.some((i) => i.to === GATED), '探针为 false 时 /vault 仍然显示')
  })

  it('探针还没回来（null）→ 按不可用处理，先藏后现', () => {
    // 这条最容易写错成 `gate !== false`：那样首帧会先把一个必然失败的
    // 入口亮出来再撤掉，用户可能刚好点中它。
    const out = applyCapabilityGates(CATALOG.map(item), { [GATED]: null })
    assert.ok(!out.some((i) => i.to === GATED), 'null 被当成了可用，首帧会闪出死入口')
  })

  it('不受门控的入口一个都不能少', () => {
    const ungated = CATALOG.filter((t) => t !== GATED)
    assert.ok(ungated.length >= 8, '可供对照的未门控入口太少')
    const out = applyCapabilityGates(CATALOG.map(item), { [GATED]: false })
    for (const to of ungated) {
      assert.ok(out.some((i) => i.to === to), '未门控的 ' + to + ' 被误筛掉了')
    }
    assert.equal(out.length, ungated.length)
  })

  it('目录里的 /vault 确实接上了门控，而且门控后的列表真的被渲染', () => {
    // 结构性对照：上面 4 条证明函数对，这一条证明**有人在用它、且模板消费的是门控后那份**。
    // 两者缺一不可 —— 只测函数 = 装饰性护栏。
    //
    // 必须分开断言「调用点」和「模板消费点」：只查 'reachableMainFeatures' 出现过
    // 是不够的 —— computed 的**定义处**本身就满足它，把 v-for 退回 mainFeatures
    // （门控算了但没渲染）照样绿。模板段要单独切出来看。
    assert.match(
      moreView,
      /applyCapabilityGates\(\s*mainFeatures\.value/,
      'MoreHubView 没有把目录喂给 applyCapabilityGates',
    )
    assert.match(
      templateOf(moreView),
      /v-for="item in reachableMainFeatures"/,
      '模板渲染的不是门控后的列表 —— 门控算了但没接上',
    )
  })

  it('门控问的是真实能力，不是那个恒 false 的静态开关', () => {
    // security.keystore_v1 默认 false 且 serverOverrideable=false。
    // 用它 gate 入口的话，插件落地那天还得有人记得回来打开开关。
    assert.ok(
      moreView.includes('isKeystoreAvailable'),
      'MoreHubView 没有调用真实能力探针 isKeystoreAvailable',
    )
    assert.ok(
      !moreView.includes('keystore_v1'),
      'MoreHubView 改用了静态 feature flag；插件落地后入口不会自己回来',
    )
  })
})
