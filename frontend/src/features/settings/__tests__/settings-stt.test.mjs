/**
 * 语音转写设置页的行为锁定（2026-10-01）。
 *
 * 锁的是三件容易被改坏的事：
 *   1. 设置页必须**如实展示网关探测结论**，不能把 no_candidate 说成可用。
 *      2026-10-01 实测 llm.kxpms.cn 的 gpt-audio / gpt-audio-mini /
 *      mimo-v2.5-asr 全部 503 no_candidate —— 目录里有 ≠ 能用。
 *   2. 推荐模型必须**两组都在**（网关组 + 外部组），且外部组要能一键选中。
 *   3. 必须有「用真实录音试转」的入口：自动发现只验证连通性，
 *      识别准不准只能靠真实语音判断。
 */
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

const HERE = dirname(fileURLToPath(import.meta.url))
const read = (rel) => readFileSync(join(HERE, rel), 'utf8')

const view = read('../SettingsSTT.vue')
const api = read('../../../api/stt-settings.ts')
const router = read('../../../app/router-mobile.ts')
const settingsView = read('../SettingsView.vue')

describe('语音转写设置页', () => {
  it('路由已注册且从设置页可进入', () => {
    assert.match(router, /path: '\/settings\/stt'/, '缺少 /settings/stt 路由')
    assert.match(router, /SettingsSTT\.vue/, '路由未指向 SettingsSTT.vue')
    assert.match(settingsView, /openSttEditor/, '设置页缺少跳转函数')
    assert.match(settingsView, /\/settings\/stt/, '设置页未跳转到语音转写')
  })

  it('三个通道都要有中文说明，用户才分得清差别', () => {
    for (const value of ['auto', 'gateway', 'external']) {
      assert.ok(
        new RegExp(`value: '${value}'`).test(view),
        `通道 ${value} 缺选项`,
      )
    }
    // 说明文案来自后端 channelHints，页面必须有地方展示它。
    assert.match(view, /channelHint/, '未渲染通道说明')
  })

  it('网关候选逐个展示真实探测状态，且只有 ok 才可选', () => {
    assert.match(view, /discovery\?\.candidates\?\.length/, '未渲染候选列表')
    assert.match(view, /describeStatus\(c\)/, '未展示探测状态')
    // 不可用的候选必须禁用单选框：不能让用户选一个必然失败的模型。
    assert.match(
      view,
      /:disabled="!isUsable\(c\)"/,
      '不可用候选仍可被选中',
    )
  })

  it('网关未配置 key 时给出明确指引，而不是空白页', () => {
    assert.match(view, /stt-gateway-nokey/, '缺少网关无 key 的提示块')
    assert.match(view, /AI 模型/, '未指引用户去哪里配网关')
  })

  it('推荐模型分两组展示，点击即选中', () => {
    assert.match(view, /recommendedGroups/, '未按组组织推荐模型')
    assert.match(view, /网关模型（llm\.kxpms\.cn 模型目录）/, '缺网关组标题')
    assert.match(view, /外部服务模型（网络调研推荐）/, '缺外部组标题')
    assert.match(view, /@click="pickRecommended\(m\)"/, '推荐项不可点击')
    // 外部组必须显示成本，且没有报价时要明说「无公开报价」而不是显示 0。
    assert.match(view, /无公开报价/, '未报价模型没有明确标注')
    assert.match(view, /usdPerHour\.toFixed\(2\)/, '未展示每小时成本')
  })

  it('外部服务地址与 key 可手工填写，key 不回显', () => {
    assert.match(view, /stt-ext-base/, '缺外部服务地址输入')
    assert.match(view, /stt-ext-key/, '缺外部服务 key 输入')
    assert.match(view, /已设置（留空保留）/, '未提示 key 已保存不回显')
  })

  it('提供「录 3 秒试转」，且结果与失败都可见', () => {
    assert.match(view, /stt-probe-record/, '缺试转录音按钮')
    assert.match(view, /stt-probe-result/, '缺试转结果展示')
    assert.match(view, /stt-probe-error/, '缺试转失败展示')
    // 3 秒自动停止：让「试转」不需要用户精确掌握时长。
    assert.match(view, /setTimeout\(\(\) => mediaRecorder\?\.stop\(\), 3000\)/, '试转未自动停止')
  })

  it('试转失败必须显示后端原因，不吞错误', () => {
    // 后端失败也返回 200 + {ok:false,error}，页面必须按 ok 分流并展示 error。
    assert.match(view, /if \(res\.ok\)/, '未按 ok 分流试转结果')
    assert.match(view, /probeError\.value = res\.error/, '未展示后端返回的失败原因')
    assert.match(
      view,
      /res\.error \|\| '试转失败（上游没有返回原因）'/,
      '后端没给原因时应有兜底文案',
    )
  })
})

describe('探测状态映射', () => {
  it('五种状态都有中文说明', () => {
    for (const label of ['可用', '网关无上游 provider', '无转写端点', '上游丢弃音频', '未探测']) {
      assert.ok(
        new RegExp(label.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).test(api),
        `状态说明缺「${label}」`,
      )
    }
  })

  it('只有 ok 算可用——no_candidate / 丢音频都不算', () => {
    assert.match(
      api,
      /function isSttCandidateUsable[\s\S]{0,120}return c\.status === 'ok'/,
      '可用判定不是严格等于 ok',
    )
  })
})

describe('STT API 客户端', () => {
  it('四个端点都接上了，且探测/试转给足超时', () => {
    for (const p of ['/api/stt/config', '/api/stt/discover', '/api/stt/probe']) {
      assert.ok(api.includes(p), `未调用 ${p}`)
    }
    // 试转与扫描都要等模型推理，30s 默认超时不够。
    const longCalls = api.match(/LONG_REQUEST_TIMEOUT_MS/g) || []
    assert.ok(longCalls.length >= 2, '探测/试转未使用长超时')
  })

  it('试转走 JSON base64，与既有 /api/stt/transcribe 契约一致', () => {
    assert.match(api, /audioBase64/, '试转未用 base64 音频')
    assert.match(api, /filenameForMimeType/, '试转未按 MIME 推导文件名')
  })
})
