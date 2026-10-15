/**
 * capabilities 的真实探测测试。
 *
 * 重点不是「报 false」，而是**证明探测器能区分「真有的」与「没有的」**。
 * 一个永远返回 false 的探测器同样会让所有断言变绿——那是没有牙的门禁。
 * 所以这里用 `defaultProbes()` 跑真实运行时：
 *   - 浏览器里对**真实存在**的 Capacitor 插件（Camera）也应报 false，
 *     因为 defaultProbes 显式要求在壳内（web shim 不算原生实现）；
 *   - 注入假探针时，声称有的插件必须让能力升为 true。
 *
 * Run: node --test src/lib/shell/__tests__/capabilities-detect.test.mjs
 */
import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import {
  CAPABILITY_PLUGINS,
  NOT_IMPLEMENTED_PLUGINS,
  defaultProbes,
  detectCapabilities,
  requireCapability,
} from '../capabilities.ts'

test('defaultProbes 在浏览器（无 Capacitor）时对所有插件报 false，且不抛错', () => {
  const p = defaultProbes()
  // node 环境没有 window.Capacitor；探测器必须优雅降级
  assert.equal(p.inShell(), false)
  assert.equal(p.platform(), 'web')
  for (const name of ['BackgroundMic', 'Sherpa', 'Camera', 'TaskLedger', 'LocalAgent']) {
    assert.equal(p.pluginAvailable(name), false, `${name} 在无壳环境必须报 false`)
  }
})

test('defaultProbes 不因插件名含引号/特殊字符而抛错（防注入式探针名）', () => {
  const p = defaultProbes()
  for (const name of ['', "'; DROP TABLE --", '__proto__', 'toString', 'constructor']) {
    assert.doesNotThrow(() => p.pluginAvailable(name), `探针名 ${JSON.stringify(name)} 不得抛错`)
  }
})

test('注册了真实存在的插件时能力升为 true（证明探测器不是恒 false）', () => {
  const present = new Set(['BackgroundMic', 'Sherpa'])
  const caps = detectCapabilities({
    inShell: () => true,
    platform: () => 'android',
    pluginAvailable: (n) => present.has(n),
  })
  assert.equal(caps.recording.available, true, 'BackgroundMic 在册 → 可录音')
  assert.equal(caps.recording.background, true)
  assert.equal(caps.tasks.durableLocal, false, 'TaskLedger 不在册 → 不得声称有持久任务')
  assert.equal(caps.agent.available, false, 'LocalAgent 不在册 → 不得声称有 Agent')
  assert.equal(requireCapability(caps, 'recording').ok, true)
  assert.equal(requireCapability(caps, 'agent').ok, false)
})

// §179：这条是本文件最重要的一条。Sherpa 在 MainActivity 里 registerPlugin 过，
// 所以真机上 isPluginAvailable('Sherpa') 确实返回 true；而 SherpaPlugin.java
// 的每个 @PluginMethod 都是 call.reject。⇒ 只按「注册了没」判定会报
// recognition.asr='model'（本地 ASR 可用），而调用 100% 失败。
test('★ §179 已注册但未实现的插件不得让能力升为 true（Sherpa 是骨架）', () => {
  const caps = detectCapabilities({
    inShell: () => true,
    platform: () => 'android',
    // 探针说 Sherpa 可用 —— 这正是真机的读数
    pluginAvailable: (n) => n === 'Sherpa' || n === 'BackgroundMic',
  })
  assert.equal(
    caps.recognition.asr,
    'none',
    'Sherpa 虽已注册，但每个方法都 reject ⇒ 不得报"本地 ASR 可用"',
  )
  assert.equal(caps.recognition.ocr, 'none', '同一道判定，ocr 也不该跟着升')

  // ★ 负控：把 Sherpa 从「未实现」表里去掉，上面这条断言必须转红。
  // 证明它真的在咬，而不是恒真。
  const saved = { ...NOT_IMPLEMENTED_PLUGINS }
  delete NOT_IMPLEMENTED_PLUGINS['Sherpa']
  try {
    const leaked = detectCapabilities({
      inShell: () => true,
      platform: () => 'android',
      pluginAvailable: (n) => n === 'Sherpa',
    })
    assert.equal(
      leaked.recognition.asr,
      'model',
      '负控：移除登记后 asr 变 model ⇒ 说明上面那条断言确实由本表把守',
    )
  } finally {
    for (const k of Object.keys(NOT_IMPLEMENTED_PLUGINS)) delete NOT_IMPLEMENTED_PLUGINS[k]
    Object.assign(NOT_IMPLEMENTED_PLUGINS, saved)
  }
})

test('★ 负控：不在「未实现」表里的插件仍然能正常把能力升为 true', () => {
  // 若 anyPlugin 被改成「一律 false」，本条必须转红 —— 保证那道筛子是有缺口的，
  // 不是把所有插件都杀掉。
  const caps = detectCapabilities({
    inShell: () => true,
    platform: () => 'android',
    pluginAvailable: (n) => n === 'BackgroundMic',
  })
  assert.equal(caps.recording.available, true, 'BackgroundMic 不是骨架，必须能升为 true')
})

test('⚠️ TaskLedger / LocalAgent 在本仓并不存在 —— 能力清单必须如实登记', () => {
  // 这条守的是「别把规划中的插件写进清单当成已有的」。
  // 有人若把 CAPABILITY_PLUGINS.taskLedger 改成一个假想名字并让探测报 true，
  // 界面就会渲染出一个必然 reject 的入口。
  assert.deepEqual([...CAPABILITY_PLUGINS.taskLedger], ['TaskLedger'])
  assert.deepEqual([...CAPABILITY_PLUGINS.agent], ['LocalAgent'])
  // 真实存在的自研插件名。
  assert.ok(CAPABILITY_PLUGINS.recording.includes('BackgroundMic'), 'BackgroundMic 必须在录音能力清单里')
  // ⚠️ 注意：Sherpa **确实**是真实注册的插件，但它同时是**骨架**（方法全 reject），
  // 所以「在能力清单里」≠「会被算作可用」—— 筛子在 NOT_IMPLEMENTED_PLUGINS。
  assert.ok(CAPABILITY_PLUGINS.recognition.includes('Sherpa'))
  assert.ok('Sherpa' in NOT_IMPLEMENTED_PLUGINS, 'Sherpa 必须同时登记为「已注册但未实现」')
  // 登记表的每一条都要给出可排查的理由 + 一段可校验的指纹
  for (const [name, entry] of Object.entries(NOT_IMPLEMENTED_PLUGINS)) {
    assert.ok(entry && typeof entry === 'object', `${name} 的登记项必须是对象`)
    assert.ok(entry.why && entry.why.length > 10, `${name} 的未实现理由为空或过短`)
    assert.ok(
      entry.fingerprint && entry.fingerprint.length > 8,
      `${name} 缺少可校验指纹（门靠它判断骨架是否还在）`,
    )
  }
})

test('【变异自测】探测器恒返回 false 时，本文件的正向断言会转红', () => {
  const never = detectCapabilities({
    inShell: () => true,
    platform: () => 'android',
    pluginAvailable: () => false,
  })
  // 恒 false 时能力必须全是 false —— 这正是「恒 false 探测器」的可观测特征。
  assert.equal(never.recording.available, false)
  assert.equal(never.recognition.asr, 'none')
  // 而上面「注册了真实存在的插件」那条用例会因注入 present 集合而变红。
  // 本自测确保两份断言真的在描述不同状态，而不是同一条恒真。
  const some = detectCapabilities({
    inShell: () => true,
    platform: () => 'android',
    pluginAvailable: (n) => n === 'BackgroundMic',
  })
  assert.notEqual(some.recording.available, never.recording.available)
})

test('defaultProbes 真的去问 Capacitor，而不是写死 false', () => {
  // ⚠️ 没有这条，「把 isPluginAvailable 的返回改成 false &&」这种变异在
  // node 环境里是**完全不可见**的——因为 node 里根本没有 globalThis.Capacitor，
  // 探测器无论如何都返回 false，断言照样全绿。
  // 所以这里必须**注入一个假 Capacitor**，逼探测器真的去读它。
  const saved = globalThis.Capacitor
  globalThis.Capacitor = {
    isNativePlatform: () => true,
    getPlatform: () => 'android',
    isPluginAvailable: (name) => name === 'BackgroundMic' || name === 'Camera',
  }
  try {
    const p = defaultProbes()
    assert.equal(p.inShell(), true, '应读到注入的 isNativePlatform')
    assert.equal(p.platform(), 'android', '应读到注入的 getPlatform')
    assert.equal(p.pluginAvailable('BackgroundMic'), true, '真实存在的插件必须报 true')
    assert.equal(p.pluginAvailable('Camera'), true, '另一个真实插件也必须报 true')
    assert.equal(p.pluginAvailable('TaskLedger'), false, '不存在的插件必须报 false')

    // 端到端：能力快照应随之升为「可录音」
    const caps = detectCapabilities(p)
    assert.equal(caps.recording.available, true)
    assert.equal(caps.recording.background, true)
  } finally {
    if (saved === undefined) delete globalThis.Capacitor
    else globalThis.Capacitor = saved
  }
})

test('在壳内但插件注册表为空时，仍不得声称有原生能力', () => {
  const saved = globalThis.Capacitor
  globalThis.Capacitor = {
    isNativePlatform: () => true,
    getPlatform: () => 'ios',
    isPluginAvailable: () => false,
  }
  try {
    const caps = detectCapabilities(defaultProbes())
    assert.equal(caps.recording.available, false)
    assert.equal(caps.tasks.durableLocal, false)
    assert.equal(caps.tasks.continuation, 'foregroundOnly')
  } finally {
    if (saved === undefined) delete globalThis.Capacitor
    else globalThis.Capacitor = saved
  }
})
