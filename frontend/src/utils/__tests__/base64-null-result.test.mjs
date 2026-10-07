/**
 * base64-null-result.test.mjs — blobToBase64 的 FileReader 空值/错误路径（行为门）。
 *
 * 为什么是**行为门**而不是文本扫描：这条缺陷的形态是「promise 永不 settle」，
 * 文本扫描看不出「有没有 reject」，只能看出「有没有写 if」。真正能证伪的判据是
 * 拿一个会返回 null 的 FileReader 去跑，然后看 promise 到底怎么落地。
 *
 * ── 缺陷（2026-10-06 真机复现，Redmi 2411DRN47C / HyperOS / sttdev）────────────
 *   给一条带录音的笔记加视频保存：
 *     console: [note] 保存失败: ProgressEvent
 *              TypeError: Cannot read properties of null (reading 'split') at a.onloadend
 *     页面:    「保存失败，请稍后重试」
 *
 *   两条错误同源，但**互不相干**：
 *     ① ProgressEvent 来自 `reader.onerror = reject` —— 值是 ProgressEvent，
 *       真正的 reader.error（DOMException，带 name/message）被丢掉了，
 *       所以根因在业务层不可观测。
 *     ② TypeError 来自 `reader.result as string` 之后直接 .split。关键在于
 *       **事件处理器里抛的异常不会 reject 外层 promise** —— 它变成 uncaught
 *       飘到 console，await 永久挂起。谁也拿不到它。
 *
 *   阴性对照在文件末尾：把旧实现原样复刻一份，断言它确实「既不 settle 又抛
 *   uncaught」。没有这一步，上面几条断言就只是「现在的实现碰巧长这样」。
 */

import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { afterEach, describe, it } from 'node:test'
import { blobToBase64 } from '../base64.ts'

const RealFileReader = globalThis.FileReader
afterEach(() => { globalThis.FileReader = RealFileReader })

/**
 * 装一个可控的 FileReader。
 * `behaviour`: 'loadend'（带 result）/ 'error'（带 error）/ 'abort'
 */
function stubFileReader({ result = null, error = null, behaviour = 'loadend' }) {
  globalThis.FileReader = class StubFileReader {
    constructor() {
      this.result = null
      this.error = null
      this.onloadend = null
      this.onerror = null
      this.onabort = null
    }
    readAsDataURL() {
      queueMicrotask(() => {
        if (behaviour === 'error') {
          this.error = error
          this.result = null
          this.onerror?.({ type: 'error', target: this })
        } else if (behaviour === 'abort') {
          this.result = null
          this.onabort?.({ type: 'abort', target: this })
        } else {
          this.result = result
          this.onloadend?.({ type: 'loadend', target: this })
        }
      })
    }
  }
}

/** promise 在窗口期内是否落地；落地返回 {state:'fulfilled'|'rejected', value}，否则 null。 */
function settleWithin(p, ms = 60) {
  return Promise.race([
    p.then((value) => ({ state: 'fulfilled', value }), (value) => ({ state: 'rejected', value })),
    new Promise((r) => setTimeout(() => r(null), ms)),
  ])
}

describe('blobToBase64：FileReader 的空值与错误路径', () => {
  it('result 为 null 时必须 reject（不能挂起、不能抛 uncaught）', async () => {
    stubFileReader({ result: null })
    const got = await settleWithin(blobToBase64(new Blob(['x'])))
    assert.ok(got, '★ promise 永不 settle —— await 永久挂起，调用方界面卡在「保存中」')
    assert.equal(got.state, 'rejected', `result=null 却 resolve 了：${JSON.stringify(got.value)}`)
    assert.ok(got.value instanceof Error, '拒绝值必须是 Error，而不是 ProgressEvent 之类')
    assert.match(got.value.message, /null/, '拒绝原因里应能看出是 result 为 null，而不是笼统的「失败」')
  })

  it('onerror 时 reject 的是 reader.error 本身，而不是 ProgressEvent', async () => {
    const domEx = Object.assign(new Error('NotReadableError: 设备忙'), { name: 'NotReadableError' })
    stubFileReader({ behaviour: 'error', error: domEx })
    const got = await settleWithin(blobToBase64(new Blob(['x'])))
    assert.ok(got, '★ onerror 路径下 promise 挂起')
    assert.equal(got.state, 'rejected')
    // 身份判据：根因必须原样透出。ProgressEvent 没有 name='NotReadableError'。
    assert.equal(got.value, domEx, '★ 拒绝值不是 reader.error —— 真实根因在业务层丢失')
    assert.equal(got.value.name, 'NotReadableError')
  })

  it('onabort 时必须 reject', async () => {
    stubFileReader({ behaviour: 'abort' })
    const got = await settleWithin(blobToBase64(new Blob(['x'])))
    assert.ok(got, '★ onabort 路径下 promise 挂起')
    assert.equal(got.state, 'rejected')
    assert.match(got.value.message, /中止/)
  })

  it('正常路径：data URL 去掉前缀后 resolve（回归，别把好的那条弄坏）', async () => {
    stubFileReader({ result: 'data:audio/webm;base64,QUJD' })
    assert.equal(await blobToBase64(new Blob(['x'])), 'QUJD')
  })
})

describe('阴性对照：旧实现必须被上面那些断言抓住', () => {
  /**
   * ★ 阴性对照必须**跑在子进程里**。
   * 第一版在同进程用 `process.on('uncaughtException')` 接住：断言全过（settled=null、
   * 收到 reading 'split'），但 node:test 自己也在监听 uncaughtException，
   * 于是它在测试结束后把这条异步异常记成
   *   "A resource generated asynchronous activity after the test ended"
   * 并把整个文件判红 —— **负控自己把门打红了**。
   * 隔离到子进程后，爆炸只影响那个一次性进程，本文件的判定保持干净。
   */
  const OLD_IMPL = `
    function oldBlobToBase64(blob) {
      return new Promise((resolve, reject) => {
        const reader = new FileReader()
        reader.onloadend = () => {
          const dataUrl = reader.result
          resolve(dataUrl.split(',')[1] || '')
        }
        reader.onerror = reject
        reader.readAsDataURL(blob)
      })
    }
  `

  /** 在子进程里跑旧实现，把观测结果以 JSON 打到 stdout。 */
  function runOldImplInChild(behaviour) {
    const script = `
      ${OLD_IMPL}
      const uncaught = []
      process.on('uncaughtException', (e) => uncaught.push(String(e)))
      globalThis.FileReader = class {
        constructor() { this.result = null; this.error = null; this.onloadend = null; this.onerror = null }
        readAsDataURL() {
          queueMicrotask(() => {
            if (${JSON.stringify(behaviour)} === 'error') {
              this.error = Object.assign(new Error('NotReadableError: 设备忙'), { name: 'NotReadableError' })
              this.result = null
              this.onerror && this.onerror({ type: 'error' })
            } else {
              this.result = null
              this.onloadend && this.onloadend({ type: 'loadend' })
            }
          })
        }
      }
      const p = oldBlobToBase64(new Blob(['x']))
      // done 标志：第一版两条分支都往 stdout 写，且中间没有换行，
      // 于是 reject 分支的 JSON 与 150ms 兜底的 JSON 粘成一段，
      // 父进程 JSON.parse 直接 SyntaxError。两条只能活一条。
      let done = false
      const emit = (o) => { if (done) return; done = true; process.stdout.write('\\n' + JSON.stringify(o)) }
      p.then(
        (v) => emit({ settled: true, state: 'fulfilled', value: String(v) }),
        (e) => emit({
          settled: true, state: 'rejected',
          isReaderError: e === null || e === undefined ? false : e.name === 'NotReadableError',
          type: e && e.type, uncaught,
        }),
      )
      setTimeout(() => emit({ settled: false, uncaught }), 150)
    `
    const out = execFileSync(process.execPath, ['-e', script], { encoding: 'utf8', timeout: 10_000 })
    return JSON.parse(out.trim())
  }

  it('旧实现遇到 result=null：既不 settle，又抛 uncaught TypeError', () => {
    const r = runOldImplInChild('loadend')
    assert.equal(r.settled, false, '预期旧实现永远不 settle；若这里已 settle，说明复刻的实现已经不是原来那份')
    assert.ok(r.uncaught.length > 0, '预期旧实现抛 uncaught TypeError；一条都没抛说明事件处理器里没炸')
    assert.match(r.uncaught[0], /reading 'split'/, 'uncaught 的应是 .split 上的 TypeError')
  })

  it('旧实现的 onerror 路径：拒绝值是 ProgressEvent，reader.error 丢失', () => {
    const r = runOldImplInChild('error')
    assert.equal(r.settled, true, '旧实现也应当 reject')
    assert.equal(r.state, 'rejected')
    assert.equal(r.isReaderError, false, '旧实现本该丢失 reader.error；若这里为 true，说明对照失真')
    assert.equal(r.type, 'error', '旧实现拒绝的是 ProgressEvent（靠 type 字段辨认）')
  })

  it('新实现在同样两个场景下都落地（对照的另一半：不是对照自己太弱）', async () => {
    stubFileReader({ result: null })
    const a = await settleWithin(blobToBase64(new Blob(['x'])))
    assert.ok(a && a.state === 'rejected', '新实现必须在 result=null 时 reject（旧实现这里是 settled=false）')

    const domEx = Object.assign(new Error('NotReadableError: 设备忙'), { name: 'NotReadableError' })
    stubFileReader({ behaviour: 'error', error: domEx })
    const b = await settleWithin(blobToBase64(new Blob(['x'])))
    assert.ok(b && b.state === 'rejected', '新实现必须在 onerror 时 reject')
    assert.equal(b.value, domEx, '新实现必须透传 reader.error（旧实现这里是 false）')
  })
})