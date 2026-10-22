/**
 * 后台录音（Android 前台服务）源码级契约测试 —— 2026-10-01。
 *
 * 需求：「整个录音应该是可后台运行的，app 切换到后台也不能停止。」
 *
 * 为什么只能做源码级契约测试：这套逻辑一半在 Java（前台服务），一半在 TS
 * （Capacitor 桥）。真机行为无法在本机验证（无 adb / 无模拟器镜像），
 * 所以这里锁住的是**最容易被静默回退的三个失效点**：
 *
 *  1. `start()` 不能在 startForegroundService 之后立刻 resolve——服务若当场崩
 *     （权限未授予 → startForeground 抛 SecurityException；麦克风被占用），
 *     JS 会误以为后台录音可用，表现为「显示正在录音、一整场没有声音」。
 *  2. 启动失败必须撤通知 + 结束服务，否则留下一个永不消失的「正在录音」空壳。
 *  3. START_STICKY 被系统重启时 intent 为 null，此时不能开录（幽灵录音）。
 *
 * 这些断言是**读源码文本**的负向契约：把对应的 Java/TS 代码改回旧写法，
 * 断言就会红。已在交付前逐条做过负控对照。
 */
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'
import { stripVueComments } from '../../__tests__/source-scan.mjs'

const HERE = dirname(fileURLToPath(import.meta.url))
const NATIVE_DIR = resolve(HERE, '../../../android/app/src/main/java/com/kaixuan/opencode/pocket/plugins')
const read = (rel) => readFileSync(join(HERE, rel), 'utf8')
const readJava = (name) => readFileSync(join(NATIVE_DIR, name), 'utf8')

const plugin = readJava('BackgroundMicPlugin.java')
const service = readJava('MeetingRecordService.java')
const runtime = read('../recordingRuntime.ts')
const bridge = read('../background-mic.ts')
const manifest = readFileSync(
  resolve(HERE, '../../../android/app/src/main/AndroidManifest.xml'), 'utf8')

describe('Android 前台录音：插件名两端对齐', () => {
  it('TS 注册名与 Java @CapacitorPlugin 名都是 BackgroundMic', () => {
    assert.match(plugin, /@CapacitorPlugin\(name = "BackgroundMic"\)/)
    // 桥里实际写法是 (capRegisterPlugin as ...)(...)('BackgroundMic')，
    // 所以断言的是「'BackgroundMic' 作为调用实参出现」，而不是找某个变量名。
    assert.match(bridge, /\(\s*'BackgroundMic'\s*\)/)
  })

  it('manifest 声明的服务类与 Intent 里用的一致', () => {
    assert.match(manifest, /android:name="\.plugins\.MeetingRecordService"/)
    assert.match(plugin, /new Intent\(getContext\(\), MeetingRecordService\.class\)/)
  })
})

describe('Android 前台录音：start() 必须等到真的采到音', () => {
  it('插件侧不出现「调完 startForegroundService 立刻 resolve」的写法', () => {
    // startForegroundService 之后不允许紧跟 call.resolve()。
    const idx = plugin.indexOf('startForegroundService(i)')
    assert.ok(idx > 0, '应存在 startForegroundService 调用')
    const after = plugin.slice(idx, idx + 400)
    assert.ok(
      !/startForegroundService\(i\)[\s\S]{0,200}?call\.resolve\(\)/.test(after),
      'startForegroundService 之后不得直接 resolve，必须等 reportStart 回报',
    )
  })

  it('插件先查 RECORD_AUDIO 权限，缺失时明确 reject', () => {
    assert.match(plugin, /Manifest\.permission\.RECORD_AUDIO/)
    assert.match(plugin, /checkSelfPermission/)
    assert.match(plugin, /麦克风权限未授予/)
  })

  it('插件有启动超时兜底，服务崩溃也不会让 JS 无限等待', () => {
    assert.match(plugin, /START_TIMEOUT_MS/)
    assert.match(plugin, /postDelayed/)
    assert.match(plugin, /未在[\s\S]{0,40}秒内回报启动结果/)
  })

  it('服务只在 rec.startRecording() 成功之后才回报启动成功', () => {
    const startIdx = service.indexOf('rec.startRecording()')
    const reportIdx = service.indexOf('reportStart(true')
    assert.ok(startIdx > 0, '应存在 startRecording 调用')
    assert.ok(reportIdx > 0, '应存在 reportStart(true) 回报')
    assert.ok(reportIdx > startIdx, '回报成功必须排在 startRecording 之后')
  })

  it('打不开麦克风 / 启动异常都走 failAndQuit，而不是默默返回', () => {
    assert.match(service, /failAndQuit\("无法打开麦克风/)
    assert.match(service, /catch \(Exception e\)[\s\S]{0,200}?failAndQuit\("麦克风启动失败/)
  })
})

describe('Android 前台录音：失败不留空壳', () => {
  it('failAndQuit 会撤下前台通知并结束服务', () => {
    assert.match(service, /private void failAndQuit\([\s\S]*?stopForeground\(true\)[\s\S]*?stopSelf\(\)/)
  })

  it('startForeground 抛异常时被接住并回报失败', () => {
    assert.match(service, /try \{\s*startForegroundCompat\(\);[\s\S]*?catch \(Exception e\)[\s\S]*?reportStart\(false/)
  })
})

describe('Android 前台录音：START_STICKY 重启不得产生幽灵录音', () => {
  it('intent 为 null 时停止服务而不是开录', () => {
    assert.match(
      service,
      /if \(intent == null\) \{[\s\S]{0,300}?stopSelf\(\)[\s\S]{0,80}?return START_NOT_STICKY/,
    )
  })
})

describe('Android 14+ 前后台录音合规前置条件', () => {
  it('服务声明 microphone 前台服务类型', () => {
    assert.match(manifest, /MeetingRecordService[\s\S]*?foregroundServiceType="microphone"/)
  })

  it('声明 FOREGROUND_SERVICE 与 FOREGROUND_SERVICE_MICROPHONE（Android 14+ 必需）', () => {
    assert.match(manifest, /android\.permission\.FOREGROUND_SERVICE"/)
    assert.match(manifest, /android\.permission\.FOREGROUND_SERVICE_MICROPHONE/)
  })

  it('声明 RECORD_AUDIO 与 POST_NOTIFICATIONS（常驻通知需要）', () => {
    assert.match(manifest, /android\.permission\.RECORD_AUDIO/)
    assert.match(manifest, /android\.permission\.POST_NOTIFICATIONS/)
  })

  it('Android 10+ 用带类型的三参 startForeground', () => {
    assert.match(service, /ServiceInfo\.FOREGROUND_SERVICE_TYPE_MICROPHONE/)
  })
})

describe('前端：后台录音不可用时原因必须可见', () => {
  it('桥接层不再把 reject 原因吞成 false', () => {
    assert.match(bridge, /BackgroundMicStartResult/)
    assert.ok(
      !/catch \{\s*return false\s*\}/.test(bridge),
      'startBackgroundMic 不得用空 catch 把失败原因丢掉',
    )
    // 必须锚定 catch 分支本身。只断言「文件里出现过 reason:」是不够的——
    // 负控实测过：把 catch 里的 reason 删掉，另一行
    // `return { ok: false, reason: '后台录音插件未注册' }` 仍能让断言全绿。
    assert.match(bridge, /catch \(e\) \{[\s\S]{0,120}?return \{ ok: false, reason: String\(/)
  })

  it('runtime 记录后台录音失败原因', () => {
    assert.match(runtime, /backgroundMicReason/)
  })

  it('切后台的提示里带上真实原因', () => {
    assert.match(runtime, /backgroundMicReason\.value[\s\S]{0,200}?toast\.error/)
  })

  it('只有非 nativeMode 才挂 visibilitychange 告警（native 模式本就该继续录）', () => {
    // 顺序不能反：先判 nativeMode 返回，再注册监听。
    assert.match(
      runtime,
      /if \(document\.visibilityState !== 'hidden'[\s\S]{0,120}?this\.nativeMode\) return/,
    )
  })
})

// ══════════════════════════════════════════════════════════════════════════
// §956 · 原生分片必须带上**本轮采集原点**（epochMs）
//
// ── 为什么 ──
//   `MeetingRecordService.loop()` 的 `captureStart` 是**每轮重取**的
//   ⇒ 原生服务每重启一轮（中途切设备就会重启一轮），轮内相对量
//     `startMs/endMs` 就**从 0 重新开始** ⇒ 直接拿它当分段时间范围
//     ⇒ **时间轴回卷** ⇒ 后半场被排到前半场之前 + 相邻段重叠
//     ⇒ 去重的**精确裁剪档**（§634/§683：能吃掉正文）被打开。
//
// ⇒ 修法：原生**如实上报**本轮原点（epochMs），由 JS 侧把各轮
//   **精确**拼回同一根会话时间轴（`features/meetings/native-part-timeline.ts`）。
//
// ⚠⚠ 扫描前**必须剥注释**（共用底座 `src/__tests__/source-scan.mjs`）：
//   本节在 Java/TS 里写了大量提到 `epochMs` 的说明文字 ⇒ 不剥的话
//   下面每一条断言都会命中**注释**而不是代码 ⇒ 全绿而毫无意义。
// ══════════════════════════════════════════════════════════════════════════

describe('§956 · 原生分片必须带上本轮采集原点（epochMs）', () => {
  const pluginCode = stripVueComments(plugin)
  const serviceCode = stripVueComments(service)
  const bridgeCode = stripVueComments(bridge)

  it('★ 载荷里必须有 epochMs（各轮的时间轴靠它拼回同一根轴）', () => {
    assert.equal(
      (pluginCode.match(/data\.put\("epochMs", epochMs\)/g) ?? []).length, 1,
      'emitPart 的载荷里没有 epochMs ⇒ 换轮次后时间轴拼不回去（回卷照旧）',
    )
  })

  it('★ 旧字段必须原样保留（向后兼容：旧 JS 只看 startMs/endMs）', () => {
    assert.match(pluginCode, /data\.put\("startMs", startMs\)/)
    assert.match(pluginCode, /data\.put\("endMs", endMs\)/)
  })

  it('★ emitPart 必须带 epoch 实参', () => {
    assert.match(
      pluginCode,
      /static void emitPart\(int seq, byte\[\] wav, long startMs, long endMs, long epochMs\)/,
    )
  })

  it('★ 服务侧传的 epoch 必须就是算相对时间用的那个原点（不许另取一个钟）', () => {
    const calls = [...serviceCode.matchAll(/emitPart\(([^)]*)\)/g)]
      .map((m) => m[1].split(',').map((s) => s.trim()))
    assert.equal(calls.length, 2, '原生服务应当有两处 emitPart（定时切片 + 收尾补片）')
    const origins = [...serviceCode.matchAll(/System\.currentTimeMillis\(\)\s*-\s*(\w+)/g)]
      .map((m) => m[1])
    assert.ok(origins.length >= 2, '找不到轮内相对量的计算')
    assert.equal(new Set(origins).size, 1, '轮内相对量必须全部以同一个原点计算')
    for (const args of calls) {
      assert.equal(args.length, 5, 'emitPart 必须带 epoch（第 5 个实参）')
      assert.equal(args[4], origins[0], 'emitPart 传的 epoch 必须就是算相对时间用的那个原点')
    }
  })

  it('★ 桥侧不得把缺失的 epoch 折成 0（0 = 1970 年 ⇒ 偏移是天文数字）', () => {
    assert.ok(!/epochMs\s*\?\?\s*0/.test(bridgeCode), '缺失的 epoch 不得被折成 0')
    assert.ok(!/epochMs:\s*0\b/.test(bridgeCode), '缺失的 epoch 不得被折成 0')
    // ⚠ 解析规则**只在纯模块里有一份**（`readNativeEpoch`）⇒ 桥侧必须调它。
    //   自己再写一份 ⇒ 两处会漂（而漂的方向通常是「一边开始折成 0 了」）。
    // ⇒ 这条断言只是「接线」；解析规则本身是**行为**判据（见纯模块那组用例）。
    assert.match(bridgeCode, /readNativeEpoch\(/,
      '桥侧必须用纯模块里那一个解析实现（自己再写一份 ⇒ 两处会漂）')
  })

  it('★ 桥侧必须把 epoch 转发给消费侧（解析完就扔 ⇒ 时间轴照样回卷）', () => {
    assert.match(bridgeCode, /\.epochMs\s*=/, '载荷里没有 epoch ⇒ 消费侧拿不到原点')
    assert.match(bridgeCode, /epochMs !== undefined/, '缺失的 epoch 不得写进载荷')
  })
})

// ══════════════════════════════════════════════════════════════════════════
// §957 · 同一轮里的原生分片必须**首尾相接**
//
// ── 为什么这条是 §957 的地基 ──
//   §957 要把「切换空隙」（轮次之间那段**没有音频**的墙钟）与「丢了一片」
//   （同一轮里时间戳跳了一段）**分开** —— 否则护栏会把真丢音频当成空隙放过去。
//   ⇒ 能分开的前提是：**同一轮里的分片本来就是连续的**
//     （`MeetingRecordService.loop()` 每发一片就把 `partStartRel` 推进到 `nowRel`）
//   ⇒ 于是「同一轮里出现跳跃」只可能是「丢了一片」，而不会是「原生侧留了缝」。
//
// ⚠⚠ 扫描前**必须剥注释**（共用底座 `src/__tests__/source-scan.mjs`）：
//   本节在 Java 里写了大量提到 `partStartRel` / `nowRel` 的说明文字
//   ⇒ 不剥的话下面每一条断言都会命中**注释**而不是代码。
// ══════════════════════════════════════════════════════════════════════════

describe('§957 · 同一轮里的原生分片必须首尾相接', () => {
  const serviceCode = stripVueComments(service)

  it('★ 每发完一片必须把 partStartRel 推进到 nowRel（片与片之间不留缝）', () => {
    // ⚠ 锚定「赋值出现在 emitPart **之后**」—— 顺序反了就是「先推进再发」，
    //   那样这一片的起点会落在**下一段**音频上（时间戳与音频错位）。
    // ⚠ 末尾的 `;` 是刻意的：`partStartRel = nowRel + 100`（留缝）不该被这条放过。
    assert.match(
      serviceCode,
      /emitPart\([^)]*\);[\s\S]{0,200}?partStartRel\s*=\s*nowRel\s*;/,
      '发片之后没有把 partStartRel 推进到 nowRel ⇒ 同一轮里的片不再连续 ⇒ 「丢片」与「空隙」分不开了',
    )
  })

  it('★ partStartRel 的初值必须是 0（第一片从原点开始）', () => {
    assert.match(serviceCode, /long partStartRel = 0;/,
      '第一片不从 0 开始 ⇒ 轮内相对量的原点对不上')
  })

  it('★ captureStart 必须**每轮只取一次**（取两次 ⇒ 时间轴又回卷）', () => {
    const n = (serviceCode.match(/captureStart\s*=\s*System\.currentTimeMillis\(\)/g) ?? []).length
    assert.equal(n, 1, `captureStart 被取了 ${n} 次 ⇒ 每轮必须只取一次（否则 §956 的重基失效）`)
  })

  it('★ 两处 emit（定时切片 + 收尾补片）都必须带**本轮**原点', () => {
    const calls = [...serviceCode.matchAll(/emitPart\(([^)]*)\)/g)].map((m) => m[1])
    assert.equal(calls.length, 2, '原生服务应当有两处 emitPart（定时切片 + 收尾补片）')
    for (const args of calls) {
      const parts = args.split(',').map((s) => s.trim())
      assert.equal(parts.length, 5, 'emitPart 必须带 epoch（第 5 个实参）')
      assert.equal(parts[4], 'captureStart',
        '两处 emit 都必须带本轮原点（否则最后一片落回另一根轴 ⇒ 空隙算错）')
    }
  })
})
