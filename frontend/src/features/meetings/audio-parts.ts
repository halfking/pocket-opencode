/**
 * 会议音频分片落盘。崩溃后可凭 local_meeting_audio_parts 恢复转写。
 * 原生优先写 files/recordings/{meetingId}/part-N.webm；Web 退回 base64。
 */
import { Capacitor } from '@capacitor/core'
import { Filesystem, Directory } from '@capacitor/filesystem'
import { localDB } from '../../native/local-db'
import { captureRecordingDb, type RecordingDb } from './recording-operation'
import { captureMeetingOwnership, ownerColumnValues, ownerOf } from '../../native/meeting-ownership'
// §888：原来这里自己有一份 blobToBase64，而它的失败路径是
//   `String(reader.result || '')` → `''` → `i < 0` → **resolve('空串')**（而不是 reject）
// ⇒ 原生平台会写出一个 **0 字节的 webm**、库里 data_base64 存空串、
//   `return id` 仍报成功 ⇒ 那一段录音**永久丢失且无痕迹**。
//   而 ingest-speech.ts:52 的 catch（它即是唯一的诊断线索）**永远不会触发**。
// ⇒ 收编到公共实现（它对 null/type 异常、onerror、onabort 都 reject，
//   并透传真实的 reader.error 而不是 ProgressEvent）。
import { blobToBase64 } from '../../utils/base64.ts'
import {
  audioPartDir,
  audioPartExtForMime,
  audioPartRelativePath,
  streamNativeAudioChunkSources,
  type NativeAudioChunkSource,
} from './native-audio-parts.ts'

export interface AudioPartRow {
  id: string
  meetingId: string
  seq: number
  mimeType: string
  dataBase64: string
  filePath: string | null
  createdAt: number
}

/** 音频分片在应用数据目录下的**相对目录**（写入与读回共用同一处 ⇒ 不会写一处读另一处）。 */
export { audioPartDir, audioPartRelativePath, audioPartExtForMime } from './native-audio-parts.ts'

export async function saveAudioPart(
  meetingId: string,
  seq: number,
  blob: Blob,
  mimeType = blob.type || 'audio/webm',
  db: RecordingDb = captureRecordingDb(),
): Promise<string> {
  const id = `part-${meetingId}-${seq}`
  const dataBase64 = await blobToBase64(blob)
  db.assertCurrent()
  // ⚠ 录音分片是**原始音频** ⇒ 归属必须写进行里（不写 = 该分片归属未知，
  //   任何账号都读不到，也永不上传）。
  const [ownerUserId, ownerWorkspaceId] = ownerColumnValues(ownerOf(db))
  let filePath: string | null = null
  if (Capacitor.isNativePlatform()) {
    try {
      const dir = audioPartDir(meetingId)
      const path = audioPartRelativePath(meetingId, seq, audioPartExtForMime(mimeType))
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
  await db.run(
    `INSERT OR REPLACE INTO local_meeting_audio_parts
     (id, meeting_id, seq, mime_type, data_base64, file_path, created_at, owner_user_id, owner_workspace_id)
     VALUES (?,?,?,?,?,?,?,?,?)`,
    [id, meetingId, seq, mimeType, filePath ? '' : dataBase64, filePath, Date.now(), ownerUserId, ownerWorkspaceId],
  )
  return id
}

export async function listAudioParts(meetingId: string): Promise<AudioPartRow[]> {
  const { db, owner } = captureMeetingOwnership(localDB)
  const rows = await db.query<Record<string, unknown>>(
    'SELECT * FROM local_meeting_audio_parts WHERE meeting_id = ? AND owner_user_id = ? AND owner_workspace_id = ? ORDER BY seq',
    [meetingId, ...ownerColumnValues(owner)],
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

/**
 * 一片音频分片的**元信息**（不含字节）。
 */
export interface AudioPartFile {
  seq: number
  mimeType: string
  /** 落盘位置（原生链才有；Web 链把字节内联在库里）。 */
  filePath: string | null
}

/**
 * 只取分片的**元信息**（**不取 `data_base64`**）。
 *
 * ⚠⚠ §954：为什么必须是「只取元信息」——
 *   `data_base64` 这一列在 **Web 链**上装的是**整场音频**（原生链才写文件）
 *   ⇒ `SELECT *` 会把整场音频**一次性**读进内存 ⇒ 峰值 **O(时长)**
 *   ⇒ 而「整场音频的清单」这个需求本身**只需要 seq / mime / 路径**。
 *   ⇒ 这里显式列出三列 ⇒ 清单的内存与会议时长**无关**。
 *   ⚠ 顺带把 `listAudioParts`（`SELECT *`）标成**别用**：它是全仓唯一一处
 *   会整列读音频的地方，而且**没有调用方**（§828 登记：「崩溃后恢复转写」尚无实现）。
 */
export async function listAudioPartFiles(meetingId: string): Promise<AudioPartFile[]> {
  const { db, owner } = captureMeetingOwnership(localDB)
  const rows = await db.query<Record<string, unknown>>(
    'SELECT seq, mime_type, file_path FROM local_meeting_audio_parts WHERE meeting_id = ? AND owner_user_id = ? AND owner_workspace_id = ? ORDER BY seq',
    [meetingId, ...ownerColumnValues(owner)],
  )
  return rows.map((r) => ({
    seq: Number(r.seq ?? 0),
    mimeType: String(r.mime_type ?? ''),
    filePath: (r.file_path as string) || null,
  }))
}

/** base64 → 字节（Capacitor 的 `Filesystem.readFile` 返回的就是 base64）。 */
function base64ToBytes(b64: string): Uint8Array {
  const bin = atob(b64)
  const out = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i)
  return out
}

/**
 * 读回一片**原生**分片的字节。
 *
 * ⚠ 扩展名**依次尝试**：先按 MIME 推出来的（§954 修好之后原生链写 `.wav`），
 *   再试老的 `.webm`（老库里的文件名仍是这样，**内容也是 WAV**）。
 *
 * @returns null = 这片在盘上读不到（调用方据此**中止整条流**，见下面那条注释）。
 */
export async function readAudioPartBytes(
  meetingId: string,
  seq: number,
  mimeType: string,
): Promise<Uint8Array | null> {
  const exts = [audioPartExtForMime(mimeType), 'webm']
  for (const ext of exts) {
    const path = audioPartRelativePath(meetingId, seq, ext)
    try {
      const res = await Filesystem.readFile({ path, directory: Directory.Data })
      // ⚠ 类型上 `data` 是 `string | Blob`：原生实现给的是 base64 字符串，
      //   而 Web 实现可能直接给 Blob ⇒ 两种都要接住。
      if (typeof res.data === 'string') return base64ToBytes(res.data)
      return new Uint8Array(await res.data.arrayBuffer())
    } catch {
      // 试下一个扩展名（老库里的文件仍叫 .webm）
    }
  }
  return null
}

/**
 * §954：把**盘上已有**的原生分片**流式**接成整场音频的若干块。
 *
 * ⚠⚠ **读不到一片就整条中止**（不静默跳过）：
 *   跳过一个读不到的片 ⇒ 那一段音频变成一个**洞** ⇒ 而块的 `offsetSec` 是
 *   **音频位置**（与 Web 链同一口径）⇒ 洞之后的偏移会与**墙钟**时间轴错开
 *   ⇒ 说话人归属会整体错位 ⇒ **比「没有整场重转」更糟**。
 *   ⇒ 宁可整条中止（退回分段文本，与今天的 Android 行为一致），也不要给一份错位的全文。
 *
 * ⚠⚠⚠ **§955：本函数现在只是一层薄适配**（读盘、读库都在这里）；
 *   切块与「块的形状」在 `native-audio-parts.ts` 的 `streamNativeAudioChunkSources` 里
 *   ⇒ 那儿是**纯的**（I/O 注入）⇒ **node 里能直接跑真件**
 *   ⇒ 而此前真件住在本文件里（本文件 import 了 Filesystem 与 localDB ⇒ node 跑不到）
 *   ⇒ 判据只能测**假件** ⇒ 「假件比真件完整」这件事没人查得出来
 *   ⇒ §954 的「护栏在原生链上复活」那句话才会是假的。
 *   ⇒ ⭐ 并且**本函数产出的每一块都带 `durationSec`**（`NativeAudioChunkSource` 里它**必填**）
 *   ⇒ 漏字段**编译期**就红。
 */
export async function* streamNativeMeetingAudioChunks(
  meetingId: string,
  opts?: { chunkSeconds?: number; searchSeconds?: number; minChunkSeconds?: number },
): AsyncGenerator<NativeAudioChunkSource> {
  const files = await listAudioPartFiles(meetingId)
  if (!files.length) return
  yield* streamNativeAudioChunkSources(
    files,
    (seq, mimeType) => readAudioPartBytes(meetingId, seq, mimeType),
    { ...opts, label: `meeting=${meetingId}` },
  )
}
