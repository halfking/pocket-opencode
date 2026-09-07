/**
 * 会议音频分片落盘。崩溃后可凭 local_meeting_audio_parts 恢复转写。
 * 原生优先写 files/recordings/{meetingId}/part-N.webm；Web 退回 base64。
 */
import { Capacitor } from '@capacitor/core'
import { Filesystem, Directory } from '@capacitor/filesystem'
import { localDB } from '../../native/local-db'

export interface AudioPartRow {
  id: string
  meetingId: string
  seq: number
  mimeType: string
  dataBase64: string
  filePath: string | null
  createdAt: number
}

async function blobToBase64(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => {
      const s = String(reader.result || '')
      const i = s.indexOf(',')
      resolve(i >= 0 ? s.slice(i + 1) : s)
    }
    reader.onerror = () => reject(reader.error)
    reader.readAsDataURL(blob)
  })
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
      await Filesystem.mkdir({ path: dir, directory: Directory.Data, recursive: true }).catch(() => {})
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
