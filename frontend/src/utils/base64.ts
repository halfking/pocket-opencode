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
      // ⚠⚠ §888：这里原本是 `resolve(dataUrl.split(',')[1] || '')` ——
      //   「读到了但 data URL 里没有 base64 部分」时会 **resolve 空串**，
      //   而下游（会议录音分片落盘）会把空串当成「编码成功」⇒ 写出 0 字节文件。
      //   `utils/download-encoding.ts` 里那份同族实现是**抛错**的，且
      //   `__tests__/download.test.mjs:132` 已经把它钉成了契约
      //   （「data URL 缺 base64 部分抛『文件编码失败』」）。
      //   §888 把 5 份实现收编成 1 份 ⇒ 取**更严格**的那一份：
      //   读到了但编码不出内容，与读不到是同一类失败，都必须出声。
      const base64 = dataUrl.split(',')[1]
      if (!base64) {
        reject(new Error('文件编码失败：data URL 里没有 base64 部分'))
        return
      }
      resolve(base64)
    }
    // 透传真正的 reader.error（DOMException，带 name/message），
    // 而不是 ProgressEvent —— 否则根因不可观测，排查只能靠猜。
    reader.onerror = () => reject(reader.error ?? new Error('文件读取失败'))
    reader.onabort = () => reject(new Error('文件读取被中止'))
    reader.readAsDataURL(blob)
  })
}