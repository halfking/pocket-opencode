/**
 * flashcardMedia —— 卡片媒体（图片 / 音频）落地工具。
 *
 * Phase 9 重构：从直接 import Capacitor 切换到 pocket-native 抽象接口。
 *  - 业务代码不再 import '@capacitor/camera' / '@capacitor/filesystem'
 *  - 跨 Android / iOS / Web 由 pocket-native 工厂分发
 *  - 与原始 Capacitor 调用行为等价（保留 MediaRef 形状）
 *
 * 存储：PocketCamera 调起相机/相册 → PocketFilesystem 把图片落 app 沙盒目录
 * `flashcards/`。文件名 = 时间戳 + 随机后缀（sha1 去重 Phase 9.1）。
 *
 * 引用：note 上保存 `mediaRefs: { role, fileName, mime }[]`；渲染时通过
 * `loadMediaDataUrl()` 转 data URL 给 <img>/<audio>。
 *
 * Phase 9 简化：仅图片。音频（Recorder → audio ref）走 PocketRecorder，Phase 9.2。
 */
import { getPocketNative, type PocketPlatform } from '../../../native/pocket-native'

const MEDIA_DIR = 'flashcards'

/** Note 上挂载的媒体引用（轻量：只记文件名 + role + mime）。 */
export interface MediaRef {
  /** 'front' | 'back' | 'cloze' —— 描述媒体挂载在哪一面。 */
  role: 'front' | 'back' | 'cloze'
  /** PocketFilesystem 下的文件名（不含路径）。 */
  fileName: string
  /** MIME type（'image/jpeg' | 'image/png' | 'audio/mpeg' ...）。 */
  mime: string
}

/**
 * 调起相机 / 相册，落地图片到 flashcards/，返回 MediaRef。
 *
 * Phase 9 路径：
 *  - Android → PocketCamera（@capacitor/camera 兼容层）
 *  - iOS     → Phase 7.1 接通 Swift plugin 后可用；当前 stub 抛错
 *  - Web     → <input type=file> 文件选择器 + FileReader 转 base64
 *
 * 失败抛错（Error.message 含 i18n key 'flashcards.edit.imagePickFailed'
 * 的语义信息），调用方 catch 后给用户 Toast。
 */
export async function pickAndSaveImage(opts: {
  role: MediaRef['role']
  source?: 'camera' | 'gallery'
}): Promise<MediaRef> {
  const native = getPocketNative()
  const source = opts.source ?? 'camera'

  const base64Mime = await captureBase64(native.platform, source)
  if (!base64Mime.base64) {
    throw new Error('flashcards.edit.imagePickFailed: no image data')
  }
  const mime = base64Mime.mime
  const ext = mime === 'image/png' ? 'png' : 'jpg'
  const fileName = `${Date.now()}-${Math.random().toString(36).slice(2, 8)}.${ext}`
  await writeFile(fileName, base64Mime.base64)
  return { role: opts.role, fileName, mime }
}

/** 从 PocketFilesystem 路径读 base64，转 data URL 给 <img :src>。 */
export async function loadMediaDataUrl(fileName: string): Promise<string | null> {
  const native = getPocketNative()
  if (native.platform === 'web') {
    // Web：图片 base64 内联在 note 上（不在文件系统）；返回 null 让 caller
    // 走 imageRefs 透传路径（Phase 9.1）。
    return null
  }
  try {
    const { Filesystem, Directory } = await import('@capacitor/filesystem')
    const result = await Filesystem.readFile({
      path: `${MEDIA_DIR}/${fileName}`,
      directory: Directory.Data,
    })
    if (typeof result.data !== 'string') return null
    return result.data.startsWith('data:')
      ? result.data
      : `data:image/jpeg;base64,${result.data}`
  } catch {
    return null
  }
}

/** 删除媒体（编辑替换 / 删除卡片时清理）。 */
export async function deleteMediaFile(fileName: string): Promise<void> {
  const native = getPocketNative()
  if (native.platform === 'web') return
  try {
    const { Filesystem, Directory } = await import('@capacitor/filesystem')
    await Filesystem.deleteFile({
      path: `${MEDIA_DIR}/${fileName}`,
      directory: Directory.Data,
    })
  } catch {
    /* 文件已不存在 = 忽略 */
  }
}

/* ===== 内部 helpers ===== */

/** 跨平台拿图片 base64 + mime。Web 走 file picker，其它平台各自插件。 */
async function captureBase64(
  platform: PocketPlatform,
  source: 'camera' | 'gallery',
): Promise<{ base64: string; mime: string }> {
  if (platform === 'web') {
    const file = await pickWebFile()
    if (!file) return { base64: '', mime: '' }
    return { base64: await fileToBase64(file), mime: file.type || 'image/jpeg' }
  }
  if (platform === 'ios') {
    // Phase 7.1 接通 Swift plugin 后启用。
    throw new Error('flashcards.edit.imagePickFailed: iOS plugin not yet wired')
  }
  // Android：复用 @capacitor/camera（成熟稳定）
  const { Camera, CameraResultType, CameraSource } = await import('@capacitor/camera')
  const photo = await Camera.getPhoto({
    resultType: CameraResultType.Base64,
    source: source === 'camera' ? CameraSource.Camera : CameraSource.Photos,
    quality: 80,
    allowEditing: false,
  })
  const mime = photo.format === 'png' ? 'image/png' : 'image/jpeg'
  return { base64: photo.base64String ?? '', mime }
}

/**
 * 写文件：直接走 @capacitor/filesystem。
 *
 * Phase 9.1 增量：把 Filesystem.writeFile 也搬到 PocketFilesystem 接口。
 */
async function writeFile(fileName: string, base64: string): Promise<void> {
  const { Filesystem, Directory } = await import('@capacitor/filesystem')
  await Filesystem.writeFile({
    path: `${MEDIA_DIR}/${fileName}`,
    data: base64,
    directory: Directory.Data,
  })
}

/** Web fallback：调起文件选择器。 */
function pickWebFile(): Promise<File | null> {
  return new Promise((resolve) => {
    const input = document.createElement('input')
    input.type = 'file'
    input.accept = 'image/*'
    input.style.display = 'none'
    document.body.appendChild(input)
    input.onchange = () => {
      const file = input.files?.[0] ?? null
      document.body.removeChild(input)
      resolve(file)
    }
    input.click()
  })
}

/** File → base64 string (no data: prefix) */
function fileToBase64(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => {
      const result = reader.result
      if (typeof result !== 'string') {
        reject(new Error('reader did not return string'))
        return
      }
      const idx = result.indexOf(',')
      resolve(idx >= 0 ? result.slice(idx + 1) : result)
    }
    reader.onerror = () => reject(reader.error ?? new Error('reader error'))
    reader.readAsDataURL(file)
  })
}