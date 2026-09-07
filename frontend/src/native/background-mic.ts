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

export async function startBackgroundMic(opts: { meetingId: string; deviceId?: string }): Promise<boolean> {
  await ensureLoaded()
  if (!_plugin) return false
  try {
    await _plugin.start(opts)
    return true
  } catch {
    return false
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
