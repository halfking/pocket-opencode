// applyCapabilityGates 的语义单测。
//
// 为什么值得单独测（不是「覆盖率不够」这种理由）
// ------------------------------------------------
// 这个纯函数决定「更多」页 9 宫格里**哪些入口会出现**。本仓已经在同一个缺陷类别上
// 栽过两次，两次形态不同：
//   ① 录音 FAB 的入口被 `v-if="counts.total"`（**数据**门控）藏起来 —— 空库用户
//      整条语音转写功能不可达（2026-10-03 修复）。
//   ② 这次扫描 19 个入口时，「密码」入口消失 —— 但那一次是**正确**的：
//      hubItems.ts 查的是真实平台能力 isKeystoreAvailable()，
//      而 frontend/android 下**确实没有任何 *Keystore* 文件**（原生侧没有插件，
//      StubKeystore 的方法全是 reject），所以入口本就不该出现。
//
// ①和②的差别不在代码形状，而在**门控问的是不是真问题**。纯函数测不出这一点，
// 但它能钉住三条容易在重构中被改坏的规则：
//   · gates 里没有的 route 一律保留（不能因为 map 少一个键就全筛掉）；
//   · null（探针还没回来）按**不可用**处理，先藏后现；
//   · 只有 gate === true 才留下。
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { applyCapabilityGates } from '../hubItems.ts'

const item = (to) => ({ to, icon: 'lock', label: to })

test('gates 里没有的 route 一律保留 —— 少一个键不等于全都筛掉', () => {
  const items = [item('/ai-chat'), item('/email'), item('/vault')]
  const out = applyCapabilityGates(items, { '/vault': false })
  assert.deepEqual(out.map((i) => i.to), ['/ai-chat', '/email'])
})

test('只有 gate === true 才留下入口', () => {
  const items = [item('/a'), item('/b'), item('/c')]
  const out = applyCapabilityGates(items, { '/a': true, '/b': false, '/c': true })
  assert.deepEqual(out.map((i) => i.to), ['/a', '/c'])
})

test('null（探针还没回来）按不可用处理 —— 先藏后现', () => {
  // 首帧必然是 null。若这里改成「按可用处理」，宫格会先给出一个必然失败的入口
  // 再撤掉，用户可能刚好点中它 —— 那正是 hubItems.ts 注释里明确拒绝的形态。
  const items = [item('/vault')]
  assert.deepEqual(applyCapabilityGates(items, { '/vault': null }), [])
  assert.deepEqual(applyCapabilityGates(items, { '/vault': true }).map((i) => i.to), ['/vault'])
})

test('gates 为空对象时全部保留（不能变成「谁都过不了」的空结果）', () => {
  const items = [item('/a'), item('/b')]
  assert.equal(applyCapabilityGates(items, {}).length, 2)
})
