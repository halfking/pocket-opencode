import { strict as assert } from 'node:assert'
import { describe, test } from 'node:test'
import {
  composeSheetTitle,
  defaultForwardSubject,
  defaultReplySubject,
  todoFromDraft,
  toggleCompose,
} from '../compose-mode.ts'

test('toggleCompose opens a kind and hides when tapped again', () => {
  assert.equal(toggleCompose('hidden', 'reply'), 'reply')
  assert.equal(toggleCompose('reply', 'reply'), 'hidden')
  assert.equal(toggleCompose('reply', 'forward'), 'forward')
})

test('reply and forward subjects keep a single prefix', () => {
  assert.equal(defaultReplySubject('Hello'), 'Re: Hello')
  assert.equal(defaultReplySubject('Re: Hello'), 'Re: Hello')
  assert.equal(defaultForwardSubject('Hello'), 'Fwd: Hello')
  assert.equal(defaultForwardSubject('Fwd: Hello'), 'Fwd: Hello')
})

test('compose sheet titles name the action', () => {
  assert.equal(composeSheetTitle('hidden', 'Ada'), '')
  assert.equal(composeSheetTitle('reply', 'Ada'), '回复 Ada')
  assert.equal(composeSheetTitle('forward', 'Ada'), '转发')
  assert.equal(composeSheetTitle('todo', 'Ada'), '转 Todo')
})

test('todoFromDraft uses first line as title', () => {
  assert.deepEqual(todoFromDraft('发票待付\n金额 120', 'fallback'), {
    title: '发票待付',
    description: '金额 120',
  })
  assert.deepEqual(todoFromDraft('   ', '无主题'), {
    title: '无主题',
    description: '',
  })
})

/**
 * §918：回复/转发的主题**必须能过服务端的字节闸**。
 *
 * ── 缺陷本体 ──
 *   服务端 `handleEmailSend`：`if len(body.Subject) > 500 { 400 "subject too long" }`
 *   （`backend/internal/server/server_assistant.go:1727`）——
 *   ⚠ Go 的 `len(string)` 是**字节数** ⇒ 500 字节 ≈ **166 个中文字**。
 *   ⚠ 主题**完全由原邮件派生**（`defaultReplySubject` / `defaultForwardSubject`），
 *     **用户不能编辑它** ⇒ 原邮件主题一长，**回复/转发就永远发不出去**，
 *     而界面只显示泛化的「发送失败」⇒ 用户既不知道原因、也没有办法绕开。
 *
 * ── 修法 ──
 *   派生主题时按**字节**从**尾部**裁（`Re: `/`Fwd: ` 前缀在头部 ⇒ 一定保留）。
 */
describe('§918 邮件主题必须能过服务端的 500 **字节**闸', () => {
  /** ⚠ 刻意**不**从生产代码 import 这个数：写死服务端那个值，
   *  前端的镜像常量一旦漂移，本判据立刻红（否则两边一起错就没人知道）。 */
  const GATE = 500
  const bytes = (s) => Buffer.byteLength(s, 'utf8')

  test('★ 超长主题必须被裁到闸内，且前缀仍在、仍是原文的前缀', () => {
    // ⚠ 语料必须是**中文**：ASCII 下字节数 == 字符数 ⇒ 单位差异测不出来
    //   （§244 那条门的老教训：ASCII 语料让整份门恒绿）。
    const long = '季度预算与交付里程碑排期以及客户跟进安排事项的最终确认版本'.repeat(20)
    assert.ok(bytes(long) > GATE, '量具失效：这条语料本来就该超闸')
    for (const [name, fn, prefix] of [
      ['回复', defaultReplySubject, 'Re: '],
      ['转发', defaultForwardSubject, 'Fwd: '],
    ]) {
      const got = fn(long)
      assert.ok(bytes(got) <= GATE,
        `${name}：主题 ${bytes(got)} 字节 > 闸 ${GATE} 字节 ⇒ 这条邮件**必然 400** ⇒ 发不出去`)
      assert.ok(got.startsWith(prefix), `${name}：前缀没了（${got.slice(0, 12)}…）⇒ 裁错了头`)
      assert.ok(long.startsWith(got.slice(prefix.length)),
        `${name}：裁出来的不是原文的前缀 ⇒ 内容被改坏了`)
    }
  })

  test('★ 已在链上的主题（`Re:` / `Fwd:` 开头）也要过闸', () => {
    const long = 'Re: ' + '季度预算与交付里程碑排期以及客户跟进安排事项的最终确认版本'.repeat(20)
    const got = defaultReplySubject(long)
    assert.ok(bytes(got) <= GATE, `主题 ${bytes(got)} 字节 > 闸 ${GATE} ⇒ 必然 400`)
    assert.ok(got.startsWith('Re: '), '链上的主题丢了前缀')
  })

  test('★ 负控：短主题**逐字不变**（不裁、不改）', () => {
    for (const s of ['Hello', '会议纪要', 'Re: Hello', '季度预算确认']) {
      assert.equal(defaultReplySubject(s), s.startsWith('Re:') ? s : `Re: ${s}`,
        `短主题「${s}」被改了 ⇒ 行为变了`)
      assert.equal(defaultForwardSubject(s), s.startsWith('Fwd:') ? s : `Fwd: ${s}`,
        `短主题「${s}」被改了 ⇒ 行为变了`)
    }
  })
})
