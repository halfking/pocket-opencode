/**
 * MiniMax 直调通道（模板化转写）的行为锁定（2026-10-08）。
 *
 * 锁的是四件**改错了不会立刻报错**的事：
 *
 *   1. 「流式」与「说话人分离」必须**互斥**。实测上游对
 *      stream=true + response_format=verbose_json 直接返回 400
 *      「cannot be used with stream=true (2013)」。
 *      ⇒ 做成两个可同时勾的 checkbox 等于给用户一个必然失败的组合。
 *
 *   2. MiniMax 的 key 必须**独立**于外部服务的 key，且「不传 = 不改动」。
 *      若沿用 externalApiKey 那套「空串 = 清空」，用户只改一下语种就会
 *      连带清掉两把 key —— 而「清空」应该是一个显式动作。
 *
 *   3. 试转必须能指定模板与流式形态，否则设置页试转不到 MiniMax，
 *      用户就只能靠真实录音去发现问题。
 *
 *   4. 「当前生效」的提示必须与后端真实的回落顺序一致
 *      （网关 → MiniMax → 外部）。若前端还写「回退到外部」，
 *      提示与行为相反，用户会照着错误的预期去排查。
 *
 * 这些判据是**源码契约式**的（读 .vue / .ts 文本），与同目录
 * settings-stt.test.mjs 同一形态。理由：组件依赖浏览器 API 与 vue-tsc，
 * 真挂载的测试成本远高于此处的回归价值。
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

describe('MiniMax 直调通道（模板化转写）', () => {
  it('通道下拉里必须有 minimax，且 auto 的说明要反映三级回落', () => {
    assert.match(view, /value: 'minimax' as SttChannel/, '通道下拉缺少 minimax 选项')
    // auto 的标签必须包含 MiniMax，否则它排在回落链里却没有入口说明。
    assert.match(
      view,
      /auto' as SttChannel, label: '[^']*MiniMax/,
      'auto 通道标签未提及 MiniMax（它在后端回落链里，但 UI 说不出来）',
    )
  })

  it('流式与说话人分离做成二选一，不能是两个独立 checkbox', () => {
    // 用 radio：两个 checkbox 可以同时勾上 = 必然 400 的组合。
    assert.match(view, /type="radio"[\s\S]{0,200}stt-mm-mode-stream/, '输出方式不是 radio（可同时勾选＝非法组合）')
    assert.doesNotMatch(
      view,
      /type="checkbox"[\s\S]{0,200}stt-mm-diarization/,
      '说话人分离仍用 checkbox：会允许与流式同时勾选（上游 400 实测 (2013)）',
    )
  })

  it('onMiniMaxMode 一次赋值两个字段，互斥在赋值处就成立', () => {
    const m = view.match(/function onMiniMaxMode\([^)]*\)\s*\{[\s\S]*?\n\}/)
    assert.ok(m, '找不到 onMiniMaxMode')
    const body = m[0]
    assert.match(body, /form\.minimaxStream = mode === 'stream'/, 'onMiniMaxMode 未设置 stream')
    // 关键：两个字段在同一次赋值里互斥，而不是靠两个独立事件处理器。
    assert.match(
      body,
      /form\.minimaxDiarization = !form\.minimaxStream/,
      'onMiniMaxMode 没有把 diarization 绑到 stream 的反值上（可留下两个都为真的状态）',
    )
  })

  it('加载配置时若两个都为真，以流式为准并关掉分离', () => {
    assert.match(
      view,
      /minimaxDiarization = !!cfg\.settings\.minimaxDiarization && !form\.minimaxStream/,
      'applyConfig 未处理「流式与分离同时为真」的老数据（会还原出一个必然 400 的配置）',
    )
  })

  it('MiniMax key 与外部 key 独立，且不传 = 不改动', () => {
    // 前端只在用户真填了才传 minimaxApiKey ⇒ undefined ⇒ 后端「不改动」。
    assert.match(
      view,
      /minimaxApiKey: form\.minimaxApiKey \|\| undefined/,
      'minimaxApiKey 未按「填了才传」发送（会把「没填」当成「清空」）',
    )
    // 而 externalApiKey 保持旧语义（空串→undefined 同样不传），两者不能混淆。
    assert.match(view, /externalApiKey: form\.externalApiKey \|\| undefined/, 'externalApiKey 语义被改动了')
  })

  it('保存时把 minimax 的四个字段一起送出去', () => {
    for (const field of [
      'minimaxBaseURL: form.minimaxBaseURL',
      'minimaxModel: form.minimaxModel',
      'minimaxStream: form.minimaxStream',
      'minimaxDiarization: form.minimaxDiarization',
    ]) {
      assert.ok(view.includes(field), `保存载荷缺字段：${field}`)
    }
  })

  it('试转能指定模板与流式形态（否则用户只能靠真实录音发现问题）', () => {
    assert.match(
      view,
      /provider: 'minimax-speech-to-text'/,
      '试转未指定 minimax 模板（会走 OpenAI 兼容层，拿不到 minimax 的结果）',
    )
    assert.match(
      view,
      /transport: form\.minimaxStream \? 'sse' : 'transcriptions'/,
      '试转未按当前二选一传 transport（勾了流式却按非流式试）',
    )
  })

  it('互斥约束在 UI 上有明示，而不是只在代码里', () => {
    assert.match(
      view,
      /互斥/,
      'UI 未告知「流式与说话人分离互斥」——用户会以为两个都能要',
    )
  })

  it('「当前生效」提示覆盖 minimax 通道，不显示成别的通道', () => {
    assert.match(
      view,
      /form\.channel === 'minimax'[\s\S]{0,200}miniMaxMissing/,
      'effectiveText 未处理 minimax 通道（会掉到 auto 的回落描述，与实际行为不符）',
    )
  })

  it('「当前生效」的 auto 描述与后端回落顺序一致（网关→MiniMax→外部）', () => {
    const m = view.match(/const effectiveText = computed\(\(\) => \{[\s\S]*?\n\}\)/)
    assert.ok(m, '找不到 effectiveText')
    const body = m[0]
    const mmIdx = body.indexOf('回退到 MiniMax 直调')
    const extIdx = body.indexOf('将回退到外部服务')
    assert.ok(mmIdx > 0, 'auto 描述里没有「回退到 MiniMax 直调」这一级')
    assert.ok(extIdx > 0, 'auto 描述里没有「将回退到外部服务」这一级')
    // 后端 resolveSTTTarget 的顺序是 网关 → MiniMax → 外部，
    // 且「已配 MiniMax key」时优先 MiniMax。所以提示里不能暗示外部优先。
    assert.ok(
      mmIdx < extIdx,
      '提示里 MiniMax 出现在外部之后 —— 与后端实际的回落顺序相反（2026-10-08 后端把 MiniMax 插到了外部之前）',
    )
  })

  it('miniMaxMissing 按「有没有 key」判断，而不是只看输入框非空', () => {
    // 否则用户明明保存过 key，打开设置页却看到「未配置 API Key」。
    assert.match(
      view,
      /if \(!form\.hasMiniMaxKey && !form\.minimaxApiKey\)/,
      'miniMaxMissing 未考虑已保存的 key（会造成反着说的提示）',
    )
  })

  it('api 层：类型与契约里都有 minimax 相关字段', () => {
    assert.match(api, /hasMiniMaxKey\?: boolean/, 'SttSettings 缺 hasMiniMaxKey')
    assert.match(api, /minimaxApiKey\?: string/, 'saveConfig 缺 minimaxApiKey')
    assert.match(api, /templates: SttTemplate\[\]/, 'SttConfigResponse 缺 templates（模板清单由后端给）')
    assert.match(api, /'minimax'/, 'SttChannel 联合类型缺 minimax')
    assert.match(api, /'sse'/, 'SttTransport 联合类型缺 sse')
  })
})
