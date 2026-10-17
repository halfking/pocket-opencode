/**
 * meeting-audio.ts — 会议录音 IndexedDB 持久化
 *
 * blob URL 在 App 重启后失效；完整录音存入 IndexedDB 以便回放。
 * Sprint 4 可迁移至 @capacitor/filesystem。
 */

const DB_NAME = 'pocket-meeting-audio'
const STORE = 'recordings'
const DB_VERSION = 1

function openDB(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const req = indexedDB.open(DB_NAME, DB_VERSION)
    req.onupgradeneeded = () => {
      const db = req.result
      if (!db.objectStoreNames.contains(STORE)) {
        db.createObjectStore(STORE)
      }
    }
    req.onsuccess = () => resolve(req.result)
    req.onerror = () => reject(req.error)
  })
}

/** 保存会议完整录音 */
export async function saveMeetingAudio(meetingId: string, blob: Blob): Promise<void> {
  const db = await openDB()
  await new Promise<void>((resolve, reject) => {
    const tx = db.transaction(STORE, 'readwrite')
    tx.objectStore(STORE).put(blob, meetingId)
    tx.oncomplete = () => resolve()
    tx.onerror = () => reject(tx.error)
  })
  db.close()
}

/** 读取录音并返回 blob URL（调用方负责 revokeObjectURL） */
export async function loadMeetingAudio(meetingId: string): Promise<string | null> {
  const db = await openDB()
  const blob = await new Promise<Blob | null>((resolve, reject) => {
    const tx = db.transaction(STORE, 'readonly')
    const req = tx.objectStore(STORE).get(meetingId)
    req.onsuccess = () => resolve((req.result as Blob) ?? null)
    req.onerror = () => reject(req.error)
  })
  db.close()
  if (!blob) return null
  return URL.createObjectURL(blob)
}

/** 删除会议录音 */
export async function deleteMeetingAudio(meetingId: string): Promise<void> {
  const db = await openDB()
  await new Promise<void>((resolve, reject) => {
    const tx = db.transaction(STORE, 'readwrite')
    tx.objectStore(STORE).delete(meetingId)
    tx.oncomplete = () => resolve()
    tx.onerror = () => reject(tx.error)
  })
  db.close()
}

/** blob URL → base64（供 STT 云端上传） */
export async function blobUrlToBase64(url: string, mimeType = 'audio/webm'): Promise<string> {
  const res = await fetch(url)
  const blob = await res.blob()
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => {
      const dataUrl = reader.result as string
      resolve(dataUrl.startsWith('data:') ? dataUrl : `data:${mimeType};base64,${dataUrl}`)
    }
    reader.onerror = () => reject(reader.error)
    reader.readAsDataURL(blob)
  })
}

// ─────────────────────────────────────────────────────────────────────────
/** §820：落盘失败时「该往库里写什么」与「该说什么」的**纯决策**。 */
export interface PersistedAudioDecision {
  /**
   * 写进会议记录的 `audioPath`。
   *
   * ⚠⚠ **落盘失败时必须是 `null`**，哪怕本次会话里的 objectURL 还能播：
   * blob URL 只活在**当前文档的内存**里，App 重启 / 刷新后必然失效
   * （本文件头注释第一句就在说这件事）。
   * ⇒ 写进去 = 对用户谎报「录音还在」；而 `loadMeetingAudio` 之后会返回 null
   * ⇒ 整段重转永久不可能，只剩分段文本，且没人知道为什么。
   */
  audioPath: string | null
  /** 该不该出声（null = 无事发生，不产生噪声）。 */
  warning: string | null
}

/**
 * §820：决定「录音落盘结果」怎么反映到会议记录与日志上。
 *
 * 修之前的代码在 catch 里写了一句「ok」就吞掉异常，然后**照样**把 objectURL 写进记录并标记
 * `status: 'completed'` ⇒ 落盘失败在用户侧完全看不出来。
 *
 * ⚠ 本函数**只观察，不干预主流程**：落盘失败不会阻断收尾（录音本身还在内存里，
 *   本次会话内仍可回放），也不改变 `status`。它只保证**不再写进一个假的 audioPath**。
 */
export function resolvePersistedAudio(opts: {
  /** 本次会话拿到的 objectURL（落盘成功时才有持久意义）。 */
  objectUrl: string
  /** 是否真的写进了 IndexedDB。 */
  persisted: boolean
  /** 落盘失败时的原始异常。 */
  error?: unknown
}): PersistedAudioDecision {
  if (opts.persisted) return { audioPath: opts.objectUrl, warning: null }
  const why = opts.error instanceof Error ? opts.error.message : String(opts.error ?? '未知原因')
  return {
    audioPath: null,
    warning:
      '会议录音落盘失败：完整音频未能存进 IndexedDB'
      + `（${why}）。本次会话内仍可回放，但刷新/重启后音频将不存在，`
      + '且整段高精度重转将永久不可用（只能沿用分段文本）。'
      + '常见成因：IndexedDB 配额不足。',
  }
}
