/**
 * download 编码 helpers（Phase 9.4 抽出单测覆盖）。
 *
 * 抽离原因：
 *   - utils/download.ts 顶层 import pocket-native，node ESM 跑单测时会触发
 *     '@capacitor/core' 模块解析（即便 ESM 解析顺序允许），不利于纯函数契约测试。
 *   - 抽到独立模块，download.ts 引它（带 .ts 后缀）；单测文件引它（带 .ts 后缀），
 *     不经过 pocket-native 的 cascade import。
 *
 * 契约：
 *   - utf8ToBase64: UTF-8 安全（中文/emoji 不抛 InvalidCharacterError）
 *   - blobToBase64: 纯 base64（去除 data:... 前缀，与 PocketFilesystem.writeFile 对齐）
 *   - arrayBufferToBase64: chunk 拆分 0x8000 防止 btoa 超参
 */

/** UTF-8 安全 base64 编码（避免 btoa 对中文字符抛 InvalidCharacterError）。 */
export function utf8ToBase64(s: string): string {
  return btoa(unescape(encodeURIComponent(s)))
}

export function blobToBase64(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onloadend = () => {
      const dataUrl = reader.result as string
      // 去除 data:...;base64, 前缀，PocketFilesystem.writeFile 只接受纯 base64
      const base64 = dataUrl.split(',')[1] ?? ''
      if (!base64) {
        reject(new Error('文件编码失败'))
        return
      }
      resolve(base64)
    }
    reader.onerror = () => reject(reader.error ?? new Error('文件读取失败'))
    reader.readAsDataURL(blob)
  })
}

export function arrayBufferToBase64(buffer: ArrayBuffer): string {
  const bytes = new Uint8Array(buffer)
  let binary = ''
  const chunk = 0x8000 // 分块拼接，避免 String.fromCharCode 超参
  for (let i = 0; i < bytes.length; i += chunk) {
    binary += String.fromCharCode(...bytes.subarray(i, i + chunk))
  }
  return btoa(binary)
}