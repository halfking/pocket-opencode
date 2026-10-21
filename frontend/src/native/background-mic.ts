/**
 * BackgroundMic — Android 前台麦克风服务桥。
 * Web / 无插件时全部方法降级为空，调用方继续走 getUserMedia。
 */
import { Capacitor, registerPlugin as capRegisterPlugin } from '@capacitor/core'
import type { AudioInput } from './audio-inputs'
import { classifyAudioLabel, rankAudioInputs } from './audio-inputs'
import { readNativeEpoch } from '../features/meetings/native-part-timeline'

export interface NativeMicDevice {
  deviceId: string
  label: string
  kind?: string
}

interface BackgroundMicPlugin {
  listInputs(): Promise<{ devices: NativeMicDevice[] }>
  start(opts: { meetingId: string; deviceId?: string }): Promise<void>
  stop(): Promise<void>
  addListener(
    event: 'partReady' | 'error',
    cb: (data: Record<string, unknown>) => void,
  ): Promise<{ remove: () => Promise<void> }>
}

let _plugin: BackgroundMicPlugin | null = null
let _loading: Promise<void> | null = null

async function ensureLoaded(): Promise<void> {
  if (_plugin !== null || !Capacitor.isNativePlatform()) return
  if (!_loading) {
    _loading = (async () => {
      try {
        _plugin = (capRegisterPlugin as <T>(name: string) => T)('BackgroundMic')
      } catch {
        _plugin = null
      }
    })()
  }
  await _loading
}

export function isBackgroundMicSupported(): boolean {
  return Capacitor.isNativePlatform()
}

export async function listNativeMicInputs(): Promise<AudioInput[]> {
  await ensureLoaded()
  if (!_plugin) return []
  try {
    const res = await _plugin.listInputs()
    return rankAudioInputs((res.devices ?? []).map((d) => ({
      deviceId: d.deviceId,
      label: d.label || classifyAudioLabel(d.label),
      kind: 'audioinput',
    })))
  } catch {
    return []
  }
}

export interface BackgroundMicStartResult {
  ok: boolean
  /** 失败原因（原生插件 reject 的原文）。给用户看，不要吞掉。 */
  reason?: string
}

/**
 * 拉起 Android 前台录音服务。
 *
 * 原生侧（BackgroundMicPlugin.java）**不会**在 startForegroundService 之后立刻
 * resolve：它要等服务真的开始采音才 resolve，失败则 reject（权限未授予、麦克风被
 * 占用、startForeground 抛 SecurityException…）。所以这里的 reject 是**有信息量的**，
 * 必须带回调用方——静默退回 getUserMedia 的代价是「切到后台才发现录不了」。
 */
export async function startBackgroundMic(opts: { meetingId: string; deviceId?: string }): Promise<BackgroundMicStartResult> {
  await ensureLoaded()
  if (!_plugin) return { ok: false, reason: '后台录音插件未注册' }
  try {
    await _plugin.start(opts)
    return { ok: true }
  } catch (e) {
    // e 是 unknown，必须收窄后才能取 message（vue-tsc 会拦）。
    return { ok: false, reason: String((e as Error)?.message ?? e) }
  }
}

export async function stopBackgroundMic(): Promise<void> {
  await ensureLoaded()
  if (!_plugin) return
  try { await _plugin.stop() } catch { /* already stopped */ }
}

export interface NativePart {
  seq: number
  mimeType: string
  dataBase64: string
  startMs: number
  endMs: number
  /**
   * §956：**本轮**采集原点的绝对墙钟（毫秒）—— 可选。
   *
   * ⚠⚠ **`startMs/endMs` 是「轮内相对量」**：原生服务每启动一轮（中途切设备
   *   就会重启一轮）就把原点重取一次 ⇒ 相对量**从 0 重新开始** ⇒ 直接拿它当
   *   会话坐标会**时间轴回卷**（后半场被排到前半场之前、且相邻段重叠 ⇒
   *   去重的精确裁剪档被打开 ⇒ 正文可能被吃掉）。
   * ⇒ 这个 epoch 就是**把各轮拼回同一根轴**所需的那一个事实。
   *
   * ⚠ **可选是刻意的**：旧原生包不带它 ⇒ 消费侧走「钳位保单调」的降级路
   *   （时间轴是近似的，但**不倒退**）。
   */
  epochMs?: number
}

/**
 * 订阅原生分片（`partReady`）。
 *
 * ⚠⚠ **这个 API 看着是跨平台的，但两端今天都不是「同一条路」（§446，2026-10-11 实读 + 修正 §444）**：
 *
 * | 平台 | 原生侧实况 | 本函数会不会触发 |
 * |---|---|---|
 * | Android | `BackgroundMicPlugin.java`（+ `MeetingRecordService.java`），`@CapacitorPlugin` 正常注册 | ✅ 会 emit `partReady` |
 * | **iOS** | `BackgroundMicPlugin.swift` **存在但未注册** —— Capacitor 8 的 `CapacitorBridge.swift:6` 写死 `typealias CapacitorPlugin = CAPPlugin & CAPBridgedPlugin`，而本仓三个 Swift 插件**只继承 `CAPPlugin`、没有 `CAPBridgedPlugin`**，`ios/App` 下也没有任何 `.m`/`CAP_PLUGIN` 宏 | ⛔ 永不触发（`start()` 先 reject） |
 *
 * ⇒ **iOS 上 `startBackgroundMic()` 返回 `{ok:false}` ⇒ `nativeMode = false` ⇒ Web VAD 那条分支会接管**
 * （所以「iOS 一个 asr 段都没有」是**错的**，VAD 会建段 —— 那是 §444 的误判，已在 §446 收回）。
 *
 * ⚠⚠ **真正的 iOS 后果是双写**：VAD 建 `origin:'asr'` 段的同时，
 * `startLiveCaption()`（本文件无关的另一路，`recordingRuntime.ts:372`）仍在无条件建
 * `origin:'manual'` 段 ⇒ **同一段音频被两个引擎各转写一遍**（已登记 **#21**）。
 * 两个引擎的产出本就不同 ⇒ 覆盖率达不到 0.6 ⇒ **去重消解不了**，两遍都进段表、都送 LLM。
 *
 * ⚠ `background-mic-contract.test.mjs` 的 18 条用例**全部是 Android 的**
 * （`NATIVE_DIR` 只指 `android/...`，全文 0 次 ios/swift）
 * ⇒ 「iOS 插件未注册」这件事至今没有任何门守着。
 *
 * 已登记 **#22**（iOS 原生录音插件未注册 ⇒ 后台录音能力在 iOS 上缺失）。
 * 本函数**暂不改**：修法属产品决定。
 */
export async function listenBackgroundMicParts(
  onPart: (part: NativePart) => void,
  onError?: (msg: string) => void,
): Promise<() => void> {
  await ensureLoaded()
  if (!_plugin) return () => {}
  const handles: Array<{ remove: () => Promise<void> }> = []
  handles.push(await _plugin.addListener('partReady', (data) => {
    // ⚠ §956：缺失/非法一律**不给**（undefined），绝不当成 0（0 = 1970 年的原点）。
    //   解析规则在纯模块里（`readNativeEpoch`）⇒ 那条「不得折成 0」是**行为**判据。
    const epochMs = readNativeEpoch(data.epochMs)
    // ⚠⚠ 刻意**用类型标注构造载荷**（而不是对象字面量 + 展开）：
    //   这样「载荷形状」由**类型层**守着 —— 少一个字段 / 多一个字段
    //   ⇒ **编译期**就红（§955 的教训：类型层能兜住的，别让扫描重复守；
    //   类型层兜不住的（值错、语义错）才另配行为判据）。
    const part: NativePart = {
      seq: Number(data.seq ?? 0),
      mimeType: String(data.mimeType ?? 'audio/wav'),
      dataBase64: String(data.dataBase64 ?? ''),
      startMs: Number(data.startMs ?? 0),
      endMs: Number(data.endMs ?? 0),
    }
    // ⚠ 缺失的 epoch **不写进载荷**（而不是写 undefined）⇒ 消费侧能区分
    //   「没有原点」与「原点是 0」。
    if (epochMs !== undefined) part.epochMs = epochMs
    onPart(part)
  }))
  handles.push(await _plugin.addListener('error', (data) => {
    onError?.(String(data.message ?? '录音失败'))
  }))
  return () => { void Promise.all(handles.map((h) => h.remove())) }
}

export function nativePartToBlob(part: NativePart): Blob {
  const bin = atob(part.dataBase64)
  const buf = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) buf[i] = bin.charCodeAt(i)
  return new Blob([buf], { type: part.mimeType || 'audio/wav' })
}
