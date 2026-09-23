/**
 * flashcardMedia —— 卡片媒体（图片 / 音频）落地工具。
 *
 * 存储：Capacitor Filesystem 把每张图片 / 音频落盘到 app documents 目录的
 * `flashcards/` 子目录下；文件名 = 时间戳 + 扩展名（sha1 去重 Phase 6.1）。
 *
 * 引用：note 上保存 `mediaRefs: { role, fileName }[]`，渲染时通过
 * `loadMediaDataUrl()` 转 data URL 给 <img>/<audio>。
 *
 * Web 兜底：浏览器环境（dev / Web 演示）用 `URL.createObjectURL(file)` 直接渲染，
 * 不落盘（刷新即丢）。
 *
 * Phase 6 简化：只支持图片（Camera）；音频（Recorder → audio ref）留 Phase 6.2。
 */
import { Camera, CameraResultType, CameraSource } from '@capacitor/camera'
import { Filesystem, Directory } from '@capacitor/filesystem'
import { Capacitor } from '@capacitor/core'

const MEDIA_DIR = 'flashcards'

/** Note 上挂载的媒体引用（轻量：只记文件名 + role，不重复保存 base64）。 */
export interface MediaRef {
  /** 'front' | 'back' | 'cloze' —— 描述媒体挂载在哪一面。 */
  role: 'front' | 'back' | 'cloze'
  /** Filesystem 下的文件名（不含路径）。 */
  fileName: string
  /** MIME type（'image/jpeg' | 'image/png' | 'audio/mpeg' ...）。 */
  mime: string
}

/** 平台判断：原生壳 vs Web 浏览器。 */
const isNative = (): boolean => typeof Capacitor !== 'undefined' && Capacitor.isNativePlatform()

/**
 * 调起相机 / 相册，落地图片到 flashcards/，返回 MediaRef。
 *
 * 失败抛错；调用方 catch 后给用户 Toast。
 */
export async function pickAndSaveImage(opts: {
  role: MediaRef['role']
  source?: 'camera' | 'gallery'
}): Promise<MediaRef> {
  const source = opts.source ?? 'camera'
  const sourceMap = source === 'camera' ? CameraSource.Camera : CameraSource.Photos

  if (isNative()) {
    const photo = await Camera.getPhoto({
      resultType: CameraResultType.Base64,
      source: sourceMap,
      quality: 80,
      allowEditing: false,
    })
    const base64 = photo.base64String
    if (!base64) throw new Error('no image data')
    const ext = photo.format === 'png' ? 'png' : 'jpg'
    const fileName = `${Date.now()}-${Math.random().toString(36).slice(2, 8)}.${ext}`
    const mime = ext === 'png' ? 'image/png' : 'image/jpeg'
    await Filesystem.writeFile({
      path: `${MEDIA_DIR}/${fileName}`,
      data: base64,
      directory: Directory.Data,
    })
    return { role: opts.role, fileName, mime }
  }
  throw new Error('web picker not implemented in Phase 6; open on native device')
}

/** 从落盘位置读取图片，转 base64 data URL 供 <img :src>。 */
export async function loadMediaDataUrl(fileName: string): Promise<string | null> {
  if (!isNative()) return null
  try {
    const result = await Filesystem.readFile({
      path: `${MEDIA_DIR}/${fileName}`,
      directory: Directory.Data,
    })
    // result.data 可能是 base64 字符串（默认 encoding）或 string data URI
    if (typeof result.data !== 'string') return null
    return result.data.startsWith('data:') ? result.data : `data:image/jpeg;base64,${result.data}`
  } catch {
    return null
  }
}

/** 删除媒体（Phase 6 简化：edit 时替换 / 删除卡片时一并清理）。 */
export async function deleteMediaFile(fileName: string): Promise<void> {
  if (!isNative()) return
  try {
    await Filesystem.deleteFile({
      path: `${MEDIA_DIR}/${fileName}`,
      directory: Directory.Data,
    })
  } catch {
    /* 文件已不存在 = 忽略 */
  }
}