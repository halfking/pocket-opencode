/**
 * BackgroundMic — Android 前台麦克风服务桥。
 * Web / 无插件时全部方法降级为空，调用方继续走 getUserMedia。
 */
import { Capacitor, registerPlugin as capRegisterPlugin } from '@capacitor/core'
import type { AudioInput } from './audio-inputs'
import { classifyAudioLabel, rankAudioInputs } from './audio-inputs'

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
}

export async function listenBackgroundMicParts(
  onPart: (part: NativePart) => void,
  onError?: (msg: string) => void,
): Promise<() => void> {
  await ensureLoaded()
  if (!_plugin) return () => {}
  const handles: Array<{ remove: () => Promise<void> }> = []
  handles.push(await _plugin.addListener('partReady', (data) => {
    onPart({
      seq: Number(data.seq ?? 0),
      mimeType: String(data.mimeType ?? 'audio/wav'),
      dataBase64: String(data.dataBase64 ?? ''),
      startMs: Number(data.startMs ?? 0),
      endMs: Number(data.endMs ?? 0),
    })
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
