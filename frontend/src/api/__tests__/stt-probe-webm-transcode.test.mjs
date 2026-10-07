/**
 * stt-probe-webm-transcode.test.mjs — 设置页「试转」的 webm→WAV 转码（缺陷8 后半截）。
 *
 * 缺陷8 分两半，服务端那一半在 backend/internal/server/stt_probe_json_body_test.go；
 * 这里锁的是**客户端**那一半。
 *
 * ── 现象（2026-10-06 真机复现，Redmi 2411DRN47C / Android 14 WebView）──────────
 *   设置页 → STT → 点「试转」（现场录 3 秒）→ 永远失败。
 *   SettingsSTT.vue:529 显式挑 `audio/webm`
 *   （`MediaRecorder.isTypeSupported('audio/webm') ? 'audio/webm' : ''`，
 *     Android WebView 支持），于是 blob.type = audio/webm，
 *     `filenameForMimeType` 映射成 `recording.webm`。
 *   而网关的 chat-audio bridge 只收 mp3/wav，实测原样发过去：
 *     invalid_audio_request: audio format "webm" is not supported
 *       by the chat-audio bridge (supported: mp3, wav)
 *
 * ── 为什么不能只在服务端解 ─────────────────────────────────────────────────
 *   仓里早就有 `ensureGatewayCompatible`（utils/wav-encode.ts：
 *   AudioContext.decodeAudioData → 重采样 16k 单声道 → 16-bit PCM WAV），
 *   `api/stt.ts:67` 的转写路径一直在用；`sttSettingsApi.probe` 是漏掉的那一处。
 *   修法是复用同一个函数，不新造转码器。
 *
 * ★ 本门是**行为门**：打桩 globalThis.fetch 与 window.AudioContext，
 *   真的跑一遍 probe，断言发出去的 filename 与音频字节。
 *   源级 grep 判不出「filename 跟的是转码后的 blob 还是原始 blob」——
 *   两种写法在文本上只差一个变量名。
 *
 * 为什么先 esbuild 预打包再 import：
 *   src/api/* 内部用**无扩展名** import（`from './http'`），Node 的 ESM
 *   解析器要求显式扩展名，直接 import 会 ERR_MODULE_NOT_FOUND
 *   （仓里 flashcardIo.test.ts 正是因为这个进了 test-coverage 豁免名单）。
 *   这里在测试进程里现打一个 bundle 再 import，源码一个字都不用改。
 *   做法与 scripts/check-router-runtime-parity.mjs 相同。
 */

import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { afterEach, describe, it } from 'node:test'
import { createRequire } from 'node:module'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const API_DIR = path.resolve(HERE, '..')
const STT_SETTINGS = path.join(API_DIR, 'stt-settings.ts')
// 落在 node_modules/.cache 下：external 的 pinia/vue 要能从 bundle 所在位置向上解析到，
// 放 /tmp 会 ERR_MODULE_NOT_FOUND。node_modules 本身已 gitignore。
const TMP_BUNDLE = path.resolve(API_DIR, '..', '..', 'node_modules', '.cache', 'opk-stt-settings-test-bundle.mjs')
// 本文件是 ESM，没有 require；用 createRequire 按 frontend/ 的 package.json 解析 esbuild。
const requireFrom = createRequire(path.resolve(API_DIR, '..', '..', 'package.json'))
const ESBUILD = requireFrom.resolve('esbuild')

/** 现打一个 bundle 并 import（绕过无扩展名 import 的 ESM 解析限制）。 */
async function loadSttSettings() {
  // 必须在 import 之前激活：api 链上 stores/connectivity 是模块级 store。
  // ★ 必须用 ESM 的 import()，不能用 requireFrom('pinia')：require 走 CJS、
  //   bundle 走 ESM，那是 pinia 的**两个不同模块实例**，setActivePinia 设在 CJS 那份上，
  //   bundle 里的 store 自然还是「no active Pinia」。
  const { createPinia, setActivePinia } = await import('pinia')
  setActivePinia(createPinia())
  fs.mkdirSync(path.dirname(TMP_BUNDLE), { recursive: true })
  execFileSync(process.execPath, ['-e', `
    const esbuild = require(${JSON.stringify(ESBUILD)});
    esbuild.build({
      entryPoints: [${JSON.stringify(STT_SETTINGS)}],
      bundle: true, format: 'esm', platform: 'browser', write: false,
      // pinia/vue 必须 external：否则 bundle 会内联自己那份 pinia，
      // 测试里 setActivePinia() 激活的是另一个实例，
      // 症状是 import 阶段就 getActivePinia() 抛「no active Pinia」。
      external: ['pinia', 'vue'],
      outfile: ${JSON.stringify(TMP_BUNDLE)}, logLevel: 'silent',
    }).then(r => require('fs').writeFileSync(${JSON.stringify(TMP_BUNDLE)}, r.outputFiles[0].contents))
      .catch(e => { console.error(e.message); process.exit(1) })
  `], { cwd: path.resolve(API_DIR, '..', '..'), stdio: ['ignore', 'inherit', 'inherit'] })
  return import('file://' + TMP_BUNDLE + '?t=' + process.pid)
}

/** 造一个 decodeAudioData 可解的假 AudioContext：返回已知长度的单声道 Float32。 */
/** 最小 window 桩：模块链里有地方监听 online/offline，只给桩会让整条 import 链炸。 */
function stubWindow(AudioContextClass) {
  return {
    AudioContext: AudioContextClass,
    addEventListener() {},
    removeEventListener() {},
    removeAllListeners() {},
    location: { href: 'https://localhost/', origin: 'https://localhost' },
    navigator: { userAgent: 'node-test' },
  }
}

function stubAudioContext(frames = 3200) {
  const data = new Float32Array(frames)
  for (let i = 0; i < frames; i++) data[i] = Math.sin(i / 10) * 0.5
  return class FakeAudioContext {
    constructor() { this.sampleRate = 48000 }
    async decodeAudioData() {
      return {
        sampleRate: 48000,
        length: frames,
        numberOfChannels: 1,
        getChannelData: () => data,
      }
    }
    async close() {}
  }
}

const realFetch = globalThis.fetch
const realWindow = globalThis.window
const realFileReader = globalThis.FileReader
const realLocalStorage = globalThis.localStorage
afterEach(() => {
  globalThis.fetch = realFetch
  globalThis.window = realWindow
  globalThis.FileReader = realFileReader
  globalThis.localStorage = realLocalStorage
})

/** Node 没有 localStorage；api 链上读 token / api base 都要它。 */
function installLocalStorage() {
  const map = new Map()
  globalThis.localStorage = {
    getItem: (k) => (map.has(k) ? map.get(k) : null),
    setItem: (k, v) => map.set(k, String(v)),
    removeItem: (k) => map.delete(k),
    clear: () => map.clear(),
  }
}

/**
 * Node 22 没有全局 FileReader，而 blobToBase64 用它读 Blob。
 * 打一个只够用的：readAsDataURL 走 Blob.arrayBuffer()，产出标准 data URL。
 */
function installFileReader() {
  globalThis.FileReader = class NodeFileReader {
    readAsDataURL(blob) {
      blob.arrayBuffer().then((buf) => {
        this.result = `data:${blob.type || 'application/octet-stream'};base64,${Buffer.from(buf).toString('base64')}`
        this.onloadend?.({ type: 'loadend' })
      })
    }
  }
}

/** 打桩 fetch，抓下真正发出去的请求体。 */
function captureRequest() {
  const seen = { calls: 0, body: null, url: null }
  globalThis.fetch = async (url, init) => {
    seen.calls++
    seen.url = String(url)
    seen.body = init && init.body ? JSON.parse(String(init.body)) : null
    return {
      ok: true,
      status: 200,
      // http.ts 的 assertNotHTML 会读 content-type；桩里没有 headers 就 TypeError。
      headers: { get: () => 'application/json' },
      json: async () => ({ ok: true, text: '试转结果', model: 'mimo-v2.5-asr' }),
      text: async () => JSON.stringify({ ok: true, text: '试转结果' }),
    }
  }
  return seen
}

function decodeB64(s) {
  return Buffer.from(s, 'base64')
}

describe('设置页试转：webm 必须先转 WAV 再发', () => {
  it('webm blob → 请求体 filename 变成 recording.wav，且音频是合法 RIFF/WAVE', async () => {
    globalThis.window = stubWindow(stubAudioContext())
    installFileReader()
    installLocalStorage()
    const seen = captureRequest()
    const { sttSettingsApi } = await loadSttSettings()

    const webm = new Blob([new Uint8Array([0x1a, 0x45, 0xdf, 0xa3, 1, 2, 3, 4])], { type: 'audio/webm' })
    const res = await sttSettingsApi.probe(webm, { channel: 'gateway', model: 'mimo-v2.5-asr' })

    assert.equal(res.ok, true, 'probe 应返回 ok')
    assert.equal(seen.calls, 1, '应发出 1 次请求')
    assert.ok(seen.body && seen.body.audioBase64, '请求体应有 audioBase64')

    // ★ 判别力核心：文件名必须跟着**转码后**的容器走。
    // 修复前这里是 recording.webm ⇒ 服务端挑 webm bridge ⇒ 网关直接拒收。
    assert.equal(
      seen.body.filename, 'recording.wav',
      `★ filename 应是转码后的 recording.wav，实得 ${seen.body.filename}（= 修复前形态）`,
    )
    const audio = decodeB64(seen.body.audioBase64)
    assert.equal(audio.subarray(0, 4).toString('latin1'), 'RIFF', '音频应是 RIFF 容器')
    assert.equal(audio.subarray(8, 12).toString('latin1'), 'WAVE', '音频应是 WAVE 容器')
    assert.equal(audio.readUInt16LE(22), 1, '应为单声道')
    assert.equal(audio.readUInt16LE(34), 16, '应为 16-bit PCM')
    assert.equal(audio.readUInt32LE(24), 16000, '应重采样到 16kHz（网关 ASR 的标准输入）')
  })

  it('wav blob 不被重复转码（回归：直传就够）', async () => {
    globalThis.window = stubWindow(stubAudioContext())
    installFileReader()
    installLocalStorage()
    const seen = captureRequest()
    const { sttSettingsApi } = await loadSttSettings()

    const wav = new Blob([Buffer.from('RIFF0000WAVEfmt fake-wav-bytes')], { type: 'audio/wav' })
    await sttSettingsApi.probe(wav, { channel: 'gateway', model: 'mimo-v2.5-asr' })

    assert.equal(seen.body.filename, 'recording.wav', 'wav 应直传且名不变')
    assert.equal(
      decodeB64(seen.body.audioBase64).subarray(0, 4).toString('latin1'), 'RIFF',
      'wav 不该再被 decodeAudioData 重编（字节应保持原样）',
    )
  })

  it('转码失败时回退原样上传（不能因此让试转彻底不可用）', async () => {
    // decodeAudioData 直接抛错：ensureGatewayCompatible 约定回退原 blob
    globalThis.window = stubWindow(class {
      async decodeAudioData() { throw new Error('decode failed') }
      async close() {}
    })
    installFileReader()
    installLocalStorage()
    const seen = captureRequest()
    const { sttSettingsApi } = await loadSttSettings()

    const webm = new Blob([new Uint8Array([0x1a, 0x45, 0xdf, 0xa3])], { type: 'audio/webm' })
    const res = await sttSettingsApi.probe(webm, { channel: 'gateway', model: 'mimo-v2.5-asr' })

    assert.equal(res.ok, true, '转码失败也不该让调用方拿到异常')
    assert.equal(seen.calls, 1, '回退后仍要发出请求（外部 OpenAI 通道支持 webm）')
    assert.equal(seen.body.filename, 'recording.webm', '回退时文件名保持 webm（此时它就是真实容器）')
  })
})

describe('负控：把修复改回去必须报红', () => {
  /** 源级检测：probe 的 filename 必须取自**转码后**的变量。 */
  function findProbeFilenameRisks(src) {
    const risks = []
    const at = src.indexOf('async probe(')
    if (at < 0) return ['sttSettingsApi.probe 声明没找到（门形同虚设）']
    const body = src.slice(at, src.indexOf('\n  },', at))
    if (!/ensureGatewayCompatible\s*\(/.test(body)) {
      risks.push('probe 没有调用 ensureGatewayCompatible —— webm 会原样发出，被网关拒收')
    }
    // 不变量：filename 必须跟着**转码结果变量**（const out = await ensureGatewayCompatible(in) 的 out）走。
    // 第一版把 out 写成了 in（ensureGatewayCompatible 的入参），于是自己的正确源码被判红。
    // 这两者恰好是修复前/修复后的唯一差别，所以这里必须比 out，不是比 in。
    const conv = body.match(/const\s+([A-Za-z_$][\w$]*)\s*=\s*await\s+ensureGatewayCompatible\s*\(/)
    const fn = body.match(/filename:\s*filenameForMimeType\s*\(\s*([A-Za-z_$][\w$]*)\.type\s*\)/)
    if (!conv) {
      risks.push('解析不出 `const out = await ensureGatewayCompatible(...)` 的 out 变量')
    } else if (!fn) {
      risks.push('解析不出 `filename: filenameForMimeType(<var>.type)` 的变量')
    } else if (fn[1].trim() !== conv[1].trim()) {
      risks.push(
        `filename 跟的是 ${fn[1].trim()}，而转码结果是 ${conv[1].trim()} —— `
        + '两者必须同一个变量，否则发出去的文件名与实际容器不符（修复前形态）',
      )
    }
    return risks
  }

  it('当前源码无风险', () => {
    const src = fs.readFileSync(STT_SETTINGS, 'utf8')
    assert.deepEqual(findProbeFilenameRisks(src), [])
  })

  it('负控1：拿掉转码调用 → 报红', () => {
    const src = fs.readFileSync(STT_SETTINGS, 'utf8')
    const mutated = src.replace(
      /const audioBlob = await ensureGatewayCompatible\(audio\)/,
      'const audioBlob = audio /* 变异：去掉转码 */',
    )
    assert.notEqual(mutated, src, '变异没生效：先看源码再改门')
    const risks = findProbeFilenameRisks(mutated)
    assert.ok(risks.length, '★ 门有洞：拿掉转码竟然判为安全')
    assert.ok(risks.some((r) => /ensureGatewayCompatible/.test(r)))
  })

  it('负控2：filename 改回跟原始 audio 走 → 报红（这正是修复前的形态）', () => {
    const src = fs.readFileSync(STT_SETTINGS, 'utf8')
    const mutated = src.replace('filename: filenameForMimeType(audioBlob.type)', 'filename: filenameForMimeType(audio.type)')
    assert.notEqual(mutated, src, '变异没生效：filename 那行结构与预期不符')
    const risks = findProbeFilenameRisks(mutated)
    assert.ok(risks.length, '★ 门有洞：filename 跟原始 audio 竟然判为安全')
    assert.ok(
      risks.some((r) => /filename 跟的是 audio\b/.test(r)),
      `门红了但红得不对，要的是「变量不一致」那条，实际：${JSON.stringify(risks)}`,
    )
  })

  it('负控3：设置页仍固定挑 audio/webm（前提判据）', () => {
    // 这是缺陷8 触发源：若哪天改成录 wav 了，本门的必要性下降（但不失效）。
    const vue = fs.readFileSync(
      path.resolve(API_DIR, '..', 'features', 'settings', 'SettingsSTT.vue'), 'utf8',
    )
    assert.ok(
      /isTypeSupported\('audio\/webm'\)/.test(vue),
      '设置页不再挑 audio/webm —— 请同步复核本门的前提是否仍成立',
    )
  })
})