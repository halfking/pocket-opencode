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
 * 结果：并行会话在 §4.4 费力保住的可行动原因，在**笔记录音**这条链路上被静默
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

/**
 * 真机（Redmi 2026-10-01）auto 通道两段都不通时的**实际**后端原文。
 *
 * 与上面 BACKEND 的区别：第二段也带 `stt_unavailable:` 前缀。2026-10-01 之前
 * `resolveSTTTarget` 直接 `fmt.Errorf("%s；%s", gwErr, extErr)`，两段各自带码，
 * 于是用户在中文句子中间会看到一个裸错误码。后端已改为只保留首位前缀。
 */
const BACKEND_AUTO_BOTH = (() => {
  const b = BACKEND
  const i = b.indexOf('；')
  return b.slice(0, i + 1) + 'stt_unavailable: ' + b.slice(i + 1)
})()

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
        !/apiError\(\s*(?:props\.)?(?:recError|error)\b/.test(src),
        `${rel} 不应对已是成品文案的 error 再调 apiError：` +
        '会因错误码前缀已被 sttFailureText 剥掉而落回通用兜底',
      )
    }
  })

  it('守卫正则必须真的能匹配到 NoteListView 的调用形态（防假绿）', () => {
    // 2026-10-01 假绿实录：上面的守卫写的是
    //   /apiError\(\s*(?:props\.)?error\b/
    // 而 NoteListView 实际写的是 `apiError(recError.value, 'errors.sttNotConfigured')`，
    // `error\b` 匹配不到 `recError.value` —— 守卫全程没生效，测试绿灯而缺陷仍在，
    // 真机才暴露出来。这里把两种真实调用形态都钉住。
    const guard = /apiError\(\s*(?:props\.)?(?:recError|error)\b/
    assert.ok(
      guard.test("apiError(recError.value, 'errors.sttNotConfigured')"),
      '守卫正则匹配不到 NoteListView 的真实调用形态，会再次假绿',
    )
    assert.ok(
      guard.test('apiError(props.error, "errors.sttNotConfigured")'),
      '守卫正则匹配不到 NoteRecordingStudio 的形态',
    )
    // 反向：不能误伤同文件里与 error 无关的 apiError 调用。
    assert.ok(
      !guard.test("apiError(e, 'errors.loadNotesFailed')"),
      '守卫正则过宽，会误报与 runtime error 无关的调用',
    )
  })

  it('笔记页停止后的横幅直出成品文案，可行动原因不被压掉', () => {
    // 真机复现：录音中显示完整原因，点停止后横幅却变成「该功能尚未完成配置」。
    const curated = sttFailureText({ message: BACKEND, body: { error: BACKEND } }, '转写失败')
    // NoteListView 现在是 `computed(() => recError.value || '')`，直出。
    const recordErrorText = curated || ''
    assert.ok(
      recordErrorText.includes('设置 → 语音转写'),
      '停止后的横幅必须保留「去设置里配外部服务」这条唯一行动指引',
    )
    assert.ok(
      !recordErrorText.includes('stt_unavailable'),
      '用户可见文案里不该残留裸错误码',
    )
  })

  it('auto 双通道失败的后端原文只带一个错误码前缀（后端拼接去重）', () => {
    // 前端只能保证「首位前缀被剥掉」；第二段前缀的去除由后端 resolveSTTTarget
    // 负责，对应 Go 侧用例 TestResolveSTTTargetAutoBothChannelsUnavailable_NoDuplicateCode。
    // 这里钉住的是前端这半边的责任边界：sttFailureText 只处理首位。
    const curated = sttFailureText(
      { message: BACKEND_AUTO_BOTH, body: { error: BACKEND_AUTO_BOTH } },
      '转写失败',
    )
    assert.ok(
      !curated.startsWith('stt_unavailable'),
      '首位前缀必须被剥掉',
    )
    assert.ok(
      curated.includes('设置 → 语音转写'),
      '尾部行动指引必须保留',
    )
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
          || /mic\.deniedLabel\.value/.test(rhs)
          || /EMPTY_RECORDING_NOTICE/.test(rhs),
        `recordingRuntime.ts:${i + 1} 的 error 写入既无字面文案也未过 sttFailureText：${line.trim()}`,
      )
    })
  })

  it('白名单里的 EMPTY_RECORDING_NOTICE 必须真的是字符串字面量', () => {
    // 上一条把常量名放行了，那就必须在这里把它钉死——否则「放行一个名字」
    // 就变成了绕过不变量的后门：任何人把常量指向原始异常 message 都能过。
    //
    // 这一条比既有的 mic.deniedLabel.value 先例更严：那处放行时**什么都不查**，
    // 这里放行则要求定义确实是面向用户的字面文案。
    const note = read('../../features/notes/note-recording.ts')
    const decl = note.match(/export const EMPTY_RECORDING_NOTICE\s*=\s*([\s\S]*?)\n/)
    assert.ok(decl, 'note-recording.ts 里没有 EMPTY_RECORDING_NOTICE 的定义')
    const rhs = decl[1].trim()
    assert.ok(
      /^['"`]/.test(rhs),
      `EMPTY_RECORDING_NOTICE 必须定义为字符串字面量，实际是：${rhs.slice(0, 60)}`,
    )
    assert.ok(
      !/\b(?:err|e|ex|exc|error)\b\s*\??\s*\.\s*(?:message|body|response)\b/.test(rhs),
      'EMPTY_RECORDING_NOTICE 指向了原始异常的可行动信息会在这里被二次压掉',
    )
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
