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
