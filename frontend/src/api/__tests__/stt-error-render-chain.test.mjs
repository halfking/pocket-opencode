/**
 * STT 失败原因在「runtime → 渲染」整条链路上不能被二次归一压掉（2026-10-01 审计）。
 *
 * ## 缺陷背景
 *
 * 另一个并行会话在 `api/stt-error.ts` 开了窄口径特例：`sttFailureText()` 放行
 * 带 `stt_unavailable` 错误码的**整理后中文原因**，并剥掉错误码前缀。设计是对的
 * ——它保住的正是唯一能指导用户行动的信息（"网关列了模型但没开通 provider，
 * 去设置里换外部服务"）。
 *
 * 但 `recordingRuntime` 在 **写入时** 就调了 `sttFailureText`，于是存进
 * `this.error` 的已经是**面向用户的成品文案**（错误码前缀已被剥掉）。
 * 而笔记页 `NoteListView` / `NoteRecordingStudio` 渲染前又调了一次
 * `apiError(recError, 'errors.sttNotConfigured')`。`apiError` 内部靠
 * `extractErrorCode()` 取**第一个冒号前**的片段当错误码，前缀没了 → 取不到码
 * → 落回通用兜底「语音转写服务尚未配置」。
 *
 * 结果：并行会话在 §4.4 费力保住的���行动原因，在**笔记录音**这条链路上被静默
 * 压回通用文案；同一份文案在会议页（直接渲染 sttError）却是完整的。
 * 用户报的原话正是"笔记录音没有转成文字"——最需要解释的那条路径反而被盖掉了。
 *
 * ## 为什么不能"两个都留着"
 *
 * `apiError` 的设计前提是"入参是**原始异常**"。而 runtime 的 `this.error`
 * 全部写入点（见本文件下方不变量测试）都已经是面向用户的文案，
 * 再套一层归一只会丢失信息，不会有任何收益。
 */
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

const HERE = dirname(fileURLToPath(import.meta.url))
const read = (rel) => readFileSync(join(HERE, rel), 'utf8')

// 两个都是无运行时依赖的纯模块，node --test 可直接加载
const { sttFailureText } = await import('../stt-error.ts')
const { toUserMessage } = await import('../error-message.ts')

const BACKEND =
  'stt_unavailable: 网关暂无可用的语音转写模型（mimo-v2.5-asr=网关无上游 provider）；' +
  '外部语音转写服务未配置 API Key（设置 → 语音转写）'

/** 假翻译器：只认本测试关心的两个 key，其余原样回显。 */
const fakeT = (key) => (key === 'errors.sttNotConfigured' ? '语音转写服务尚未配置' : key)

describe('STT 失败原因在渲染链路上不被二次归一压掉', () => {
  it('runtime 写入后已是成品文案，再过 apiError 会把它压回通用兜底（缺陷复现）', () => {
    // 第一段：runtime 的写入路径（并行会话加的窄口径特例）
    const curated = sttFailureText({ message: BACKEND, body: { error: BACKEND } }, '转写失败')
    assert.equal(
      curated,
      BACKEND.slice('stt_unavailable:'.length).trim(),
      '前置条件：sttFailureText 应产出整理后的可行动原因',
    )
    assert.ok(curated.includes('设置 → 语音转写'), '前置条件：设置入口指引必须在')

    // 第二段：笔记页渲染前的二次归一 —— 这里就是信息被抹掉的地方
    const afterRender = toUserMessage(curated, fakeT, fakeT('errors.sttNotConfigured'))

    // 这是**缺陷本身**：可行动原因没了，只剩一句用户无法据以行动的通用文案
    assert.equal(afterRender, '语音转写服务尚未配置')
    assert.ok(
      !afterRender.includes('设置 → 语音转写'),
      '若这条断言失败，说明二次归一不再丢信息，本测试需要重新评估',
    )
  })

  it('渲染层不得再对 runtime 的 error 调 apiError（它已是面向用户的文案）', () => {
    for (const rel of [
      '../../features/notes/NoteListView.vue',
      '../../features/notes/NoteRecordingStudio.vue',
    ]) {
      const src = read(rel)
      assert.ok(
        !/apiError\(\s*(?:props\.)?error\b/.test(src),
        `${rel} 不应对已是成品文案的 error 再调 apiError：` +
        '会因错误码前缀已被 sttFailureText 剥掉而落回通用兜底',
      )
    }
  })

  it('useVoiceInput 的转写失败不得把原始 e.message 拼到界面上（第三处漏网）', () => {
    const src = read('../../composables/useVoiceInput.ts')
    assert.ok(
      !/sttError\.value\s*=\s*`[^`]*\$\{msg\}`/.test(src),
      'useVoiceInput 仍在把原始 e.message 拼进 sttError，UnifiedComposer 会直接显示技术串',
    )
    assert.match(
      src,
      /sttFailureText\(/,
      'useVoiceInput 应改用 sttFailureText，与 recordingRuntime 保持同一套展示规则',
    )
  })
})

describe('runtime 的 error 写入点不变量', () => {
  const runtime = read('../../native/recordingRuntime.ts')

  it('this.error.value 的每一处赋值都不得直接取原始异常的 message', () => {
    // 这条不变量是"渲染层可以直接渲染 error"的前提：只要有一个写入点塞的是
    // 原始异常，渲染层就还得再归一一次，可行动原因就会被二次压掉。
    // 判定方式不是"必须是字面量"（三元选字面量、mic.deniedLabel 都合法），
    // 而是反过来：**不碰异常对象的 message**。
    const lines = runtime.split(/\r?\n/)
    const offenders = []
    lines.forEach((line, i) => {
      const m = line.match(/this\.error\.value\s*=\s*(.+)$/)
      if (!m) return
      const rhs = m[1]
      const touchesRawError = /\b(?:err|e|ex|exc|error)\b\s*\??\s*\.\s*(?:message|body|response)\b/.test(rhs)
        || /String\(\s*(?:err|e|error)\s*\)/.test(rhs)
      if (touchesRawError) offenders.push(`  - recordingRuntime.ts:${i + 1}  ${line.trim()}`)
    })
    assert.equal(
      offenders.length,
      0,
      `这些写入点直接取了原始异常，渲染层就不敢直接渲染：\n${offenders.join('\n')}`,
    )
  })

  it('错误文案要么是字面量，要么经过 sttFailureText 归一', () => {
    const lines = runtime.split(/\r?\n/)
    lines.forEach((line, i) => {
      const m = line.match(/this\.error\.value\s*=\s*(.+)$/)
      if (!m) return
      const rhs = m[1].trim()
      assert.ok(
        /sttFailureText\(/.test(rhs)
          || /['"`]/.test(rhs)               // 至少含一个字面文案（含字面量三元）
          || /mic\.deniedLabel\.value/.test(rhs),
        `recordingRuntime.ts:${i + 1} 的 error 写入既无字面文案也未过 sttFailureText：${line.trim()}`,
      )
    })
  })

  it('会议侧渲染 sttError 时不再叠加归一（与笔记侧保持一致）', () => {
    for (const rel of [
      '../../features/meetings/MeetingDetailView.vue',
      '../../features/sessions/SessionLiveRecordPanel.vue',
    ]) {
      const src = read(rel)
      assert.ok(
        !/apiError\(\s*(?:recorder\.)?sttError\b/.test(src),
        `${rel} 对 sttError 做了二次归一，会与笔记侧行为不一致`,
      )
    }
  })
})
