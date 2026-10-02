// email-headerless-message.test.mjs
//
// 锁住**同一个缺陷类的第二个入口**：`extractEmailBody` 的两条 fail-open
// （`!looksLikeMime → return src` 与 `!parts.length → return src`）会把
// **一封没有 MIME 头的报文**的协议头当正文返回。
//
// 与 54d2fc51 的区别要说清楚，否则容易误以为已经修过：
//   · 54d2fc51：头**太长**，40 组 Received/ARC/DKIM 把 Content-Type 顶出
//     4000 字符窗口 → 判「不是 MIME」→ 原文返回。
//   · 本条：压根**没有** Content-Type / Content-Transfer-Encoding /
//     MIME-Version（部分老网关与脚本发送方会省掉），于是三条判据一条都不
//     命中 → 同样判「不是 MIME」→ 同样原文返回。
// 两者是不同的输入，走的是**同一行** `return src`。
//
// 探针实测（修之前）：
//   泄露头? true
//   输出: "From: zhang@example.com\r\nTo: li@example.com\r\nSubject: …"
// 即详情页首屏全是 From / To / Subject / Date / Message-ID。
//
// 这正是用户报的「邮件的详情展示异常」的另一个来源。
//
// 负控见文件末尾，实测转红。

import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import { extractEmailBody } from '../email-body-format.ts'

/** 协议头在正文里出现 —— 判据成立时必须为 false。 */
function leakedHeaderLines(text) {
  return (text || '')
    .split(/\r?\n/)
    .filter((l) => /^(?:from|to|cc|bcc|sender|reply-to|subject|date|message-id|in-reply-to|references|return-path|received|mime-version|content-type)\s*:/i.test(l.trim()))
}

test('无 Content-Type 的报文：协议头不当作正文', () => {
  const raw = [
    'From: zhang@example.com',
    'To: li@example.com',
    'Subject: =?utf-8?B?5rWL6K+V?=',
    'Date: Mon, 12 Oct 2026 10:00:00 +0800',
    'Message-ID: <abc@example.com>',
    '',
    '张经理：',
    '',
    '附件是本周的结算明细，请查收。',
  ].join('\r\n')

  const out = extractEmailBody(raw)
  assert.deepEqual(leakedHeaderLines(out), [], `协议头泄漏进正文：${JSON.stringify(out.slice(0, 120))}`)
  // 不能只是「头没了」——正文必须真的还在。
  assert.ok(out.includes('结算明细'), `正文丢了：${JSON.stringify(out)}`)
})

test('无 Content-Type 但有 Received 堆：同样不能泄漏', () => {
  const raw = [
    'Received: from a.example.com by b.example.com; Mon, 12 Oct 2026 10:00:00 +0800',
    'From: zhang@example.com',
    'Subject: hello',
    'Date: Mon, 12 Oct 2026 10:00:00 +0800',
    '',
    '正文第一行',
    '正文第二行',
  ].join('\r\n')

  const out = extractEmailBody(raw)
  assert.deepEqual(leakedHeaderLines(out), [], `协议头泄漏进正文：${JSON.stringify(out.slice(0, 120))}`)
  assert.ok(out.includes('正文第一行'), `正文丢了：${JSON.stringify(out)}`)
})

test('反向：不是报文的东西不能被剥头', () => {
  const cases = [
    // 裸 HTML 片段（服务端 BODY[TEXT] / ExtractDisplayBody 兜底产物）
    ['<html><body><p>您好</p></body></html>', '您好'],
    // 纯文本正文，首行形似 field-name 但不在 RFC 5322 核心集合里
    ['Note: 已确认\r\n\r\n明天上午十点开会', '已确认'],
    // 已是拍平好的展示文本（无头无空行）
    ['张经理：\r\n\r\n附件是本周的结算明细。', '结算明细'],
    // 空
    ['', ''],
  ]
  for (const [input, mustKeep] of cases) {
    const out = extractEmailBody(input)
    assert.equal(out, input, `不该被改动的输入被剥了：${JSON.stringify(input)} -> ${JSON.stringify(out)}`)
    if (mustKeep) assert.ok(out.includes(mustKeep), `内容丢失：${JSON.stringify(out)}`)
  }
})

test('有 Content-Type 的正常邮件行为不变', () => {
  const raw = [
    'From: zhang@example.com',
    'Subject: hello',
    'Content-Type: text/plain; charset=utf-8',
    '',
    '正文第一行',
  ].join('\r\n')
  const out = extractEmailBody(raw)
  assert.deepEqual(leakedHeaderLines(out), [])
  assert.ok(out.includes('正文第一行'), `正文丢了：${JSON.stringify(out)}`)
})

// ---------------------------------------------------------------------------
// 负控
// ---------------------------------------------------------------------------
// 把 looksLikeMime 退回「只在前 200 字符里找边界行」的旧形态（去掉 /m）、
// 并把两条 fail-open 出口退回 `return src` 时，本文件必须转红。
//
// 实测：还原 extractEmailBody 的 `return src`（两处）后，
//   · 「无 Content-Type 的报文：协议头不当作正文」转红
//   · 「无 Content-Type 但有 Received 堆」转红
// 还原 looksLikeMime 的 200 字符窗口不会让本文件转红 ——
// 它是 54d2fc51 那条用例的职责，两者守的是不同入口。
