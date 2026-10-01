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
    // 成本必须走 formatCost 而不是内联 toFixed(2)：最便宜档是 $0.012/小时，
    // toFixed(2) 会显示成 $0.01，把 1 分钱档之间的差异抹平——而「尽可能费用少」
    // 正是这个字段存在的理由。见 src/api/stt-presentation.ts。
    assert.match(view, /formatCost\(m\.usdPerHour\)/, '未通过 formatCost 展示每小时成本')
    assert.equal(
      /usdPerHour\s*\.toFixed\(2\)/.test(view), false,
      '不得内联 toFixed(2)，低价 ASR 候选会被四舍五入成同一个价',
    )
  })

  it('展示「即时出字能力」与单次时长上限的取舍', () => {
    // 调研结论：便宜的 ASR 普遍不支持服务端真流式（OpenRouter 上游约 60 秒超时），
    // 「省钱」与「逐字出字」需要二选一。这个取舍必须在选模型时就看得见。
    assert.match(view, /streamingHint\(m\)/, '未提示流式能力差异')
    assert.match(view, /maxSecondsHint\(m\.maxSeconds\)/, '未提示单次时长上限')
  })

  it('展示录音语音提示的引擎可用性', () => {
    // 播报失败是静默的：没有 TTS 引擎时用户听不到声音，只会以为功能没做。
    // 国内 ROM 常移除 Google TTS，所以必须把结论显式摆在设置页。
    assert.match(view, /probeVoicePromptSupport/, '未探测语音提示可用性')
    assert.match(view, /voice-prompt-state/, '未展示语音提示状态')
  })

  it('模板标签配对完整（vue-tsc 不校验模板，构建才会报）', () => {
    // 2026-10-01 实测：编辑模板时吃掉了一个 <section>/<label> 开标签，
    // `vue-tsc --noEmit` 仍然 exit 0（它只查类型，不查标签配对），
    // 只有 vite build 报 Invalid end tag。源码级契约能提前一步。
    const opens = (view.match(/<section\b/g) || []).length
    const closes = (view.match(/<\/section>/g) || []).length
    assert.equal(opens, closes, `<section> 开闭标签不配对（${opens} 开 / ${closes} 闭）`)
    const labels = (view.match(/<label\b/g) || []).length
    const labelCloses = (view.match(/<\/label>/g) || []).length
    assert.equal(labels, labelCloses, `<label> 开闭标签不配对（${labels} 开 / ${labelCloses} 闭）`)
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

/**
 * 2026-10-01 真机回归：设置页把上游 503 响应体直接甩给用户。
 *
 * 证据（真机截图 logs/real-device-stt-20261001-033020/31-stt-settings-page.png）：
 * mimo-v2.5-asr 候选卡片下方渲染出五六行
 * `{"error":{"alternatives":{"requested_model":"mimo-v2.5-asr",…}}`。
 * 后端 `providerDetail` 是按字节截断到 300 字符，断在 JSON 中间反而更难读。
 *
 * 徽章已经说明了「网关无上游 provider」这个**状态**，原始响应对用户决策没有
 * 增量价值（要查细节看服务端日志），所以渲染层只放行人话细节。
 */
describe('候选详情不泄漏上游原始响应', () => {
  it('模板渲染的是 candidateDetail(c) 而不是裸 c.detail', () => {
    assert.match(
      view,
      /\{\{\s*candidateDetail\(c\)\s*\}\}/,
      '候选详情仍在直接渲染 c.detail',
    )
    assert.doesNotMatch(
      view,
      /candidate-detail[^>]*>\s*\{\{\s*c\.detail\s*\}\}/,
      '候选详情仍直接输出 c.detail',
    )
  })

  it('candidateDetail 对 JSON / 长 URL 细节返回空，对短句截断', () => {
    const fn = view.slice(view.indexOf('function candidateDetail'))
    // 整体是上游 JSON 就不显示
    assert.ok(fn.includes('/^[[{]/'), '未拦截以 { 或 [ 开头的上游响应体')
    // 截断在 JSON 中间（以 } 或 ] 结尾）同样不显示
    assert.ok(fn.includes('/[\\]}]$/'), '未拦截被截断的 JSON 尾巴')
    // 长 URL 技术串不显示
    assert.ok(fn.includes('/https?:\\/\\/\\S{40,}/'), '未拦截长 URL 技术串')
    // 短句截到 80 字
    assert.match(fn, /length > 80[\s\S]{0,80}slice\(0, 80\)/, '短句未截断到 80 字')
  })

  it('样式层双保险：detail 最多占两行', () => {
    const style = view.slice(view.lastIndexOf('<style'))
    const block = style.slice(style.indexOf('.candidate-detail'))
    assert.match(block.slice(0, 400), /-webkit-line-clamp:\s*2|line-clamp:\s*2/,
      'candidate-detail 未限制行数，漏判时仍会撑爆卡片')
  })
})
