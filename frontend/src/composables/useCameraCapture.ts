/**
 * useCameraCapture — @capacitor/camera 封装（统一输入的"拍照 / 相册"入口）。
 *
 * 原生端走 Capacitor Camera 插件（真机原生相机/相册）；Web 端插件自动
 * 降级为 file input 体验。返回 dataUrl 交给 useAttachments 统一校验。
 *
 * 插件不可用（极老 WebView / 插件加载失败）时回退到隐藏 file input，
 * 由调用方触发 click。
 */
import { Capacitor } from '@capacitor/core'

export type CameraPickSource = 'camera' | 'photos'

export function useCameraCapture() {
  async function pickImage(source: CameraPickSource): Promise<{ dataUrl: string; name: string } | null> {
    try {
      const { Camera, CameraResultType, CameraSource } = await import('@capacitor/camera')
      const photo = await Camera.getPhoto({
        resultType: CameraResultType.DataUrl,
        source: source === 'camera' ? CameraSource.Camera : CameraSource.Photos,
        quality: 80,
        width: 1600,
        correctOrientation: true,
      })
      if (!photo.dataUrl) return null
      const ext = photo.format ? `.${photo.format}` : ''
      return { dataUrl: photo.dataUrl, name: `${source === 'camera' ? '拍照' : '相册'}${ext}` }
    } catch (err) {
      // 用户取消（"cancelled"）静默；真实错误上抛由调用方提示。
      const msg = err instanceof Error ? err.message : String(err)
      if (/cancel/i.test(msg)) return null
      throw err
    }
  }

  const isNative = Capacitor.isNativePlatform()

  return { pickImage, isNative }
}
