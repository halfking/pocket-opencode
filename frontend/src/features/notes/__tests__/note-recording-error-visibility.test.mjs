/**
 * 笔记录音转写失败的可见性回归（2026-09-30 真机审计）。
 *
 * 真机报「点击停止没有办法停止，语音没有转成文字」。停止死锁已修（实测
 * 513ms 退出录音态），但「没有转成文字」的表现是**完全静默**：
 * NoteRecorderRuntime.runStop() 把兜底转写的失败写进 this.error，
 * 而唯一渲染它的 NoteRecordingStudio 只在 isRecording 为真时挂载 ——
 * 一点停止组件就卸载，错误随之从界面上消失，用户只看到「点完没反应」。
 *
 * 这里锁住两条约定：
 *   1. 录音结束后仍要把错误显示出来（不能随 Studio 一起卸载）
 *   2. 下一次录音开始时错误要被清空（不能一直挂着）
 *
 * 2026-10-01 追加：原第 2 条约定「错误文案走统一归一」已被**推翻**。
 * 归一搬到了 runtime 写入时（sttFailureText），渲染层再归一一次会把
 * 可行动原因压掉 —— 详见下面那条用例的注释。
 */
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

const HERE = dirname(fileURLToPath(import.meta.url))
const read = (rel) => readFileSync(join(HERE, rel), 'utf8')

describe('note recording error visibility', () => {
  const view = read('../NoteListView.vue')

  it('停止录音后仍渲染转写错误（不随 Studio 卸载消失）', () => {
    // 录音中：Studio 渲染
    assert.match(view, /v-if="isRecording"[\s\S]{0,200}NoteRecordingStudio/,
      '录音中应挂载 NoteRecordingStudio')
    // 停止后：仍有独立的错误提示分支
    assert.match(view, /v-else-if="recError"[\s\S]{0,200}recordErrorText/,
      '停止录音后必须仍渲染转写错误提示')
  })

  it('错误文案直接渲染 runtime 的成品文案，不得再套 apiError 二次归一', () => {
    // 2026-10-01 真机复现修正 —— 这条断言原先是**反的**，它要求
    // `apiError(recError.value, 'errors.sttNotConfigured')` 必须存在。
    //
    // 它的前提（注释原文：「recError 存的是原始异常文本」）在
    // 2026-09-30 成立，但 recordingRuntime 后来把归一搬到了**写入时**
    // （`sttFailureText()`），存进 rt.error 的已经是面向用户的成品文案，
    // `stt_unavailable:` 前缀已被剥掉。再套 apiError：
    //   extractErrorCode 取第一个冒号前的片段 → 取不到码
    //   → 落回 errors.notConfigured「该功能尚未完成配置」
    // 把「去设置里配外部服务」这条唯一行动指引整个抹掉。
    //
    // 真机（Redmi）证据：同一个录音，录音中显示完整原因，点停止后横幅变成
    // 「该功能尚未完成配置」。技术串确实没上屏（那部分前提仍由
    // recordingRuntime 的写入点不变量保证），但代价是**有用信息也没了**。
    assert.match(view, /const recordErrorText = computed\(\(\) => recError\.value \|\| ''\)/,
      '停止后的横幅应直出 runtime 已归一的成品文案')
    assert.ok(
      !/apiError\(\s*recError\b/.test(view),
      '不得对已是成品文案的 recError 再调 apiError，会压掉可行动原因',
    )
  })

  it('错误提示有实际样式（Studio 的 scoped 类不作用到本页）', () => {
    const style = view.slice(view.lastIndexOf('<style'))
    assert.match(style, /\.studio-error\s*\{/, '本页需自带 .studio-error 样式')
    assert.match(style, /\.studio-error[\s\S]{0,200}var\(--danger\)/, '应使用 danger 颜色')
  })

  it('新一次录音会清空旧错误，不会一直挂在界面上', () => {
    const rt = read('../../../native/recordingRuntime.ts')
    const start = rt.slice(rt.indexOf('async start()'), rt.indexOf('async start()') + 1200)
    assert.match(start, /this\.error\.value = ''/,
      'start() 必须清空上一轮的错误')
  })
})

/**
 * 即时总结的可见性（2026-10-02 静态审计）。
 *
 * 用户报「录音停止后没有即时总结」。功能其实**存在**：
 * NoteListView.createVoiceDraft() 落库草稿后调 /api/notes/{id}/summarize，
 * NoteMetaSheet 顶部有 .ai-summary 块。真正的缺陷是**状态从来没被渲染**：
 *
 *   - summarizeError：声明(141) + 重置(281) + 赋值(290)，模板里 0 次出现
 *   - summarizing：声明(140) + true(280) + false(292)，模板里 0 次出现
 *
 * 于是无论「还在生成」「失败抛异常」还是「压根没触发」，用户看到的都是同一
 * 副样子：面板里没有总结、也没有任何提示。NoteListView 的注释写着
 * 「只是顶部多出一行错误提示」——那句话此前是假的。
 *
 * 这类缺陷运行时不会报任何错，只能靠扫描源码文本守住，跟上面几条同一范式。
 */
describe('note 即时总结状态必须被渲染', () => {
  const listView = read('../NoteListView.vue')
  const sheet = read('../NoteMetaSheet.vue')

  it('summarizeError / summarizing 真的传进了 NoteMetaSheet', () => {
    const tag = listView.slice(listView.indexOf('<NoteMetaSheet'))
    assert.match(tag, /:summary-error="summarizeError"/,
      'summarizeError 必须传给 NoteMetaSheet，否则它只存在于 setup 里没人看')
    assert.match(tag, /:summary-loading="summarizing"/,
      'summarizing 必须传给 NoteMetaSheet，否则「生成中」永远看不见')
  })

  it('NoteMetaSheet 声明了这两个 prop', () => {
    const props = sheet.slice(sheet.indexOf('defineProps'), sheet.indexOf('defineEmits'))
    assert.match(props, /summaryError\?\s*:\s*string/, '缺少 summaryError prop')
    assert.match(props, /summaryLoading\?\s*:\s*boolean/, '缺少 summaryLoading prop')
  })

  it('两个状态在模板里都有渲染出口（否则传了也等于没传）', () => {
    const tpl = sheet.slice(0, sheet.indexOf('</template>'))
    assert.match(tpl, /v-if="summaryLoading"/, '模板里没有渲染 summaryLoading')
    assert.match(tpl, /v-else-if="summaryError"/, '模板里没有渲染 summaryError')
  })

  it('错误态有实际样式（scoped 类不作用到别处，且不能是纯文字无色）', () => {
    const style = sheet.slice(sheet.lastIndexOf('<style'))
    assert.match(style, /\.err\s*\{[\s\S]{0,120}var\(--danger\)/,
      '错误提示要用 danger 颜色，否则与普通文字无法区分')
  })

  it('接口返回空 summary 时也要给提示，不能静默', () => {
    // api/notes.ts 的 summarize 注释写明「失败时返回空 summary，前端不阻塞
    // 流程」——也就是 200 + {summary:''}，**不抛异常**。原来的
    // `if (summary && ...)` 直接跳过且不设错误，于是这条路径同样静默。
    //
    // 判据刻意锚在 else 分支本身，而不是「if 之后 N 个字符内」：第一版写成
    // `[\s\S]{0,400}` 的窗口，被自己那段中文注释撑爆而误报。字符窗口对注释
    // 长度敏感，改成语义锚点。
    assert.match(
      listView,
      /if \(summary && metaNote\.value\)\s*\{[\s\S]{0,600}?\}\s*else\s*\{\s*(?:\/\/[^\n]*\n\s*)*summarizeError\.value\s*=/,
      '200 + 空 summary 的分支必须设置提示文案，否则用户仍看不到任何反馈',
    )
  })
})
