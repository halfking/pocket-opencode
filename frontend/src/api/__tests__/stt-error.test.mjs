/**
 * STT 失败原因展示规则的回归（2026-10-01）。
 *
 * 这条规则存在的原因：录音转写失败时，原来只写死「转写失败，将在下一段重试」，
 * 真实原因只进 console.warn；而改用 i18n 通用文案又会把后端那句可行动原因盖掉。
 * 窄口径是：**只放行带 stt_unavailable 错误码的整理文案**。
 */
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

const HERE = dirname(fileURLToPath(import.meta.url))
const read = (rel) => readFileSync(join(HERE, rel), 'utf8')

// stt-error.ts 是纯模块（无任何 import），node --test 可以直接加载
const { sttFailureText, STT_UNAVAILABLE_CODE } = await import('../stt-error.ts')

const FALLBACK = '转写失败，将在下一段重试'

/** 模拟 http.ts 抛出的 ApiError（body.error 才是后端原文）。 */
const apiError = (message, body) => ({ name: 'ApiError', message, body })

describe('sttFailureText', () => {
  it('放行带 stt_unavailable 码的整理原因，并剥掉错误码前缀', () => {
    const backend =
      'stt_unavailable: 网关暂无可用的语音转写模型（gpt-audio=网关无上游 provider）；' +
      '外部语音转写服务未配置 API Key（设置 → 语音转写）'
    const got = sttFailureText(apiError(backend, { error: backend }), FALLBACK)
    assert.equal(got, backend.slice('stt_unavailable:'.length).trim())
    assert.ok(!got.startsWith('stt_unavailable'), '错误码不该出现在界面上')
    assert.ok(got.includes('网关无上游 provider'), '可行动原因必须完整保留')
    assert.ok(got.includes('设置 → 语音转写'), '设置入口指引必须保留')
  })

  it('错误码前缀有空格/大小写差异也能识别', () => {
    const backend = 'stt_unavailable:   外部语音转写服务未配置 API Key（设置 → 语音转写）'
    const got = sttFailureText(apiError(backend, { error: backend }), FALLBACK)
    assert.equal(got, '外部语音转写服务未配置 API Key（设置 → 语音转写）')
  })

  it('没有稳定错误码的技术串一律走通用兜底（不把 dial tcp 甩给用户）', () => {
    const technical = [
      'stt gpt-4o-mini-transcribe 0: Post "https://api.openai.com/v1/audio/transcriptions": dial tcp 1.2.3.4:443: i/o timeout',
      'Failed to fetch',
      'TimeoutError: /api/stt/transcribe 超时（120000ms）',
      'panic: runtime error: invalid memory address',
    ]
    for (const t of technical) {
      assert.equal(sttFailureText(apiError(t, { error: t }), FALLBACK), FALLBACK, `不该展示：${t}`)
    }
  })

  it('空异常 / 非 Error 异常走兜底', () => {
    assert.equal(sttFailureText(null, FALLBACK), FALLBACK)
    assert.equal(sttFailureText(undefined, FALLBACK), FALLBACK)
    assert.equal(sttFailureText({}, FALLBACK), FALLBACK)
    assert.equal(sttFailureText('   ', FALLBACK), FALLBACK)
  })

  it('只有错误码没有原因时走兜底（不显示裸错误码）', () => {
    assert.equal(sttFailureText('stt_unavailable:', FALLBACK), FALLBACK)
    assert.equal(sttFailureText('stt_unavailable:    ', FALLBACK), FALLBACK)
  })

  it('超长原因中间省略，头尾都保留（2026-10-01 真机实测修正）', () => {
    // 后端文案的真实形状：头是原因，尾是行动指引。
    const long = 'stt_unavailable: 网关暂无可用的语音转写模型（mimo-v2.5-asr=网关无上游 provider；' +
      '逐候选诊断'.repeat(60) +
      '；外部语音转写服务未配置 API Key（设置 → 语音转写）'
    const got = sttFailureText(apiError(long, { error: long }), FALLBACK)
    assert.ok(got.length <= 161, `收敛后仍过长：${got.length}`)
    assert.ok(got.includes('…'), '超长必须收敛')
    // 头：为什么失败
    assert.ok(got.startsWith('网关暂无可用的语音转写模型'), '失败原因必须保留')
    // 尾：用户该做什么 —— 原来的硬砍尾巴正好把它砍没了
    assert.ok(got.endsWith('API Key（设置 → 语音转写）'), `行动指引被截断了：${got.slice(-40)}`)
  })

  it('支持字符串异常与 { message } 形态', () => {
    assert.equal(
      sttFailureText('stt_unavailable: 网关无上游 provider', FALLBACK),
      '网关无上游 provider',
    )
    assert.equal(
      sttFailureText({ message: 'stt_unavailable: 外部服务未配置 API Key' }, FALLBACK),
      '外部服务未配置 API Key',
    )
  })

  it('错误码常量与后端 / i18n 映射表同名', () => {
    assert.equal(STT_UNAVAILABLE_CODE, 'stt_unavailable')
  })
})

describe('录音链路确实用上了这条规则', () => {
  const runtime = read('../../native/recordingRuntime.ts')

  it('分片转写失败不再只写死通用文案', () => {
    assert.match(
      runtime,
      /this\.sttError\.value = sttFailureText\(e, '转写失败，将在下一段重试'\)/,
      '会议/笔记分片转写失败未透出真实原因',
    )
    // 原来这行是硬编码，必须消失
    assert.ok(
      !/this\.sttError\.value = '转写失败，将在下一段重试'/.test(runtime),
      '仍存在写死的通用文案赋值',
    )
  })

  it('停止链路的兜底转写也不再直接甩 e.message', () => {
    assert.match(runtime, /this\.error\.value = sttFailureText\(e, '转写失败'\)/, '停止链路未走 sttFailureText')
    assert.ok(
      !/this\.error\.value = e instanceof Error \? e\.message/.test(runtime),
      '停止链路仍直接使用原始 e.message',
    )
  })
})
