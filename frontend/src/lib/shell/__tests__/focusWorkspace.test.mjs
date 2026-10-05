/**
 * focusWorkspace + capabilities 契约测试（UI规范 07 §9 / 08 §1、§3）。
 *
 * 两条主张：
 *   1. 进出会**精确归还**锁与快照；任一步失败仍清理剩余的锁。
 *   2. 形态开关（compact / 在壳内）**不等于**能力；探测不到就是 false，
 *      「不确定」不等于「可能可以」。
 */
import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import { FocusWorkspace, shouldBlockBackgroundEvent } from '../focusWorkspace.ts'
import { detectCapabilities, requireCapability } from '../capabilities.ts'

/** 可观测的假 DOM 适配器：记录调用顺序，便于断言「先存后锁」。 */
function makeAdapter(over = {}) {
  const calls = []
  return {
    calls,
    setBackgroundInert: (on) => calls.push(`inert:${on}`),
    setFocusableTrap: (r) => calls.push(`trap:${r ? 'set' : 'null'}`),
    lockScrollHost: (el) => calls.push(`lock:${el.id}`),
    unlockScrollHost: (el, s) => calls.push(`unlock:${el.id}:${s.overflow}`),
    saveScroll: (id) => {
      calls.push(`save:${id}`)
      return { x: 0, y: 120 }
    },
    restoreScroll: (id, v) => calls.push(`restore:${id}:${v.y}`),
    lockBackgroundInteractions: (on) => calls.push(`bg:${on}`),
    ...over,
  }
}

function host(id, overflow = '', position = '') {
  const el = { id, style: { overflow, position } }
  return el
}

test('进入顺序：先存快照再锁背景（反了就读不到滚动位置）', () => {
  const dom = makeAdapter()
  const h = host('main', 'auto', 'relative')
  h.dataset = { focusScrollId: 'main' }
  const ws = new FocusWorkspace({ dom, scrollHosts: () => [h] })
  ws.enter()
  const saveIdx = dom.calls.indexOf('save:main')
  const lockIdx = dom.calls.indexOf('lock:main')
  assert.ok(saveIdx >= 0 && lockIdx >= 0)
  assert.ok(saveIdx < lockIdx, `必须先存后锁，实际顺序=${dom.calls.join(' → ')}`)
  assert.equal(ws.current, 'focused')
})

test('退出：锁全部归还，且**准确恢复原 inline style**（不粗暴置空）', () => {
  const dom = makeAdapter()
  const h = host('main', 'auto', 'relative')
  h.dataset = { focusScrollId: 'main' }
  const ws = new FocusWorkspace({ dom, scrollHosts: () => [h] })
  ws.enter()
  dom.calls.length = 0
  ws.exit()
  assert.ok(dom.calls.includes('unlock:main:auto'), '必须恢复 overflow 原值')
  assert.ok(dom.calls.includes('restore:main:120'), '必须归还滚动位置')
  assert.ok(dom.calls.includes('inert:false'))
  assert.ok(dom.calls.includes('bg:false'))
  assert.equal(ws.current, 'normal')
})

test('重复 enter 不叠加锁', () => {
  const dom = makeAdapter()
  const h = host('main')
  h.dataset = { focusScrollId: 'main' }
  const ws = new FocusWorkspace({ dom, scrollHosts: () => [h] })
  ws.enter()
  ws.enter()
  assert.equal(dom.calls.filter((c) => c === 'lock:main').length, 1, '不得锁两次')
  ws.exit()
})

test('任一步清理抛错，仍把状态归位（半解锁比不解锁更糟）', () => {
  let boom = false
  const dom = makeAdapter({
    lockBackgroundInteractions: (on) => {
      if (boom) throw new Error('unlock failed')
    },
  })
  const h = host('main')
  h.dataset = { focusScrollId: 'main' }
  const ws = new FocusWorkspace({ dom, scrollHosts: () => [h] })
  ws.enter()
  boom = true
  assert.throws(() => ws.exit(), /unlock failed/)
  assert.equal(ws.current, 'normal', '即使解锁抛错也必须归位，否则再也进不去')
})

test('子确认框引用计数：popChild 到 0 才允许真正退出', () => {
  const ws = new FocusWorkspace({ dom: makeAdapter(), scrollHosts: () => [] })
  ws.enter()
  ws.pushChild()
  ws.pushChild()
  assert.equal(ws.childDepth, 2)
  assert.equal(ws.popChild(), false, '还有一个子层')
  assert.equal(ws.popChild(), true, '归零')
})

test('forceExit 在未进入时是 no-op（不抛错）', () => {
  const ws = new FocusWorkspace({ dom: makeAdapter(), scrollHosts: () => [] })
  ws.forceExit()
  assert.equal(ws.current, 'normal')
})

test('背景事件拦截：专注层自身与子层一律放行', () => {
  const focusRoot = { contains: (n) => n === 'child' }
  assert.equal(shouldBlockBackgroundEvent('focused', 'background-el', focusRoot), true)
  assert.equal(shouldBlockBackgroundEvent('focused', 'child', focusRoot), false, '子确认框不得被屏蔽')
  assert.equal(shouldBlockBackgroundEvent('normal', 'background-el', focusRoot), false, '未进入时不拦')
})

test('能力探测：探测不到就是 false，形态开关不算能力', () => {
  // 在壳内 + compact 宽度，但没有任何原生插件注册。
  const caps = detectCapabilities({
    inShell: () => true,
    platform: () => 'android',
    pluginAvailable: () => false,
  })
  assert.equal(caps.recording.available, false, '在壳内不等于可录音')
  assert.equal(caps.recording.background, false)
  assert.equal(caps.tasks.durableLocal, false)
  assert.equal(caps.tasks.continuation, 'foregroundOnly')
  assert.equal(caps.recognition.ocr, 'none')
  assert.equal(caps.agent.available, false)
  assert.equal(caps.agent.skillFormat, 'none')
  // 导航/专注是 Web 侧运行时，不依赖原生。
  assert.equal(caps.navigation, true)
  assert.equal(caps.focusWorkspace, true)
})

test('能力探测：插件真的注册了才升级（前后台是两件事）', () => {
  const caps = detectCapabilities({
    inShell: () => true,
    platform: () => 'ios',
    pluginAvailable: (n) => n === 'BackgroundMic',
  })
  assert.equal(caps.recording.available, true)
  assert.equal(caps.recording.background, true)
  // 没装 TaskLedger 就不能声称可调度本地持久任务。
  assert.equal(caps.tasks.durableLocal, false)
})

test('能力门返回原因，便于 UI 给对应文案', () => {
  const caps = detectCapabilities({
    inShell: () => false,
    platform: () => 'web',
    pluginAvailable: () => false,
  })
  const rec = requireCapability(caps, 'recording')
  assert.equal(rec.ok, false)
  assert.equal(rec.reason, 'unsupported')
  assert.match(rec.detail, /窄屏不等于可录音/)

  const tasks = requireCapability(caps, 'durableTasks')
  assert.equal(tasks.ok, false)
  assert.equal(tasks.reason, 'unavailable')
  assert.match(tasks.detail, /foregroundOnly/)

  // PDF 文本提取纯 Web 可做，不该被 native 能力门挡住。
  assert.equal(requireCapability(caps, 'agent').ok, false)
})
