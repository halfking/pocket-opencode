/**
 * base64.ts — Blob ↔ base64 共享工具（无 data: 前缀）。
 * 供 STT 音频上传与会议录音分片落盘共用。
 */
export function blobToBase64(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onloadend = () => {
      const dataUrl = reader.result as string
      resolve(dataUrl.split(',')[1] || '')
    }
    reader.onerror = reject
    reader.readAsDataURL(blob)
  })
}
