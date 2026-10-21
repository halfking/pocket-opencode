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
  try {
    await new Promise<void>((resolve, reject) => {
      const tx = db.transaction(STORE, 'readwrite')
      tx.objectStore(STORE).put(blob, meetingId)
      // ⚠ 必须等 `tx.oncomplete`：`put` 的 request 成功**不代表已落库**（见 §877 D46）。
      tx.oncomplete = () => resolve()
      tx.onerror = () => reject(tx.error)
      tx.onabort = () => reject(tx.error ?? new Error('IndexedDB 事务被中止：录音未落盘'))
    })
  } finally {
    // §877 D48：失败路径也必须关连接 —— 旧代码 close() 写在 await 之后，
    // 一旦这里 reject，连接就永久留着。
    db.close()
  }
}

/** 读取录音并返回 blob URL（调用方负责 revokeObjectURL） */
export async function loadMeetingAudio(meetingId: string): Promise<string | null> {
  const db = await openDB()
  let blob: Blob | null
  try {
    blob = await new Promise<Blob | null>((resolve, reject) => {
      const tx = db.transaction(STORE, 'readonly')
      const req = tx.objectStore(STORE).get(meetingId)
      req.onsuccess = () => resolve((req.result as Blob) ?? null)
      req.onerror = () => reject(req.error)
    })
  } finally {
    db.close()
  }
  if (!blob) return null
  return URL.createObjectURL(blob)
}

/** 删除会议录音 */
export async function deleteMeetingAudio(meetingId: string): Promise<void> {
  const db = await openDB()
  try {
    await new Promise<void>((resolve, reject) => {
      const tx = db.transaction(STORE, 'readwrite')
      tx.objectStore(STORE).delete(meetingId)
      tx.oncomplete = () => resolve()
      tx.onerror = () => reject(tx.error)
      tx.onabort = () => reject(tx.error ?? new Error('IndexedDB 事务被中止：录音未被删除'))
    })
  } finally {
    db.close()
  }
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
  /**
   * §826：本次走的是**原生后台录音**（Android 前台服务）。
   *
   * ⚠ 必须在 `cleanupMedia()` **之前**取值 —— 那一步会把 `nativeMode` 置回 false，
   *   而决策发生在它之后 ⇒ 晚一步读到的永远是 false，诊断就成了死代码（且不报错）。
   */
  nativeMode?: boolean
}): PersistedAudioDecision {
  if (opts.persisted) return { audioPath: opts.objectUrl, warning: null }
  // ⚠⚠ 顺序不能反：「压根没有整段音频可存」必须**先于**「存失败」判。
  //   两者的成因不同（原生侧只发分片 vs IndexedDB 写失败），排查的人会去查完全不同的地方。
  if (!opts.error && opts.nativeMode) {
    return {
      audioPath: null,
      warning:
        '本次是原生后台录音：原生侧只产出分片、不产整段文件'
        + '（Android 前台服务用 AudioRecord 直接推 PCM，没有容器封装）'
        // ⚠⚠ §954：原文写的是「整段高精度重转不会进行」——**那句话现在不成立了**：
        //   整段重转改成**从盘上已落的分片流式供给**（`streamNativeMeetingAudioChunks`）
        //   ⇒ 它**会**发生。但「没有可保存的完整录音文件」这件事**仍然成立**
        //   （那一条是 §826 的原始事实，且整条 128 行的 `audioPath` 语义依赖它）。
        + '⇒ 这场会议没有可保存的完整录音文件；'
        + '整段高精度重转改为从盘上已落的分片流式供给（§954），'
        + '分片读不回来时仍只能基于录音期间逐段落库的文本。',
    }
  }
  const why = opts.error instanceof Error ? opts.error.message : String(opts.error ?? '未知原因')
  return {
    audioPath: null,
    warning:
      '会议录音落盘失败：完整音频未能存进 IndexedDB'
      + `（${why}）。刷新/重启后音频将不存在，`
      + '且整段高精度重转将永久不可用（只能沿用分段文本）。'
      + '⚠ 本前端目前**没有任何音频播放界面**（无 <audio>/播放器组件），'
      + 'audioPath 只被 API 序列化、笔记音频徽章与整段重转消费 —— '
      + '所以这次失败对用户的表现是「精校质量降级」，而不是「放不出声」。'
      + '常见成因：IndexedDB 配额不足。',
  }
}
