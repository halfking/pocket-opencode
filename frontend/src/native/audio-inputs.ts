/**
 * 音频输入枚举与选优。
 *
 * 多麦克风时：蓝牙/有线耳机优先（近讲更清晰），其次 USB，再内置阵列。
 * 约束请求 2 声道 + 系统 AEC/NS/AGC，让阵列麦走系统波束成形。
 */
export type AudioInputKind = 'bluetooth' | 'headset' | 'usb' | 'builtin' | 'unknown'

export interface AudioInput {
  deviceId: string
  label: string
  kind: AudioInputKind
  rank: number
}

const KIND_RANK: Record<AudioInputKind, number> = {
  bluetooth: 0,
  headset: 1,
  usb: 2,
  builtin: 3,
  unknown: 4,
}

export function classifyAudioLabel(label: string): AudioInputKind {
  const t = label.toLowerCase()
  if (/bluetooth|bt\b|sco|a2dp|airpods|buds/.test(t)) return 'bluetooth'
  if (/headset|headphone|earpiece|wired/.test(t)) return 'headset'
  if (/\busb\b|uac|dongle/.test(t)) return 'usb'
  if (/builtin|built-in|speakerphone|camcorder|default/.test(t)) return 'builtin'
  if (!label.trim() || t === 'default' || t === 'communications') return 'unknown'
  return 'unknown'
}

export function rankAudioInputs(
  devices: Array<{ deviceId: string; label: string; kind?: string }>,
): AudioInput[] {
  return devices
    .filter((d) => !d.kind || d.kind === 'audioinput')
    .filter((d) => d.deviceId && d.deviceId !== 'default')
    .map((d) => {
      const kind = classifyAudioLabel(d.label)
      return { deviceId: d.deviceId, label: d.label || kind, kind, rank: KIND_RANK[kind] }
    })
    .sort((a, b) => a.rank - b.rank || a.label.localeCompare(b.label))
}

export function preferredAudioInput(inputs: AudioInput[]): AudioInput | undefined {
  return inputs[0]
}

export function micTrackConstraints(deviceId?: string): MediaTrackConstraints {
  const base: MediaTrackConstraints = {
    echoCancellation: true,
    noiseSuppression: true,
    autoGainControl: true,
    channelCount: { ideal: 2 },
    sampleRate: { ideal: 16000 },
  }
  if (deviceId) base.deviceId = { exact: deviceId }
  return base
}

export async function listAudioInputs(): Promise<AudioInput[]> {
  if (!navigator.mediaDevices?.enumerateDevices) return []
  const all = await navigator.mediaDevices.enumerateDevices()
  return rankAudioInputs(all.filter((d) => d.kind === 'audioinput'))
}

export async function openMicStream(deviceId?: string): Promise<{
  stream: MediaStream
  inputs: AudioInput[]
  selected?: AudioInput
}> {
  const constraints = { audio: micTrackConstraints(deviceId), video: false }
  const stream = await navigator.mediaDevices.getUserMedia(constraints)
  const inputs = await listAudioInputs()
  const usedId = stream.getAudioTracks()[0]?.getSettings().deviceId
  const selected = inputs.find((i) => i.deviceId === usedId)
    || (deviceId ? inputs.find((i) => i.deviceId === deviceId) : preferredAudioInput(inputs))
  return { stream, inputs, selected }
}

/** 先拿权限拿到设备标签，再切到排序最高的麦（蓝牙/耳机优先）。 */
export async function openPreferredMicStream(deviceId?: string): Promise<{
  stream: MediaStream
  inputs: AudioInput[]
  selected?: AudioInput
}> {
  const first = await openMicStream(deviceId)
  if (deviceId) return first
  const pref = preferredAudioInput(first.inputs)
  if (!pref || !first.selected || pref.deviceId === first.selected.deviceId) return first
  if (pref.rank >= first.selected.rank) return first
  first.stream.getTracks().forEach((t) => t.stop())
  return openMicStream(pref.deviceId)
}

/** 立体声时取能量更高的声道（阵列/左右麦选近讲侧）。 */
export function louderChannelRms(left: Float32Array, right: Float32Array): 'left' | 'right' | 'mono' {
  if (!right || right.length === 0) return 'mono'
  const rms = (buf: Float32Array) => {
    let s = 0
    for (let i = 0; i < buf.length; i++) s += buf[i] * buf[i]
    return Math.sqrt(s / buf.length)
  }
  return rms(right) > rms(left) * 1.08 ? 'right' : 'left'
}
