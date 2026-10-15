// recording-title.test.mjs —— §388 的**行为**门。
//
// ⭐ 为什么是行为门而不是源码判据
//   首版把这条钉在 `check:refine-summary-meta-split` 的 A6 上，判据是
//   「`createMeeting` 的 title 实参里不得出现 `sessionTitle`」。
//   那钉的是**写法**：把逻辑内联在 `start()` 里、换个写法就红；
//   而写法对了、行为写反了（例如把 `!startsWith` 写成 `startsWith`）它照样绿。
//   ⇒ 这里直接 import 纯函数 `resolveRecordingTitle` 跑真实输入输出。
//
// ⚠ 首版 import 的是 `useSessionLiveRecord.ts` 本身 ⇒ 连带解析
//   `'../../composables/useMeetingRecorder'`（无扩展名）⇒ ERR_MODULE_NOT_FOUND，
//   用例一条都跑不起来。⇒ 纯决策已抽到零 import 的 `recording-title.ts`（§52）。
//
// ⚠ 三条纪律
//   ① 判据量的是**返回值**，不是源码里有没有某个字符串。
//   ② 每个用例都写明**为什么**是这个期望值（不写「应该等于」这种废话）。
//   ③ 必有一个**阴性对照**：把判断方向反过来时，本文件必须变红 ——
//      否则「恒真」看起来和「通过」完全一样。

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { resolveRecordingTitle } from '../recording-title.ts'

// 本机 opencode.db 的真实会话 ID 形态（§387.2 只读查询）
const ID = 'ses_edac25254ffeOyu929tw4kRIuy'
// §387.1：`sessionTitle()` 的第三档兜底是 `sessionID.slice(0, 8)`
const SLICE8 = ID.slice(0, 8)

test('ID 片段不能当会议标题（§387 抓到的那一档）', () => {
  // SessionConversationView 的显示兜底原样漏进建会 ⇒ 列表里逐条 `ses_edac2`
  assert.equal(resolveRecordingTitle(ID, SLICE8), '会话录音')
})

test('标题恰好等于整个 ID 也算片段', () => {
  // 首版用 `<` 而不是 `<=`，这一档漏了；这里钉住边界
  assert.equal(resolveRecordingTitle(ID, ID), '会话录音')
})

test('真实的会话标题原样保留', () => {
  // 这是修好之后**该有的行为**：§146 的 TITLE 3/3 依赖真标题进得来
  assert.equal(resolveRecordingTitle(ID, '悬界芯片客户评审会'), '悬界芯片客户评审会')
})

test('空 / 空白标题落到占位名', () => {
  // `|| '会话录音'` 原本就是作者写的兜底，只是永远走不到
  assert.equal(resolveRecordingTitle(ID, ''), '会话录音')
  assert.equal(resolveRecordingTitle(ID, '   '), '会话录音')
})

test('真实标题两侧的空白被裁掉，而不是被当成 ID 片段', () => {
  // ⭐ 这一条是**阴性对照**：若把判断写成 `id.startsWith(title.trim()) === false` 反向，
  //   或把 trim 漏掉，`' 悬界芯片评审 '` 会掉进占位分支 ⇒ 这里就红。
  assert.equal(resolveRecordingTitle(ID, '  悬界芯片评审  '), '悬界芯片评审')
})

test('ID 为空时不做片段判定（opencode 还没给出 ID 的极早时刻）', () => {
  // 判据要求 `id.length > 0`，否则任何标题都会被 `startsWith('')` 判成片段
  assert.equal(resolveRecordingTitle('', '随手记一下'), '随手记一下')
})

test('与 ID 同前缀但并非前缀的标题不受影响', () => {
  // `startsWith` 的方向不能搞反：ID 是 'ses_…'，标题 '周会' 不是它的前缀
  assert.equal(resolveRecordingTitle(ID, '周会'), '周会')
})