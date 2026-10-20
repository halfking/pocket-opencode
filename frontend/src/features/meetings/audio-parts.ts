/**
 * 会议音频分片落盘。崩溃后可凭 local_meeting_audio_parts 恢复转写。
 * 原生优先写 files/recordings/{meetingId}/part-N.webm；Web 退回 base64。
 */
import { Capacitor } from '@capacitor/core'
import { Filesystem, Directory } from '@capacitor/filesystem'
import { localDB } from '../../native/local-db'
// §888：原来这里自己有一份 blobToBase64，而它的失败路径是
//   `String(reader.result || '')` → `''` → `i < 0` → **resolve('空串')**（而不是 reject）
// ⇒ 原生平台会写出一个 **0 字节的 webm**、库里 data_base64 存空串、
//   `return id` 仍报成功 ⇒ 那一段录音**永久丢失且无痕迹**。
//   而 ingest-speech.ts:52 的 catch（它即是唯一的诊断线索）**永远不会触发**。
// ⇒ 收编到公共实现（它对 null/type 异常、onerror、onabort 都 reject，
//   并透传真实的 reader.error 而不是 ProgressEvent）。
import { blobToBase64 } from '../../utils/base64.ts'

export interface AudioPartRow {
  id: string
  meetingId: string
  seq: number
  mimeType: string
  dataBase64: string
  filePath: string | null
  createdAt: number
}

export async function saveAudioPart(
  meetingId: string,
  seq: number,
  blob: Blob,
  mimeType = blob.type || 'audio/webm',
): Promise<string> {
  const id = `part-${meetingId}-${seq}`
  const dataBase64 = await blobToBase64(blob)
  let filePath: string | null = null
  if (Capacitor.isNativePlatform()) {
    try {
      const dir = `recordings/${meetingId}`
      const path = `${dir}/part-${seq}.webm`
      await Filesystem.mkdir({ path: dir, directory: Directory.Data, recursive: true }).catch(() => {
        // mkdir 失败不致命：紧接着的 writeFile 会自己抛出去，被下面的 catch 收成
        // filePath = null（改为把 base64 内联存库）⇒ 这里的失败不会消失。
      })
      await Filesystem.writeFile({
        path,
        data: dataBase64,
        directory: Directory.Data,
      })
      const uri = await Filesystem.getUri({ path, directory: Directory.Data })
      filePath = uri.uri
    } catch {
      filePath = null
    }
  }
  await localDB.run(
    `INSERT OR REPLACE INTO local_meeting_audio_parts
     (id, meeting_id, seq, mime_type, data_base64, file_path, created_at)
     VALUES (?,?,?,?,?,?,?)`,
    [id, meetingId, seq, mimeType, filePath ? '' : dataBase64, filePath, Date.now()],
  )
  return id
}

export async function listAudioParts(meetingId: string): Promise<AudioPartRow[]> {
  const rows = await localDB.query<Record<string, unknown>>(
    'SELECT * FROM local_meeting_audio_parts WHERE meeting_id = ? ORDER BY seq',
    [meetingId],
  )
  return rows.map((r) => ({
    id: r.id as string,
    meetingId: r.meeting_id as string,
    seq: r.seq as number,
    mimeType: r.mime_type as string,
    dataBase64: (r.data_base64 as string) || '',
    filePath: (r.file_path as string) || null,
    createdAt: r.created_at as number,
  }))
}
