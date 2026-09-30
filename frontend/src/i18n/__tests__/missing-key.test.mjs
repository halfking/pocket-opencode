// BUG-AO 回归锁：缺 key 必须告警，且同一个 key 重复出现只告警一次。
//
// 反证设计：先断言「去重生效」——若实现漏了去重，warnedKeys 的长度会是
// key 调用次数而不是唯一 key 数，这条断言就会失败。
// 判据要求在有缺陷一侧失败过：BUG-AO 修复前 i18n/index.ts 根本没有 missing 钩子，
// 缺 key 完全静默，本文件的「首次必告警」断言直接失败。
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { onMissingKey, resetMissingWarnState, warnedKeys } from '../missing-key.ts'

test('缺 key 首次出现必须打一条告警（BUG-AO：此前完全静默）', () => {
  resetMissingWarnState()
  const seen = []
  const realWarn = console.warn
  console.warn = (...args) => seen.push(args.join(' '))
  try {
    onMissingKey('zh-CN', 'study.due.allClear')
  } finally {
    console.warn = realWarn
  }
  assert.equal(seen.length, 1, '缺 key 应恰好打一条告警')
  assert.match(seen[0], /study\.due\.allClear/, '告警里必须含缺失的 key')
  assert.match(seen[0], /zh-CN/, '告警里必须含当前语言')
})

test('同一个 key 重复出现只告警一次（渲染循环不能刷屏）', () => {
  resetMissingWarnState()
  let n = 0
  const realWarn = console.warn
  console.warn = () => { n++ }
  try {
    for (let i = 0; i < 50; i++) onMissingKey('zh-CN', 'study.inbox.title')
  } finally {
    console.warn = realWarn
  }
  assert.equal(n, 1, `50 次调用只应告警 1 次，实际 ${n} 次`)
  assert.deepEqual(warnedKeys(), ['zh-CN:study.inbox.title'])
})

test('不同语言的同一个 key 分别告警（去重键必须含语言）', () => {
  resetMissingWarnState()
  const realWarn = console.warn
  console.warn = () => {}
  try {
    onMissingKey('zh-CN', 'nav.flashcards')
    onMissingKey('ja-JP', 'nav.flashcards')
  } finally {
    console.warn = realWarn
  }
  assert.equal(warnedKeys().length, 2)
  assert.ok(warnedKeys().includes('ja-JP:nav.flashcards'))
})

test('返回值仍是 key 本身——不改变用户看到的文案', () => {
  resetMissingWarnState()
  const realWarn = console.warn
  console.warn = () => {}
  try {
    // 「界面上出现可疑字符串」这个信号必须保留：换成中性占位符会让漏 key 更难被发现
    assert.equal(onMissingKey('zh-CN', 'study.reminder.next'), 'study.reminder.next')
  } finally {
    console.warn = realWarn
  }
})
