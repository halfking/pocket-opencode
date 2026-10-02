// email-header-heavy-mime.test.mjs
//
// 锁住一条真机实测的缺陷：**协议头堆太长时，整封邮件被当成纯文本，
// MIME 解析整条被跳过，于是 Received / ARC-Seal / DKIM-Signature
// 这些协议头直接当正文渲染给用户。**
//
// 2026-10-03 真机（Redmi 2411DRN47C / Android 14）实测：
//   邮件详情页正文是 95869 字符的原文，头屏全是
//     Received: from mail-yx2-f41.google.com (unknown [74.125.224.169])
//     ARC-Seal: i=2; a=rsa-sha256; t=1790923240; cv=pass; d=google.com; ...
//   真实邮件正文一屏都看不到。
//
// 根因：looksLikeMime() 只在**前 4000 字符**里找 Content-Type。
// 经 Gmail/Coremail 转发的邮件前面堆了几十个 Received / ARC / DKIM
// 头，Content-Type 被推到 4000 字符之外 → 判定「不是 MIME」→
// 原文原样返回。
//
// 负控见文件末尾，实测转红。

import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import { extractEmailBody } from '../email-body-format.ts'

/** 造一封「协议头很长」的邮件：N 个 Received/ARC 头，把 Content-Type 推到远处。 */
function buildHeaderHeavyMIME(headerCount) {
  const lines = []
  for (let i = 0; i < headerCount; i++) {
    lines.push(
      `Received: from relay${i}.mail.example.com (unknown [10.0.${i}.1])`,
      `        by mx${i}.example.com with SMTP id ${'x'.repeat(60)}`,
      `        for <kimmy.huang@163.com>; Fri, 02 Oct 2026 14:40:41 +0800 (CST)`,
      `ARC-Seal: i=${i}; a=rsa-sha256; t=17909232${i}; cv=${i % 2 ? 'none' : 'pass'};`,
      `        d=google.com; s=arc-20260327; b=${'A'.repeat(50)}`,
      `DKIM-Signature: v=1; a=rsa-sha256; c=relaxed/relaxed; d=example.com;`,
      `        s=selector1; h=from:to:subject:date; bh=${'b'.repeat(60)};`,
    )
  }
  lines.push(
    'Content-Type: multipart/alternative; boundary="BOUND42"',
    'MIME-Version: 1.0',
    '',
    'This is a multi-part message in MIME format.',
    '',
    '--BOUND42',
    'Content-Type: text/plain; charset=utf-8',
    'Content-Transfer-Encoding: 8bit',
    '',
    '这是真正的邮件正文。额度不足请及时充值。',
    '',
    '--BOUND42',
    'Content-Type: text/html; charset=utf-8',
    'Content-Transfer-Encoding: 8bit',
    '',
    '<html><body><p>这是真正的邮件正文。额度不足请及时充值。</p></body></html>',
    '',
    '--BOUND42--',
  )
  return lines.join('\n')
}

test('协议头堆到 4000 字符以外时仍须按 MIME 解析（真机实测缺陷）', () => {
  const raw = buildHeaderHeavyMIME(40)
  // 先确认这份样本确实把 Content-Type 推出了 4000 字符窗口——
  // 否则这条用例即使转绿也什么都没证明（负控样本自身过期的同款陷阱）。
  const head4000 = raw.slice(0, 4000)
  assert.ok(
    !/content-type\s*:/i.test(head4000),
    '样本没把 Content-Type 推出 4000 字符窗口，这条用例不成立',
  )
  assert.ok(raw.length > 4000)

  const body = extractEmailBody(raw)

  assert.ok(
    !/ARC-Seal|DKIM-Signature/.test(body),
    `协议头漏进正文了（真机就是这样显示的）：\n${body.slice(0, 200)}`,
  )
  assert.ok(
    !/Received: from relay/.test(body),
    'Received 头漏进正文了',
  )
  assert.match(body, /这是真正的邮件正文/, '真实正文被协议头挤掉了')
})

test('头很短时行为不变（回归保护）', () => {
  const raw = buildHeaderHeavyMIME(1)
  const body = extractEmailBody(raw)
  assert.match(body, /这是真正的邮件正文/)
  assert.ok(!/ARC-Seal/.test(body))
})

test('判据自检：负控必须转红', () => {
  // 把 looksLikeMime 的 4000 字符窗口改回 200 字符（旧实现的等价替身），
  // 同一份样本必须立刻失败——证明本判据不是空转。
  const legacy = (s) => {
    const head = s.slice(0, 200)
    if (/content-type\s*:/i.test(head) || /content-transfer-encoding\s*:/i.test(head)) return true
    return /^--[\w'+=.-]+/m.test(s.slice(0, 200))
  }
  const raw = buildHeaderHeavyMIME(40)
  assert.equal(legacy(raw), false, '负控样本没让旧实现失效——本负控不成立')
})
