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
  const SRC_ROOT = new URL('../../', import.meta.url).pathname
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
 *   取原始字节」—— 它不是加固版，是**另一条路径**。若被当成加固版调用，
 *   传给 `decodePartBytes(…, charset)` 的就是未校验的 latin1 字节而不是字符
 *   ⇒ **邮件正文乱码**。§889 改名为 `latin1PartToBytes`（私有、单一调用点 ⇒ 零风险）。
 */
describe('§889 · D59：bytes→base64 这个「概念」只能有一份实现', () => {
  const SRC_ROOT = new URL('../../', import.meta.url).pathname
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
   * 四个**收编落点**：每一处都不许再自带 bytes→base64 实现。
   *
   * ⚠⚠ 这里刻意**不**做「全仓形状唯一」的断言（第一版那样写过）：
   *   那个量具把 `native/crypto.ts` 的两处**函数体内联编码**
   *   （`btoa(String.fromCharCode(...salt))`，用来把 salt/密钥材料存进
   *   localStorage）也判成了「第 2、3 份实现」。
   *   ⭐ 而它们**不该**收编 —— 那不是可复用工具，是业务流程里顺手的一步。
   *   ⇒ 「bytes→base64」这个**形状**天然会在多处出现（任何把字节存成字符串的
   *   地方都得这么写），所以形状不是判据，**「这几处收编过」才是**。
   */
  const RECLAIMED = [
    ['native/recording-audio-transcode.ts', /function\s+arrayBufferToBase64/,
      '它是录音转写上传链路那份（§889 收编自它）'],
    ['utils/download-encoding.ts', /function\s+arrayBufferToBase64/,
      '它是第二份公共实现（§889 收编自它）'],
    ['features/email/email-image-preload.ts', /function\s+bytesToBase64/,
      '它原本叫 bytesToBase64，用 apply(null, Array.from(...)) 多一次拷贝'],
    // ⚠ email-body-format 那份**有意**留了一个薄包装 `b64FromBytes`：
    //   它套的 `catch → ''` 是既有行为（§889 不代拍，改它属行为变更，简报 112）。
    //   ⇒ 这里判的是**体内不再自己拼 base64**，而不是「函数不许存在」——
    //   否则就把「保留既有 try/catch 外壳」也判成了回归（第一版就是这么写错的，
    //   而且它当场把自己刚写的收编判成了红）。
    ['features/email/email-body-format.ts', /\bbtoa\(/,
      '它体内又出现 btoa( ⇒ 又自己拼了一遍 base64（薄包装里应直接调公共实现）'],
  ]

  const assertScanned = () => {
    assert.ok(walk(SRC_ROOT).length >= 100, `只扫到 ${walk(SRC_ROOT).length} 个源文件 —— 扫描器坏了`)
  }

  it('★★★ 阳性对照：扫描器能看到全仓（前置闸）', () => {
    assertScanned()
  })

  it('★★★ 四个收编落点都不许再自带 bytes→base64 实现', () => {
    assertScanned()
    const back = []
    for (const [file, pat, why] of RECLAIMED) {
      const src = strip(readFileSync(resolve(SRC_ROOT, file), 'utf8'))
      if (pat.test(src)) back.push(`${file} —— ${why}`)
    }
    assert.deepEqual(back, [],
      `这些收编过的落点又自带了一份实现：\n  - ${back.join('\n  - ')}\n` +
      '公共实现在 utils/base64.ts（arrayBufferToBase64 / blobToBase64），请改成 import。')
  })

  it('★★★ 公共实现必须保留分块（整块传会在大文件上 RangeError）', () => {
    const src = strip(readFileSync(resolve(SRC_ROOT, 'utils/base64.ts'), 'utf8'))
    assert.match(src, /CHUNK|chunk/,
      'arrayBufferToBase64 里看不到分块常量 ⇒ String.fromCharCode 超参会抛 RangeError')
  })

  it('★★★ 录音链路那份必须 re-export 且带 .ts（§881 的判据在 node 里 import 它）', () => {
    assertScanned()
    const src = strip(readFileSync(resolve(SRC_ROOT, 'native/recording-audio-transcode.ts'), 'utf8'))
    // ⚠ 这里**必须带扩展名**：§881 的判据在 node 里 import 这个模块，而
    //   `node --experimental-strip-types` 不接受无扩展名 import（§875）。
    assert.match(src, /export \{ arrayBufferToBase64 \} from '\.\.\/utils\/base64\.ts'/,
      're-export 必须写成 `export { arrayBufferToBase64 } from \'../utils/base64.ts\'`，' +
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
    // ⇒ 「bytes→base64」这个形状天然会在多处出现，所以形状不是判据。
    const cryptoSrc = strip(readFileSync(resolve(SRC_ROOT, 'native/crypto.ts'), 'utf8'))
    assert.match(cryptoSrc, /btoa\(String\.fromCharCode\(/,
      'crypto.ts 的内联编码不见了 ⇒ 若它被改成了别的形态，请重新评估「形状不是判据」这个前提')
  })
})
