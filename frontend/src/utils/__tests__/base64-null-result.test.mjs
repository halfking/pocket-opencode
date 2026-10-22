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
import { readFileSync, readdirSync } from 'node:fs'
import { dirname, join, relative, resolve } from 'node:path'
import { afterEach, describe, it } from 'node:test'
import { fileURLToPath } from 'node:url'
import { blobToBase64, arrayBufferToBase64, utf8ToBase64, base64ToUtf8 } from '../base64.ts'

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
/**
 * §888 D58：`blobToBase64` 在本仓有 **5 份实现**，其中两份是**真缺陷** ——
 * 而且公共实现（`utils/base64.ts`）**早就在那里**，只是没人收编。
 *
 * 普查读数（§888 落地前）：
 *
 * | 位置 | 形态 | result=null 时 | 危害 |
 * |---|---|---|---|
 * | `utils/base64.ts:5` | **公共实现** | reject（带可读原因） | — |
 * | `utils/download-encoding.ts:21` | 第二份公共实现 | reject | 纯重复 |
 * | `utils/version.ts:305` | 私有副本 | reject | 纯重复 |
 * | `features/email/use-invoice-thumbs.ts:37` | 私有副本 | reject | 纯重复 |
 * | `composables/usePdfViewer.ts:35` | 私有副本 | **Promise 永久挂起** | ❌ 真缺陷 |
 * | `features/meetings/audio-parts.ts:19` | 私有副本 | **resolve(空串)** | ❌❌ 最严重 |
 *
 * ⭐⭐ `audio-parts.ts` 那份是**会议录音分片落盘**（本仓录音主链路）：
 *
 *   `String(reader.result || '')` → `''` → `indexOf(',')` 为 -1 → **resolve('')**
 *   ⇒ `saveAudioPart` 拿到空串后继续：
 *      · 原生平台 `Filesystem.writeFile({data: ''})` 写出**一个 0 字节的 webm**
 *      · 落库 `filePath ? '' : dataBase64` ⇒ `data_base64` **存空串**
 *      · `return id` ⇒ **报成功**
 *   ⇒ 那一段录音**永久丢失**，库与文件都看不出异常。
 *
 *   ⚠ 最狠的一点：`ingest-speech.ts:52` 那个 `saveAudioPart(...).catch(console.warn)`
 *     **永远不会被触发** —— 它是这条链上唯一存在的诊断线索，而失败压根没走失败分支。
 *     该处注释还写着「§826 让这件事在 Android 上变得不可逆：原生模式压根不产整段音频，
 *     分片是那段话的**唯一一份拷贝**」。
 *
 * ⇒ 与 §876/D46 同族（「排队成功」被报成「落盘成功」），但更狠：
 *   这里连**失败分支都没进**，所以连诊断都没有。
 */
describe('§888 · D58：blobToBase64 只能有一份实现，且所有调用点必须用公共实现', () => {
  const SRC_ROOT = fileURLToPath(new URL('../../', import.meta.url))
  const SKIP = (p) => p.includes('__tests__') || p.endsWith('.test.ts') || p.endsWith('.test.mjs')

  function walk(dir, out = []) {
    for (const name of readdirSync(dir, { withFileTypes: true })) {
      const p = join(dir, name.name)
      if (name.isDirectory()) {
        if (name.name === 'node_modules') continue
        walk(p, out)
      } else if (/\.(ts|vue)$/.test(name.name) && !SKIP(p)) out.push(p)
    }
    return out
  }

  /** 剥注释：锚点必须落在真代码上，不能落在自己写的说明文字上。 */
  const strip = (t) =>
    t.replace(/\/\*[\s\S]*?\*\//g, (m) => m.replace(/[^\n]/g, ' '))
     .replace(/(^|[^:])\/\/[^\n]*/g, '$1')

  /** 全仓 `blobToBase64` 的**定义处**（不是 import、不是调用）。 */
  const defs = () =>
    walk(SRC_ROOT)
      .map((abs) => ({ file: relative(SRC_ROOT, abs), code: strip(readFileSync(abs, 'utf8')) }))
      .flatMap(({ file, code }) =>
        [...code.matchAll(/(?:^|\n)\s*(?:export\s+)?(?:async\s+)?function\s+blobToBase64\b/g)]
          .map(() => file))

  /** ⚠ 前置闸：漏扫时下面几条会在子集上**假绿**（§885 实测过这条）。 */
  const assertScanned = () => {
    assert.ok(walk(SRC_ROOT).length >= 100, `只扫到 ${walk(SRC_ROOT).length} 个源文件 —— 扫描器坏了`)
  }

  it('★★★ 阳性对照：扫描器能看到全仓（前置闸，不是同伴用例）', () => {
    assertScanned()
  })

  it('★★★ blobToBase64 的定义全仓只能有一份，且必须是公共实现', () => {
    assertScanned()
    const d = defs()
    assert.equal(d.length, 1,
      `blobToBase64 有 ${d.length} 份定义：${d.join(', ')}\n` +
      '§888 落地前是 5 份，其中两是真缺陷（audio-parts 把失败 resolve 成空串、' +
      'usePdfViewer 让 promise 永久挂起）。复制出去的那份一定会漂移 —— 请改成 import `utils/base64.ts`。')
    assert.equal(d[0], join('utils', 'base64.ts'), `唯一定义跑到了 ${d[0]}`)
  })

  it('★★★ 录音分片那处不许再自带实现（它是唯一一份「失败报成成功」的）', () => {
    assertScanned()
    const src = strip(readFileSync(resolve(SRC_ROOT, 'features/meetings/audio-parts.ts'), 'utf8'))
    assert.doesNotMatch(src, /function\s+blobToBase64/,
      'audio-parts.ts 又自带了一份 —— 它失败时会 resolve(空串)，' +
      '原生平台于是写出 0 字节 webm、库里存空串、return id 报成功，' +
      '而 ingest-speech.ts 那个唯一存在的 .catch(console.warn) 永不触发（D58）')
    assert.match(src, /from '[^']*utils\/base64/,  // 抗有无 .ts 扩展名（§875 记着 node 加载不了无扩展名 import）
      'audio-parts.ts 没有 import 公共实现')
  })

  it('★★★ PDF 预览那处必须用公共实现（旧写法让 promise 永久挂起，try/catch 兜不住）', () => {
    assertScanned()
    const src = strip(readFileSync(resolve(SRC_ROOT, 'composables/usePdfViewer.ts'), 'utf8'))
    assert.doesNotMatch(src, /function\s+blobToBase64/, 'usePdfViewer.ts 又自带了一份（D58）')
    assert.match(src, /from '[^']*utils\/base64/, 'usePdfViewer.ts 没有 import 公共实现')
  })

  it('★★ 负控：复刻 audio-parts 那份，确认它真的把失败 resolve 成空串', () => {
    // 没有这一步，上面「不许自带实现」只是「现在的实现碰巧长这样」。
    // ⚠ 必须跑在**子进程**里：本文件的负控已记录过同进程会污染 node:test 判定
    //   （见上面「阴性对照」那段注释）。
    const script = `
      function oldBlobToBase64(blob) {
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
      globalThis.FileReader = class {
        constructor() { this.result = null; this.error = null; this.onload = null; this.onerror = null }
        readAsDataURL() {
          queueMicrotask(() => { this.result = null; this.onload && this.onload({ type: 'load' }) })
        }
      }
      oldBlobToBase64(new Blob(['x'])).then(
        (v) => process.stdout.write(JSON.stringify({ state: 'fulfilled', value: String(v) })),
        (e) => process.stdout.write(JSON.stringify({ state: 'rejected', msg: String(e && e.message) })),
      )
    `
    const out = execFileSync(process.execPath, ['-e', script], { encoding: 'utf8', timeout: 10_000 })
    const r = JSON.parse(out.trim())
    assert.equal(r.state, 'fulfilled',
      '旧实现本该把失败 resolve 成空串；若这里 reject 了，说明复刻的实现已经不是原来那份')
    assert.equal(r.value, '',
      '旧实现 resolve 的应是**空串**（于是写出 0 字节文件、库里存空串、对外报成功）')
  })

  it('★★★ 新实现对同一场景 reject（对照的另一半：不是对照自己太弱）', async () => {
    stubFileReader({ result: null })
    const got = await settleWithin(blobToBase64(new Blob(['x'])))
    assert.ok(got, '★ 公共实现必须在 60ms 内落地')
    assert.equal(got.state, 'rejected',
      `公共实现不该 resolve（旧实现这里是 fulfilled("")）—— 一旦回退，audio-parts 那条链就会复活`)
  })
})

/**
 * §889 D59：base64 工具族的问题**不是**「同一个名字抄了 N 份」，
 * 而是**同一个概念被命名成好几个不同的名字** —— 于是 §888 那道「定义数 = 1」的门
 * 压根管不到它们（门按函数名判，而这些函数连名字都不一样）。
 *
 * §889 落地前的普查读数（「字节数组 → base64」这一个概念）：
 *   native/recording-audio-transcode.ts::arrayBufferToBase64   ← **录音转写上传链路**
 *   utils/download-encoding.ts::arrayBufferToBase64
 *   features/email/email-image-preload.ts::bytesToBase64
 *   features/email/email-body-format.ts::b64FromBytes（私有，另套一层 try/catch）
 *
 * ⭐ 四份**写法各不相同**（`String.fromCharCode.apply` / 展开 `...` /
 *   `apply(null, Array.from(...))` / 套 try/catch），但行为等价 ——
 *   而且**每一份都分块**（0x8000 / 32768），因为 `String.fromCharCode` 与
 *   `Function.prototype.apply` 都有**参数个数上限**，整块传会在大文件上直接 RangeError。
 *
 * ⚠ **D59**：`email-body-format.ts` 里那个 `base64ToBytesSafe`，名字读起来像
 *   「`base64ToBytes` 的安全版」，实际语义是「**跳过 base64 解码**、按 latin1
 *   取原始字节」—— 它不是加固版，是**另一条路径**。
 *   而**同一个文件里确实另有一个真叫 `base64ToBytes` 的**（§889 审计时读到，
 *   `email-body-format.ts:464`）⇒ 旧名不是"啰嗦"，是**指向隔壁那个函数**。
 *   若被当成加固版调用，传给 `decodePartBytes(…, charset)` 的就是未校验的
 *   latin1 字节而不是字符 ⇒ **邮件正文乱码**。
 *   §889 改名为 `latin1PartToBytes`（私有、单一调用点 ⇒ 零风险）。
 */
describe('§889 · D59：bytes→base64 的实现收编到一处（形态无关判定）', () => {
  const SRC_ROOT = fileURLToPath(new URL('../../', import.meta.url))
  const SKIP = (p) => p.includes('__tests__') || p.endsWith('.test.ts') || p.endsWith('.test.mjs')

  function walk(dir, out = []) {
    for (const name of readdirSync(dir, { withFileTypes: true })) {
      const p = join(dir, name.name)
      if (name.isDirectory()) {
        if (name.name === 'node_modules') continue
        walk(p, out)
      } else if (/\.(ts|vue)$/.test(name.name) && !SKIP(p)) out.push(p)
    }
    return out
  }

  const strip = (t) =>
    t.replace(/\/\*[\s\S]*?\*\//g, (m) => m.replace(/[^\n]/g, ' '))
     .replace(/(^|[^:])\/\/[^\n]*/g, '$1')

  /**
   * 「这个文件里有没有 bytes→base64 的实现」——
   *
   * ⚠⚠ 判定用**形状**（`btoa(` 与 `String.fromCharCode` 同时出现），
   *   **不用函数名**。原因（实测，见下面「量具自检」那条）：
   *   判据写成 `/function\s+arrayBufferToBase64/` 时，
   *   `const bytesToBase64 = (buf) => …` 这种**形态**的副本**全绿逃掉**。
   *   名字可以改、形态可以换，形状改不掉 —— 字节变 base64 只能靠
   *   「把字节摊成 binary string」+「btoa」这两步。
   *
   * ⚠ 但形状**不能拿来做全仓唯一性**判定：`native/crypto.ts` 里把 salt/密钥
   *   存进 localStorage 时用的就是 `btoa(String.fromCharCode(...))` ——
   *   形状相同，但那是业务流程里顺手的一步，不是可复用工具，收编过去反而别扭。
   *   ⇒ **形状只在这 4 个「收编落点」上判**（它们本来就该只剩公共实现），
   *   全仓不做唯一性断言。
   */
  const hasBytesToBase64 = (src) =>
    /\bbtoa\(/.test(src) && /String\.fromCharCode/.test(src)

  /**
   * 四个**收编落点**：每一处都不许再自带 bytes→base64 实现。
   *
   * ⚠ `email-body-format.ts` 那份**有意**留了一个薄包装 `b64FromBytes`：
   *   它套的 `catch → ''` 是既有行为（§889 不代拍，改它属行为变更）。
   *   ⇒ 这里判的是「体内不再自己拼 base64」，而不是「函数不许存在」。
   */
  const RECLAIMED = [
    ['native/recording-audio-transcode.ts', '录音转写上传链路那份（§889 收编自它）'],
    ['utils/download-encoding.ts', '第二份公共实现（§889 收编自它）'],
    ['features/email/email-image-preload.ts', '原本叫 bytesToBase64（apply(null, Array.from(...))，多一次拷贝）'],
    ['features/email/email-body-format.ts', '薄包装 b64FromBytes（外面套着既有的 catch → ""）'],
  ]

  /**
   * 这三个落点**没有任何合法的 btoa**（实测：base64.ts 之外，btoa 只出现在
   * `crypto.ts` / `pocket-native.ts` / `note-files.ts` / `download-encoding.ts` 的
   * `utf8ToBase64` 里）⇒ 规则 A 之外再加一条更紧的「连 btoa 都不许出现」。
   * `download-encoding.ts` 排除在外：它那句 btoa 是 **utf8ToBase64**（字符串→base64，
   * 与 bytes→base64 是**两个概念**），不是漏网的副本。
   */
  const NO_BTOA_AT_ALL = [
    'native/recording-audio-transcode.ts',
    'features/email/email-image-preload.ts',
    'features/email/email-body-format.ts',
  ]

  const assertScanned = () => {
    assert.ok(walk(SRC_ROOT).length >= 100, `只扫到 ${walk(SRC_ROOT).length} 个源文件 —— 扫描器坏了`)
  }

  it('★★★ 阳性对照：扫描器能看到全仓（前置闸）', () => {
    assertScanned()
  })

  it('★★★ 量具自检：规则能抓住**非 function 形态**的副本，且不误伤 utf8ToBase64', () => {
    // 这条不是"同伴用例"，它是**量具自己的体检**。
    // ⚠ 判据若写成 `/function\s+名字/`，const/箭头形态的副本会**全绿逃掉**：
    //   §889 变异实测（注入 const 形态副本）⇒ 19/19 全绿 ⇒ 判据无敏感性。
    const constForm = [
      'const bytesToBase64 = (buf: ArrayBuffer): string => {',
      '  let bin = \'\';',
      '  for (let i = 0; i < buf.byteLength; i += 0x8000) {',
      '    bin += String.fromCharCode(...new Uint8Array(buf, i, 0x8000));',
      '  }',
      '  return btoa(bin);',
      '}',
    ].join('\n')
    assert.ok(hasBytesToBase64(constForm),
      '规则没抓住 const 形态的副本 ⇒ 判据退化成「只认 function 声明」，换个写法就逃得掉')
    assert.ok(!hasBytesToBase64(
      'export function utf8ToBase64(s: string): string {\n  return btoa(unescape(encodeURIComponent(s)))\n}'),
      '规则把 utf8ToBase64（字符串→base64，另一个概念）误判了 ⇒ 规则太宽')
  })

  it('★★★ 四个收编落点都不许再自带 bytes→base64 实现（形态无关）', () => {
    assertScanned()
    const back = []
    for (const [file, why] of RECLAIMED) {
      const src = strip(readFileSync(resolve(SRC_ROOT, file), 'utf8'))
      if (hasBytesToBase64(src)) back.push(`${file} —— ${why}`)
    }
    assert.deepEqual(back, [],
      `这些收编过的落点又自带了一份 bytes→base64 实现：\n  - ${back.join('\n  - ')}\n` +
      '公共实现在 utils/base64.ts（arrayBufferToBase64 / blobToBase64），请改成 import。')
  })

  it('★★★ 三个落点里连 btoa 都不许出现（download-encoding 的 utf8ToBase64 除外）', () => {
    assertScanned()
    const back = []
    for (const file of NO_BTOA_AT_ALL) {
      const src = strip(readFileSync(resolve(SRC_ROOT, file), 'utf8'))
      if (/\bbtoa\(/.test(src)) back.push(file)
    }
    assert.deepEqual(back, [],
      `这些落点里出现了 btoa( ⇒ 要么自己拼了 base64，要么加了一个新的编码器：\n  - ${back.join('\n  - ')}\n` +
      '新增编码器请先想清楚它是不是「bytes→base64」的又一份副本。')
  })

  it('★★★ 公共实现必须保留分块（整块传会在大文件上 RangeError）', () => {
    const src = strip(readFileSync(resolve(SRC_ROOT, 'utils/base64.ts'), 'utf8'))
    assert.match(src, /CHUNK|chunk/,
      'arrayBufferToBase64 里看不到分块常量 ⇒ String.fromCharCode 超参会抛 RangeError')
  })

  it('★★★ 公共实现与 Node Buffer **逐字节**一致，且跨分块边界', () => {
    // ⚠ 边界值必须覆盖：32767 / 32768 / 32769 —— 只测 0 与 200000 会漏掉
    //   「恰好等于分块大小」这一档（`i + CHUNK` 越界一格就会多编一个空块）。
    for (const size of [0, 1, 32767, 32768, 32769, 65536, 200000]) {
      const buf = new ArrayBuffer(size)
      const dv = new DataView(buf)
      for (let i = 0; i < size; i++) dv.setUint8(i, (i * 7) % 256)
      assert.equal(arrayBufferToBase64(buf), Buffer.from(new Uint8Array(buf)).toString('base64'),
        `size=${size} 与 Buffer 不一致 ⇒ 公共实现的分块边界有问题`)
    }
  })

  it('★★★ 录音链路那份必须 re-export 且带 .ts（§881 的判据在 node 里 import 它）', () => {
    assertScanned()
    const src = strip(readFileSync(resolve(SRC_ROOT, 'native/recording-audio-transcode.ts'), 'utf8'))
    // ⚠ 这里**必须带扩展名**：§881 的判据在 node 里 import 这个模块，而
    //   `node --experimental-strip-types` 不接受无扩展名 import（§875）。
    assert.match(src, /export \{ arrayBufferToBase64 \} from '\.\.\/utils\/base64\.ts'/,
      "re-export 必须写成 `export { arrayBufferToBase64 } from '../utils/base64.ts'`，" +
      '否则 §881 的判据加载不了本模块')
  })

  it('★★ 那个「名字与语义相反」的函数不许再叫 base64ToBytesSafe（D59）', () => {
    assertScanned()
    const src = strip(readFileSync(resolve(SRC_ROOT, 'features/email/email-body-format.ts'), 'utf8'))
    assert.doesNotMatch(src, /base64ToBytesSafe/,
      'base64ToBytesSafe 又回来了 —— 它读起来像「base64ToBytes 的安全版」，' +
      '实际是「跳过 base64 解码、按 latin1 取原始字节」；当成加固版调用会得到乱码正文（D59）')
    assert.match(src, /function latin1PartToBytes/, '应已改名为 latin1PartToBytes')
    assert.match(src, /latin1PartToBytes\(part\.body\)/, '调用点必须跟着改名')
  })

  it('★★ 边界登记：crypto.ts 的**内联**编码不在收编范围（形状相同但不是工具函数）', () => {
    // 这条不是「必须成立」而是**量具边界的记录**：
    // `native/crypto.ts` 把 salt / 密钥材料编码成字符串存 localStorage，
    // 用的也是 `btoa(String.fromCharCode(...))` —— 形状与公共实现相同，
    // 但它**不是可复用工具**，收编过去反而别扭。
    // ⇒ 「bytes→base64」这个形状天然会在多处出现，所以形状不是**全仓**判据。
    const cryptoSrc = strip(readFileSync(resolve(SRC_ROOT, 'native/crypto.ts'), 'utf8'))
    assert.match(cryptoSrc, /btoa\(String\.fromCharCode\(/,
      'crypto.ts 的内联编码不见了 ⇒ 若它被改成了别的形态，请重新评估「形状不是全仓判据」这个前提')
  })

  it('★★ 边界登记：这道门**只覆盖那 4 个落点**，不是全仓唯一性断言', () => {
    // 如实登记已知边界（不假装覆盖范围比实际大）：
    // 在**别处**新写第 5 份 bytes→base64 副本，这道门抓不到
    // （§889 变异实测：新增 src/utils/rogue-base64.ts ⇒ 19/19 全绿）。
    // ⇒ 全仓唯一性靠**审计普查**（§888 那类「同名函数 > 1 次」清单），
    //   不靠这道门。门只负责"已经收编过的地方别退回去"。
    const rogue = strip([
      'export function bytesToBase64(buf: ArrayBuffer): string {',
      '  let bin = \'\'',
      '  bin += String.fromCharCode(...new Uint8Array(buf))',
      '  return btoa(bin)',
      '}',
    ].join('\n'))
    assert.ok(hasBytesToBase64(rogue),
      '规则连这种一眼可见的副本都认不出 ⇒ 形状规则本身坏了，上面几条的通过毫无意义')
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// §891 D61：`loadFullContent` 的降级 `body ?? note.content` 是**数据销毁机制**。
//
// 链条（逐段核对源码，非推断）：
//   1. note-storage-policy.ts:55-62  tier='file' ⇒ displayContent = summarizeForDisplay(body)
//                               （**只取第一段、≤280 字**），落进 local_notes.content
//   2. notes-persist.ts:81-93       INSERT 写的就是这个 storedContent
//   3. notes-persist.ts:331-334     body 读不到 ⇒ 退回 note.content（= 摘要）
//   4. notes-persist.ts:213,217-223 updateNote 里 patch.content !== undefined
//                               ⇒ persistNotePayload({ body: nextContent }) 写回正文文件
//   ⇒ 「打开笔记 → 只改标题 → 保存」足以把全文永久换成摘要。
//
// 而原先正文**损坏**时 readNoteBody 是抛的，**只有缺失/空行才走降级**
// ⇒ 一个静默销毁数据、一个报错。D61 把两者对齐成「都出声」。
// ─────────────────────────────────────────────────────────────────────────────
describe('§891 · D61：base64 解码失败必须返回 null（不抛），否则降级契约形同虚设', () => {
  // ⚠⚠ 本文件里已有一个 `SRC_ROOT`，但它是 §889 那个 describe 内部的**局部**常量，
  //   在这里取不到。⇒ 这里自己算一份，别指望复用。
  //   （顺带：本块第一版写成 `HERE` 而这里没有 HERE，跑出来是 suite 构造期
  //     `ReferenceError: HERE is not defined`，而汇总行仍是 `# pass 23 / # fail 0`
  //     —— 正是「suite 构造期失败不计进 # fail」那条。只看计数会把整块判据当成全绿。）
  const SRC_ROOT = fileURLToPath(new URL('../../', import.meta.url))
  const NOTES_PERSIST = resolve(SRC_ROOT, 'features/notes/notes-persist.ts')
  const NOTE_FILES = resolve(SRC_ROOT, 'features/notes/note-files.ts')

  it('★★★ 行为门：截断/污染的 base64 返回 null 而不是抛', () => {
    // ⚠ 直接调用被导出的真实函数 ⇒ 形态无关（§889 被审计批过的「按名字+形态判」
    //   那类假绿在这里不成立：把实现从 function 改成 const 箭头，用例照样咬得住）。
    const valid = utf8ToBase64('（语音草稿）你好世界'.repeat(20))
    for (const [name, bad] of [
      ['末尾截断 1 个字符', valid.slice(0, -1)],
      ['末尾截断 3 个字符', valid.slice(0, -3)],
      ['长度非 4 的倍数', valid.slice(0, 7)],
      ['中间插非法字符', valid.slice(0, 10) + '!' + valid.slice(10)],
      ['payload 含 NUL', valid.slice(0, 10) + '\u0000' + valid.slice(10)],
    ]) {
      assert.equal(base64ToUtf8(bad), null, `${name} 时必须返回 null，不能抛 —— 抛会绕过 loadFullContent 的降级契约`)
    }
  })

  it('★★★ 负控：复刻旧的 decodeURIComponent(escape(atob(...)))，确认它确实抛', () => {
    // 本仓「负控自伤」教训：必须先确认**对照本身**确实有病。
    const legacy = (b64) => decodeURIComponent(escape(atob(b64)))
    const valid = utf8ToBase64('（语音草稿）你好世界'.repeat(20))
    let threw = false
    try { legacy(valid.slice(0, -1)) } catch { threw = true }
    assert.ok(threw, '负控假定失效：旧实现在截断时不再抛 ⇒ 本门失去区分力')
  })

  it('★★ 正常内容往返不变（含中文/emoji/NUL/全 latin1 字节）', () => {
    for (const s of ['abc', '你好世界', '👋🎉', 'a\u0000b', '\x00\x01\x7f\x80\xff']) {
      assert.equal(base64ToUtf8(utf8ToBase64(s)), s, `往返失败：${JSON.stringify(s)}`)
    }
  })

  it('★★ 含空白的 base64 仍要解得出（atob 容忍 RFC 2045 换行）', () => {
    // ⭐ 防「矫枉过正」：修「抛错」时很容易顺手把空白也收紧，
    //   而 MIME 的 base64 是允许折行的 —— 收紧会让**合法**输入被判成损坏。
    const valid = utf8ToBase64('（语音草稿）你好世界')
    const wrapped = valid.slice(0, 10) + '\n  ' + valid.slice(10)
    assert.equal(base64ToUtf8(wrapped), '（语音草稿）你好世界')
  })

  it('★★ utf8ToBase64 搬家后契约不变（承接 download.test.mjs 的既有断言）', () => {
    assert.equal(utf8ToBase64('hello'), 'aGVsbG8=')
    assert.equal(utf8ToBase64(''), '')
    const cn = utf8ToBase64('你好 POCKET')
    assert.equal(Buffer.from(cn, 'base64').toString('utf8'), '你好 POCKET')
  })

  // ⚠⚠ 下面两条源码断言都必须**先剥注释**再匹配：
  //   §891 的修复动作本身留下了大量解释性注释，其中就写着
  //   「原来这里是 `return body ?? note.content`」与
  //   「这三处原先是裸的 `decodeURIComponent(escape(atob(x)))`」。
  //   不剥的话，判据会命中**我自己写的说明文字** ⇒ 一份完全正确的源码被判红。
  //   （本仓同类事故已发生过两次：note-persist-fk-order.test.mjs:55 与本文件 §889。）
  const strip = (t) =>
    t.replace(/\/\*[\s\S]*?\*\//g, (m) => m.replace(/[^\n]/g, ' '))
      .replace(/(^|[^:])\/\/[^\n]*/g, '$1')

  it('★★★ D61：loadFullContent 不许再用 `?? note.content` 冒充全文', () => {
    const src = readFileSync(NOTES_PERSIST, 'utf8')
    const start = src.indexOf('export async function loadFullContent(')
    assert.ok(start > 0, '定位失败：找不到 loadFullContent()')
    const body = strip(src.slice(start, src.indexOf('\nexport ', start + 10)))
    assert.doesNotMatch(body, /\?\?\s*note\.content/,
      'loadFullContent 不许退回 note.content —— tier=\'file\' 时它是 ≤280 字摘要，updateNote 会把它写回正文文件覆盖全文（D61）')
    assert.match(body, /if \(body === null\) throw new NoteBodyUnavailableError\(/,
      '正文读不到时必须抛具名错误（与「文件损坏」路径已有的抛错行为对齐）')
  })

  it('★★★ 反向：body_path 未回填时的**安全**降级必须保留（不许一刀切删掉）', () => {
    // ⭐ 防矫枉过正：把降级整体删掉会让「附件写失败」的笔记也打不开，
    //   而那正是 notes-persist.ts:79-80 注释里明确要的降级。
    //   两种场景不可混为一谈：**附件写失败**（content 还是全文）vs **正文丢失**（content 是摘要）。
    const src = readFileSync(NOTES_PERSIST, 'utf8')
    const start = src.indexOf('export async function loadFullContent(')
    const body = src.slice(start, start + 600)
    assert.match(body, /if \(note\.storageTier !== 'file' \|\| !note\.bodyPath\) return note\.content/,
      'body_path 未回填时的降级必须保留 —— 那时 note.content 仍是全文，不是摘要')
  })

  it('★★ note-files.ts 的三处解码必须走 decodeBody（不再有裸 atob 字面）', () => {
    const src = strip(readFileSync(NOTE_FILES, 'utf8'))
    assert.doesNotMatch(src, /decodeURIComponent\(escape\(atob\(/,
      'note-files.ts 仍有裸的 base64→utf-8 实现：解不出来时会抛并绕过降级契约')
    assert.match(src, /function decodeBody\(/, '应有一个统一的解码 + 出声入口')
    assert.equal((src.match(/decodeBody\(/g) || []).length, 4,
      '应是 1 处定义 + 3 处调用（sqlite 行 / 文件内容 / sqlite 兜底行）')
  })

  it('★★ download-encoding.ts 必须仍导出 utf8ToBase64（公共 API 未变）', () => {
    // ⛔ 若这条例外，download.ts:28,33 的 import 会直接崩 —— 属于结构性破坏。
    const src = readFileSync(resolve(SRC_ROOT, 'utils/download-encoding.ts'), 'utf8')
    assert.match(src, /export \{ utf8ToBase64 \} from '\.\/base64\.ts'/,
      'download-encoding.ts 必须 re-export utf8ToBase64（§875：带 .ts 扩展名才能在 node 里加载）')
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// §958：§891 收编后的**字节级行为门**（utf-8 → base64 族）。
//
// 为什么 §891 那批判据不够 —— 它们是**自洽判据**：
//   §891 唯一覆盖「搬家之后行为对」的是判据 3「往返不变」
//   （`base64ToUtf8(utf8ToBase64(s)) === s`），而往返的**真值由本仓自己的
//   两个实现共同产生** ⇒ 两侧同时换成另一族，往返照样成立，但落盘字节已不是 UTF-8。
//
//   实测（Node 22）：把这一族整体换成 utf-16le ——
//     mutEnc(s) === mutDec(mutEnc(s))            ✅ 往返判据全绿
//     mutEnc(s) === Buffer.from(s,'utf8')…        ❌ 字节已经错了
//   ⇒ 「往返不变」证明的是「这两个函数互为逆」，**不是**「落盘的是 UTF-8」。
//   而 §891 收编动的是 `note-files.ts:71` 的**正文写盘**：
//   字节错一次 = 已落盘的正文文件变成另一族编码，而解码侧会把它还原成同样的字符串
//   ⇒ **跨进程、跨版本、跨设备全都无感**，只有和别的工具互操作时才炸。
//
// 判据锚点：**Node Buffer 的 UTF-8 真值**，不是抄一份实现再比一遍
// （§889.8.3 同一纪律：抄实现 = 用同一个错误证明自己）。
// 形态无关：直接 import 真实函数，不看声明形态。
// 逐落点限定：只钉 §891 真正收编过的那几个落点，不做全仓唯一性断言。
// ─────────────────────────────────────────────────────────────────────────────
describe('§958 · §891 收编的字节级行为门：落盘字节必须是 UTF-8（不是「往返自洽」即可）', () => {
  // 样本刻意覆盖 UTF-8 的每一处边界：BMP 中文、增补平面 emoji（代理对）、
  // NUL、单个 latin1 最高位、以及纯 ASCII。少一样，下面某条边界就没人守。
  const SAMPLES = [
    ['纯 ASCII', 'hello world'],
    ['BMP 中文', '（语音草稿）你好世界'],
    ['增补平面 emoji（代理对）', '👋🎉🧑‍💻'],
    ['NUL 与控制字符', 'a\u0000b\u0007c'],
    ['latin1 最高位与边界', '\x7f\x80\xff'],
    ['组合字符与变体选择', 'é vs é 👩‍🔬'],
    ['长正文（跨分块无关，测的是编码族）', '（语音草稿）你好世界👋🎉'.repeat(500)],
  ]

  it('★★★ utf8ToBase64 的落盘字节与 Node Buffer 的 UTF-8 真值**逐字节一致**', () => {
    // ⚠ 这条**不能**用往返替代：往返是自洽判据（见本块头部的实测）。
    for (const [name, s] of SAMPLES) {
      assert.equal(
        utf8ToBase64(s),
        Buffer.from(s, 'utf8').toString('base64'),
        `${name}：utf8ToBase64 的输出不是 UTF-8 字节 ⇒ §891 收编后落盘的正文编码错了`,
      )
    }
  })

  it('★★★ base64ToUtf8 对 UTF-8 字节的还原与 Buffer 一致（解码侧同样锚 Buffer）', () => {
    for (const [name, s] of SAMPLES) {
      assert.equal(
        base64ToUtf8(Buffer.from(s, 'utf8').toString('base64')),
        s,
        `${name}：base64ToUtf8 还原不出 Buffer 编码的字节 ⇒ 解码侧与编码侧不同族`,
      )
    }
  })

  it('★★★ 负控：换成 utf-16le 的自洽同族**能过往返、过不了字节门**', () => {
    // ⚠ 先确认对照本身**确实有病**，否则上面两条可能只是「碰巧对」。
    const mutEnc = (x) => Buffer.from(x, 'utf16le').toString('base64')
    const mutDec = (x) => Buffer.from(x, 'base64').toString('utf16le')
    const s = '（语音草稿）你好世界👋🎉'
    // 对照的两个性质必须同时成立，否则它证明不了任何事：
    assert.equal(mutDec(mutEnc(s)), s,
      '负控假定失效：这个变异对连往返都不成立 ⇒ 它证明不了「往返是自洽判据」')
    assert.notEqual(mutEnc(s), Buffer.from(s, 'utf8').toString('base64'),
      '负控假定失效：这个变异对的字节与 UTF-8 相同 ⇒ 它证明不了「字节门能咬住」')
    // 而真实现必须字节对齐（正向）。
    assert.equal(utf8ToBase64(s), Buffer.from(s, 'utf8').toString('base64'),
      '真实现自己没通过字节门 ⇒ 字节门的锚点写错了')
  })

  it('★★ 逐落点限定：§891 收编的落点里不许再自带 utf-8→base64 实现（形状判定，非函数名）', () => {
    // ⚠ 形态无关：判「形状」不判「名字」—— §889.8.1 实测按名字+形态判时，
    //   注入 const 箭头形态（甚至换名）的副本全绿逃掉。
    // ⚠ 逐落点限定：不能全仓判，否则 `crypto.ts` 的内联编码会假红（§889.3）。
    const hasUtf8ToBase64 = (src) =>
      /\bbtoa\(/.test(src) && /encodeURIComponent\(/.test(src)
    const SRC_ROOT = fileURLToPath(new URL('../../', import.meta.url))
    const strip = (s) => s
      .replace(/\/\*[\s\S]*?\*\//g, '')
      .replace(/(^|[^:])\/\/.*$/gm, '$1')
    const RECLAIMED = [
      'features/notes/note-files.ts',
      'utils/download-encoding.ts',
    ]
    const back = []
    for (const file of RECLAIMED) {
      const src = strip(readFileSync(resolve(SRC_ROOT, file), 'utf8'))
      if (hasUtf8ToBase64(src)) back.push(file)
    }
    assert.deepEqual(back, [],
      `这些 §891 收编过的落点又自带了一份 utf-8→base64 实现：\n  - ${back.join('\n  - ')}\n` +
      '公共实现在 utils/base64.ts::utf8ToBase64，请改成 import。\n' +
      '⚠ 注意：即使写法换了、形状也认得 —— 但上面的字节门才是行为保证，这一条只是防搬回去。')
  })

  it('★ 量具自检：形状规则认得出非 function 形态，且不误伤 utf-16le 那一族', () => {
    const hasUtf8ToBase64 = (src) =>
      /\bbtoa\(/.test(src) && /encodeURIComponent\(/.test(src)
    assert.ok(hasUtf8ToBase64([
      'const toB64 = (s: string): string => {',
      '  return btoa(unescape(encodeURIComponent(s)))',
      '}',
    ].join('\n')), '规则认不出 const 箭头形态的副本 ⇒ 换个写法就逃得掉（§889.8.1 同款假绿）')
    assert.ok(!hasUtf8ToBase64([
      'function bytesToBase64(buf: Uint8Array): string {',
      '  return btoa(String.fromCharCode(...buf))',
      '}',
    ].join('\n')), '规则把 bytes→base64（另一个概念）误判了 ⇒ 规则太宽')
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// §959：§888 收编 `blobToBase64`（5 份 → 1 份）后的**字节级行为门**。
//
// 缺口形态：§888.5 的 6 条判据里，**没有一条钉字节** ——
//   判据 2/3/4 是文本扫描（证明「实现搬走了」），判据 5/6 是负控，
//   字节侧只有一条 `blobToBase64(new Blob(['x'])) === 'QUJD'`（**单字符**样本）。
// ⇒ 「搬走之后行为对」只靠一个 ASCII 字符撑着：改一下 base64 的**编码族**
//   （例如把二进制当 utf-8 文本再编一次），单字符样本照样绿。
//
// 而这条数据路径是**落盘**路径：`note-files.ts:101`（附件）、`audio-parts.ts:34`
//   （录音分片）、`download.ts:79`、`api/stt.ts:91`（音频上传）
//   ⇒ 字节错一次 = 写出去的 base64 解回来不是原字节。
//
// 判据锚点：**Node Buffer 的字节真值**（不是抄一份实现再比一遍，§889.8.3 纪律）。
// 判据形态：直接 import 真实函数 + 真实 Blob ⇒ 形态无关。
// 负控用**自洽同族**（§958 纪律：自洽判据对「两侧同时换族」无感，
//   所以负控必须是自洽的，才证明字节门抓的是「字节族」而不是「单侧改动」）。
// ─────────────────────────────────────────────────────────────────────────────
describe('§959 · §888 收编的字节级行为门：Blob 字节必须原样编成 base64', () => {
  /**
   * 装一个**真读字节**的 FileReader 桩。
   * ⚠ 为什么不能用现成的 `stubFileReader({result})`：它返回写死的字符串，
   *   于是测试锚的是**桩里手写的常量**，不是 Blob 的真实字节 ⇒ 门会失明。
   *   这里让桩自己去 `blob.arrayBuffer()` 取真字节，再用 Buffer 编 base64 ——
   *   锚点仍是 Node Buffer 的字节真值。
   */
  const stubRealFileReader = () => {
    globalThis.FileReader = class RealishFileReader {
      constructor() {
        this.result = null
        this.error = null
        this.onloadend = null
        this.onerror = null
        this.onabort = null
      }
      readAsDataURL(blob) {
        queueMicrotask(async () => {
          try {
            const bytes = Buffer.from(await blob.arrayBuffer())
            this.result = `data:${blob.type || 'application/octet-stream'};base64,${bytes.toString('base64')}`
            this.onloadend?.({ type: 'loadend', target: this })
          } catch (err) {
            this.error = err
            this.onerror?.({ type: 'error', target: this })
          }
        })
      }
    }
  }

  /**
   * 逐字节覆盖：0..255 全量、跨分块、含 0x00/0x80/0xFF 的边界。
   *
   * ⚠ **空 Blob 不在这里**，它单列在下面那条「边界登记」里 ——
   *   实测：空 Blob 的 data URL 是 `data:...;base64,`（**没有 payload**），
   *   而 §888 的公共实现把「缺 base64 部分」当失败 ⇒ reject。
   *   这是**既有行为**（不是本节引入的），本节**不改生产代码**，
   *   只把这条边界如实钉住，免得下一个人以为「空 blob 也该编出空串」。
   */
  const BYTE_CASES = [
    ['单字节 0x41', new Uint8Array([0x41])],
    ['全 0..255 字节', new Uint8Array(Array.from({ length: 256 }, (_, i) => i))],
    ['含 NUL 与高位字节', new Uint8Array([0x00, 0x01, 0x7f, 0x80, 0xfe, 0xff])],
    ['跨 32768 分块边界', new Uint8Array(Array.from({ length: 40000 }, (_, i) => i % 256))],
  ]

  it('★★★ blobToBase64 的输出与 Node Buffer 的字节真值逐字节一致', async () => {
    stubRealFileReader()
    for (const [name, bytes] of BYTE_CASES) {
      const blob = new Blob([bytes], { type: 'application/octet-stream' })
      const got = await blobToBase64(blob)
      assert.equal(got, Buffer.from(bytes).toString('base64'),
        `${name}：blobToBase64 的输出与 Blob 字节不一致 ⇒ §888 收编后落盘的 base64 解回来不是原字节`)
    }
  })

  it('★★ 边界登记：空 Blob 走「缺 base64 部分」⇒ reject（既有行为，非本节引入）', async () => {
    // ⚠ 如实登记，而不是把它塞进上面的字节真值表里 —— 空 Blob 的 base64
    //   确实是空串（Buffer 对得上），但**公共实现到不了那一步**：
    //   它的 data URL 形如 `data:...;base64,`，split(',')[1] 是空串 ⇒
    //   被 §888 的「缺 base64 部分」守卫判为失败 ⇒ reject。
    //   这条是**既有行为**（§888 收编前那 5 份里行为不一，本节不代拍统一口径）。
    //   登记它，是为了让「空 blob 静默产出空串」这种改动**必须显式改这条用例**，
    //   而不会被当成无关重构顺手带过去。
    stubRealFileReader()
    const got = await settleWithin(blobToBase64(new Blob([])))
    assert.ok(got, '★ promise 挂起')
    assert.equal(got.state, 'rejected',
      '空 Blob 现在 resolve 了（多半是 resolve 空串）—— 这与 §888 的守卫不一致，' +
      '若确属有意变更，请连同本用例与简报一并改口径')
    assert.match(got.value.message, /base64/, '拒绝原因应说得出是 data URL 里没有 base64 部分')
  })

  it('★★ 负控：换成 utf-8 扩张的**自洽同族**时，字节门必须红（单字符样本抓不住）', () => {
    // ⚠ 这个对照的关键性质：**它自洽**。
    //   enc_wrong：把二进制当 latin1 字符串再按 utf-8 编码一遍（高位字节 ⇒ 变长）
    //   dec_wrong：反向做一次
    //   ⇒ dec_wrong(enc_wrong(x)) === x（往返成立）
    //   ⇒ 但 enc_wrong(x) 的**字节**与 x 不同。
    // 也就是 §958 记录的形态：单侧/双侧自洽的「另一族」都能骗过往返型判据，
    // 只有锚 Buffer 的字节门能抓住。
    const enc_wrong = (bytes) => {
      const latin1 = String.fromCharCode(...bytes)
      return Buffer.from(unescape(encodeURIComponent(latin1)), 'latin1').toString('base64')
    }
    const dec_wrong = (b64) => {
      const bin = atob(b64)
      const u = new Uint8Array(bin.length)
      for (let i = 0; i < bin.length; i++) u[i] = bin.charCodeAt(i)
      return decodeURIComponent(escape(String.fromCharCode(...u)))
    }
    const bytes = new Uint8Array([0x00, 0x41, 0x80, 0xff])
    // 对照的两个性质必须同时成立，否则它证明不了任何事：
    assert.equal(dec_wrong(enc_wrong(bytes)), String.fromCharCode(...bytes),
      '负控假定失效：这个对照连往返都不成立 ⇒ 它证明不了「往返型判据无感」')
    assert.notEqual(enc_wrong(bytes), Buffer.from(bytes).toString('base64'),
      '负控假定失效：这个对照的字节与真值相同 ⇒ 它证明不了「字节门能咬住」')
    // 而单字符样本（§888 既有那条 'x' → 'QUJD'）**抓不住**它 ⇒ 这就是缺口本身。
    assert.equal(enc_wrong(new Uint8Array([0x78])), 'eA==',
      '负控前提失效：单字符样本在新同族下应当仍然成立（它全 ASCII ⇒ 编码族无关）')
  })

  it('★ 落点登记：Blob→base64 的落盘/上传路径必须走公共实现', () => {
    // 这一条是**逐落点限定**的形状门（证明「别搬回去」），不是字节门。
    const SRC_ROOT = fileURLToPath(new URL('../../', import.meta.url))
    const strip = (s) => s
      .replace(/\/\*[\s\S]*?\*\//g, '')
      .replace(/(^|[^:])\/\/.*$/gm, '$1')
    // 形状：Blob → base64 必经「读字节 + 编 base64」两步。
    // ⚠ 不能判 `btoa(`：Blob 那条路走的是 FileReader 的 data URL，不经 btoa。
    //   判据锚的是「自带 FileReader」这一形态——那就是自带实现的特征。
    const hasOwnReader = (src) => /new\s+FileReader\s*\(/.test(src) || /readAsDataURL\s*\(/.test(src)
    const LANDING = [
      'features/meetings/audio-parts.ts',
      'composables/usePdfViewer.ts',
      'features/email/use-invoice-thumbs.ts',
    ]
    const back = []
    for (const file of LANDING) {
      const src = strip(readFileSync(resolve(SRC_ROOT, file), 'utf8'))
      if (hasOwnReader(src)) back.push(file)
    }
    assert.deepEqual(back, [],
      `这些 §888 收编过的落点又自带了一份 Blob→base64 实现：\n  - ${back.join('\n  - ')}\n` +
      '公共实现在 utils/base64.ts::blobToBase64，请改成 import。')
  })
})
