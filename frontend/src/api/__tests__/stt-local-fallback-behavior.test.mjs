// stt-local-fallback-behavior.test.mjs — 层3修复（2026-11-14）的**行为**门。
//
// ★ 为什么必须做行为门而不止是源码扫描
//
// `sttApi.transcribe` 的「本地 sherpa 优先 → 云端兜底 → 云端失败时本地低置信兜底」
// 是「生产 STT 未配置时会议录音仍出字」这条需求的**唯一实现点**。
// 昨天的 root cause 正是「链路三层都断」却全绿（stt-local-first-claim-gate
// 只看注释、看不了行为）——源码扫描判据分不清「分支存在」和「值真的流过去」。
//
// ★ 为什么用 esbuild 打包期替桩而不是 node mock
//
// `stt.ts` import 的 `./http`（拖 config/api-base）、`../native/sherpa`
// （拖 @capacitor/core）在 node 下都 ERR_MODULE_NOT_FOUND（§52 同一个坑）。
// 但「本地结果留不留、云端挂了抛不抛」这些**决策本身是纯的**。
// ⇒ 打包期把 sherpa/http 换成内存假件，拿真函数跑，决策层就有行为门。
// 与 ingest-behavior.test.mjs（§86）同一做法：不改生产代码结构，改测试的装载方式。
import assert from 'node:assert/strict'
import { describe, it, before } from 'node:test'
import path from 'node:path'
import fs from 'node:fs'
import os from 'node:os'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { build } from 'esbuild'

// ★ 写入口的路径归一（§ingest-behavior 同款纪律）：
//   正斜杠化 + JSON.stringify（做字符串字面量转义）。注释里绝不能出现反引号
//   （Node 的 TS stripper 会把 // 注释里的反引号当成模板串开头）。
function asModulePath(p) {
  return JSON.stringify(String(p).replace(/\\/g, '/'))
}

const HERE = path.dirname(fileURLToPath(import.meta.url))
const SRC = path.resolve(HERE, '..') // → src/api
const WORK = fs.mkdtempSync(path.join(os.tmpdir(), 'pocket-stt-'))

// 假件记录下来的东西，供断言用。挂 globalThis 是因为 stub 是独立模块文件，
// 拿不到本文件的闭包变量。
const seen = {
  sherpaCalls: [],      // sherpa.transcribe 收到的 audioPath
  httpCalls: [],        // http() 收到的 url + method
  sherpaResult: null,   // 本次用例让 sherpa 返回什么
  sherpaThrows: null,   // 本次用例让 sherpa 抛什么
  httpResult: null,     // 本次用例让 http 返回什么
  httpThrows: null,     // 本次用例让 http 抛什么
  policyLocal: false,   // assertCloudModule 是否抛 ModulePolicyLocalError（模块策略=local）
}
globalThis.__sttSeen = seen

/** import 说明 → 假件源码。⚠ 桩必须与真模块同形（导出集对得上）。 */
const STUB_SOURCES = {
  '../native/sherpa': `
    export const sherpa = {
      async transcribe(audioPath) {
        globalThis.__sttSeen.sherpaCalls.push(audioPath)
        if (globalThis.__sttSeen.sherpaThrows) throw globalThis.__sttSeen.sherpaThrows
        return globalThis.__sttSeen.sherpaResult
      },
      async startListening() { throw new Error('stub') },
      async stopListening() { throw new Error('stub') },
    }`,
  './http': `
    export async function http(url, opts) {
      globalThis.__sttSeen.httpCalls.push({ url, method: opts && opts.method })
      if (globalThis.__sttSeen.httpThrows) throw globalThis.__sttSeen.httpThrows
      return globalThis.__sttSeen.httpResult
    }`,
  './data-policy.ts': `
    export class ModulePolicyLocalError extends Error {
      constructor(module) {
        super('module_policy_local')
        this.name = 'ModulePolicyLocalError'
        this.body = { error: 'module_policy_local', module }
      }
    }
    // ⚠ 桩默认放行；「策略禁云」用例通过 globalThis.__sttSeen.policyLocal 开关。
    export function assertCloudModule(module) {
      if (globalThis.__sttSeen.policyLocal) throw new ModulePolicyLocalError(module)
    }`,
  './abort.ts': `
    export function isAbortError(e) {
      return !!e && typeof e === 'object' && e.name === 'AbortError'
    }`,
  // ⚠ 键必须带 .ts 后缀：stt.ts 的 import 写的是 './data-policy.ts' 等**带扩展名**
  //   的说明符，桩键不带后缀的话 onResolve 的 $ 锚不命中 ⇒ 真模块被打进包 ⇒
  //   真 data-policy 读不到策略存储抛 ModulePolicyLocalError ⇒ 断言全歪。
  //   （2026-11-14 实测踩到，第一批 8 红全源于此。）
  './stt-cloud.ts': `
    export const CLOUD_STT_NEED_BLOB = 'CLOUD_STT_NEED_BLOB'
    export function requireCloudAudioBlob(opts) {
      if (!opts.audioBlob) throw new Error(CLOUD_STT_NEED_BLOB)
      return opts.audioBlob
    }`,
  '../utils/wav-encode': `
    export async function ensureGatewayCompatible(blob) { return blob }`,
  '../utils/base64': `
    export async function blobToBase64() { return 'c3R1Yg==' }`,
  '../utils/text': `
    export function utf8ByteLength(s) { return s.length }`,
  './stt-filename': `
    export function filenameForMimeType() { return 'audio.webm' }`,
}

const stubFiles = new Map()
for (const [spec, code] of Object.entries(STUB_SOURCES)) {
  const f = path.join(WORK, spec.replace(/[^a-zA-Z0-9]/g, '_') + '.mjs')
  fs.writeFileSync(f, code, 'utf8')
  stubFiles.set(spec, f)
}

let sttApi

before(async () => {
  const entry = path.join(WORK, 'entry.ts')
  fs.writeFileSync(entry, `export { sttApi } from ${asModulePath(path.resolve(SRC, 'stt.ts'))}\n`)
  await build({
    entryPoints: [entry],
    bundle: true,
    format: 'esm',
    platform: 'neutral',
    mainFields: ['module', 'main'],
    outfile: path.join(WORK, 'out.mjs'),
    logLevel: 'error',
    plugins: [{
      name: 'stub-io',
      setup(b) {
        for (const [spec, file] of stubFiles) {
          const re = new RegExp(spec.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '$')
          b.onResolve({ filter: re }, () => ({ path: file }))
        }
      },
    }],
  })
  sttApi = (await import(pathToFileURL(path.join(WORK, 'out.mjs')).href)).sttApi
})

function reset() {
  seen.sherpaCalls.length = 0
  seen.httpCalls.length = 0
  seen.sherpaResult = null
  seen.sherpaThrows = null
  seen.httpResult = null
  seen.httpThrows = null
  seen.policyLocal = false
}

const BLOB = new Blob(['stub'], { type: 'audio/wav' })

describe('层3修复 · sttApi.transcribe 的本地/云端决策行为', () => {
  it('★ 本地高置信 ⇒ 直接返回 engine=local，不发云端请求', async () => {
    reset()
    seen.sherpaResult = { text: '今天下午三点开会', confidence: 0.9, rtf: 0.3, engine: 'zipformer' }
    const res = await sttApi.transcribe({ audioBlob: BLOB, audioPath: '/data/part-1.wav', module: 'meetings' })
    assert.equal(res.engine, 'local')
    assert.equal(res.text, '今天下午三点开会')
    assert.deepEqual(seen.httpCalls, [], '本地高置信还发云端请求 ⇒ 白花流量/成本')
  })

  it('★ 本地低置信 + 云端正常 ⇒ 返回云端结果（本地只作预留）', async () => {
    reset()
    seen.sherpaResult = { text: '大概是这样吧', confidence: 0.4, rtf: 0.3, engine: 'zipformer' }
    seen.httpResult = { text: '大概是这样的', confidence: 0.95, costCents: 1 }
    const res = await sttApi.transcribe({ audioBlob: BLOB, audioPath: '/data/part-2.wav', module: 'meetings' })
    assert.equal(res.engine, 'cloud')
    assert.equal(res.text, '大概是这样的')
    assert.equal(seen.httpCalls.length, 1)
  })

  it('★★ 本地低置信 + 云端失败 ⇒ 降级返回本地结果（不向上抛 —— 层3修复的核心承诺）', async () => {
    reset()
    seen.sherpaResult = { text: '大概是这样吧', confidence: 0.4, rtf: 0.3, engine: 'zipformer' }
    seen.httpThrows = new Error('STT cloud not configured')
    const res = await sttApi.transcribe({ audioBlob: BLOB, audioPath: '/data/part-3.wav', module: 'meetings' })
    assert.equal(res.engine, 'local', '云端失败时丢弃了本地结果 ⇒ 整段丢字（生产实测现场）')
    assert.equal(res.text, '大概是这样吧')
  })

  it('★★ 本地无结果 + 云端失败 ⇒ 原样抛错（没有可降级的货就不许伪造）', async () => {
    reset()
    seen.sherpaThrows = new Error('unsupported_format')
    seen.httpThrows = new Error('STT cloud not configured')
    await assert.rejects(
      sttApi.transcribe({ audioBlob: BLOB, audioPath: '/data/part-4.wav', module: 'meetings' }),
      /STT cloud not configured/,
    )
  })

  it('★★ 本地有文本但云端失败 + 用户中止 ⇒ 中止原样抛出（不许把取消偷换成降级结果）', async () => {
    reset()
    seen.sherpaResult = { text: '大概是这样吧', confidence: 0.4, rtf: 0.3, engine: 'zipformer' }
    const abortErr = new Error('user cancelled')
    abortErr.name = 'AbortError'
    seen.httpThrows = abortErr
    await assert.rejects(
      sttApi.transcribe({ audioBlob: BLOB, audioPath: '/data/part-5.wav', module: 'meetings' }),
      (e) => e.name === 'AbortError',
      '中止被吞成降级结果 ⇒ 用户取消的那一段被悄悄写库',
    )
  })

  it('★ 不传 audioPath（blob-only 调用方，Web 链）⇒ 行为不变：一律云端', async () => {
    reset()
    seen.httpResult = { text: '云端结果', confidence: 0.9 }
    const res = await sttApi.transcribe({ audioBlob: BLOB, module: 'meetings' })
    assert.equal(res.engine, 'cloud')
    assert.deepEqual(seen.sherpaCalls, [], '没传 audioPath 还调了 sherpa ⇒ 拿 undefined 当路径')
  })

  it('★ forceEngine=cloud ⇒ 跳过本地（调用方显式要云端的权力保留）', async () => {
    reset()
    seen.httpResult = { text: '云端结果', confidence: 0.9 }
    await sttApi.transcribe({ audioBlob: BLOB, audioPath: '/data/part-6.wav', forceEngine: 'cloud', module: 'meetings' })
    assert.deepEqual(seen.sherpaCalls, [])
  })
})

describe('层3修复 · 决策链路的输入事实（负控）', () => {
  it('★★ 负控：低置信兜底判据真的依赖「云端失败」——云端正常时本地低置信绝不出场', async () => {
    // 把用例 2 与用例 3 的差异钉死：唯一变量是 http 抛不抛。
    reset()
    seen.sherpaResult = { text: '大概是这样吧', confidence: 0.4, rtf: 0.3, engine: 'zipformer' }
    seen.httpResult = { text: '云端结果', confidence: 0.99 }
    const ok = await sttApi.transcribe({ audioBlob: BLOB, audioPath: '/data/neg.wav', module: 'meetings' })
    assert.equal(ok.engine, 'cloud')
    seen.httpThrows = new Error('boom')
    const degraded = await sttApi.transcribe({ audioBlob: BLOB, audioPath: '/data/neg.wav', module: 'meetings' })
    assert.equal(degraded.engine, 'local')
    assert.notEqual(ok.engine, degraded.engine, '同一份本地结果，云端活着却返回了 local ⇒ 降级条件写反')
  })

  it('★★ 负控：本地空文本不许充当降级结果（空字幕不是字幕）', async () => {
    reset()
    seen.sherpaResult = { text: '   ', confidence: 0.4, rtf: 0.3, engine: 'zipformer' }
    seen.httpThrows = new Error('STT cloud not configured')
    await assert.rejects(
      sttApi.transcribe({ audioBlob: BLOB, audioPath: '/data/empty.wav', module: 'meetings' }),
      /STT cloud not configured/,
      '本地空文本被当成降级结果返回 ⇒ 用户看到一段空白「字幕」，比报错更迷惑',
    )
  })

  it('★★ 模块策略 local（assertCloudModule 抛 ModulePolicyLocalError）+ 本地有文本 ⇒ 降级返回本地结果（真机 16:39 第四层故障现场）', async () => {
    reset()
    // 生产无 Portal ⇒ 策略恒 local ⇒ 断言在云端路径第一行就抛，http 根本不会被打到
    seen.policyLocal = true
    seen.sherpaResult = { text: '本地引擎的结果', confidence: 0.4, rtf: 0.3, engine: 'zipformer' }
    const res = await sttApi.transcribe({ audioBlob: BLOB, audioPath: '/data/local-mode.wav', module: 'meetings' })
    assert.equal(res.engine, 'local', '策略禁云时本地结果被 assertCloudModule 拦死 ⇒ 每段 segment failed（真机实测）')
    assert.equal(res.text, '本地引擎的结果')
    assert.deepEqual(seen.httpCalls, [], '策略 local 还发起了 http ⇒ 断言没拦住')
  })

  it('★ 模块策略 local + 本地无文本 ⇒ ModulePolicyLocalError 原样抛出（策略拒绝必须可见）', async () => {
    reset()
    seen.policyLocal = true
    seen.sherpaThrows = new Error('unsupported_format')
    await assert.rejects(
      sttApi.transcribe({ audioBlob: BLOB, audioPath: '/data/local-mode-none.wav', module: 'meetings' }),
      (e) => e.body?.error === 'module_policy_local' || /module_policy_local/.test(String(e)),
      '没有本地结果时策略错误被吞 ⇒ 上层不知道是「被禁」还是「坏了」',
    )
  })

  it('★ blob-only（无 audioPath）+ 策略 local ⇒ ModulePolicyLocalError 照常抛（不能伪造本地结果）', async () => {
    reset()
    seen.policyLocal = true
    await assert.rejects(
      sttApi.transcribe({ audioBlob: BLOB, module: 'meetings' }),
      /module_policy_local/,
    )
  })
})
