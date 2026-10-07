/**
 * base64.ts — Blob ↔ base64 共享工具（无 data: 前缀）。
 * 供 STT 音频上传与会议录音分片落盘共用。
 */
export function blobToBase64(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onloadend = () => {
      const dataUrl = reader.result
      // reader.result 有三种可能：data URL 字符串 / null / ArrayBuffer。
      // 原写法是 `reader.result as string` 直接 .split —— result 为 null 时抛
      // TypeError: Cannot read properties of null (reading 'split')。
      //
      // ★ 这个 TypeError 发生在事件处理器里，**不会**转成 promise 的 reject：
      //   异常会变成 uncaught error 飘到 console，而 await 永久挂起。
      //   所以调用方看到的 reject 实际来自下面的 onerror，值是一个 ProgressEvent，
      //   真实原因（reader.error 里的 DOMException）在日志里根本看不到 ——
      //   页面只显示「保存失败，请稍后重试」。
      //   2026-10-06 真机复现（Redmi 2411DRN47C，给带录音的笔记加视频）：
      //     console: [note] 保存失败: ProgressEvent
      //              TypeError: Cannot read properties of null (reading 'split') at a.onloadend
      //   两条错误同源，但只有第一条传到了业务层。
      if (typeof dataUrl !== 'string') {
        reject(new Error(dataUrl === null
          ? '文件读取失败：FileReader.result 为 null（读取被中止或已失败）'
          : `文件读取失败：FileReader.result 类型异常（${typeof dataUrl}）`))
        return
      }
      resolve(dataUrl.split(',')[1] || '')
    }
    // 透传真正的 reader.error（DOMException，带 name/message），
    // 而不是 ProgressEvent —— 否则根因不可观测，排查只能靠猜。
    reader.onerror = () => reject(reader.error ?? new Error('文件读取失败'))
    reader.onabort = () => reject(new Error('文件读取被中止'))
    reader.readAsDataURL(blob)
  })
}