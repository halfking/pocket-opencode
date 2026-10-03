import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import {
  TRANSLATE_CHUNK_CHARS,
  TRANSLATE_MAX_CHARS,
  detectSourceLang,
  protectSegments,
  restoreSegments,
  shouldTranslate,
  splitForTranslation,
  translateBodyPipeline,
  translateWithProtection,
} from '../translate-email.ts'

// ── 占位符保护 ──────────────────────────────────────────────────────────────

test('URL 被抽出成占位符，且不被二次抽取', () => {
  const { text, slots } = protectSegments('请访问 https://example.com/order?id=12345 查看')
  assert.match(text, /\{\{P0\}\}/)
  assert.equal(slots[0], 'https://example.com/order?id=12345')
  // 占位符里的数字不能被「纯数字长串」规则再抓一次
  assert.equal((text.match(/\{\{P\d+\}\}/g) || []).length, 1)
  assert.equal(restoreSegments(text, slots), '请访问 https://example.com/order?id=12345 查看')
})

test('HTML 标签整体保护，属性不被改写', () => {
  const { text, slots } = protectSegments('<a href="https://x.com" class="btn">点我</a>')
  assert.match(text, /\{\{P0\}\}点我\{\{P1\}\}/)
  assert.equal(slots[0], '<a href="https://x.com" class="btn">')
  assert.equal(restoreSegments(text, slots), '<a href="https://x.com" class="btn">点我</a>')
})

test('cid: 与 data: 内联图被保护', () => {
  const { slots } = protectSegments('<img src="cid:image001@01D8">')
  assert.ok(slots.some((s) => s.startsWith('cid:') || s.startsWith('<img')))
  const d = protectSegments('<img src="data:image/png;base64,iVBORw0KGgo=">')
  assert.ok(d.slots.some((s) => s.includes('data:image')))
})

test('金额、日期、订单号、电话全被保护', () => {
  const cases = [
    ['订单金额 $1,299.00 已支付', '$1,299.00'],
    ['实付 ¥1,299', '¥1,299'],
    // 日期与时刻整体成一个片段：不拆开才不会让模型看到可被重排的 {{P0}} {{P1}}
    ['发货日期 2026-01-05 14:30 已安排', '2026-01-05 14:30'],
    ['订单号 A-1234567', 'A-1234567'],
    ['发票号 INV-20260105', 'INV-20260105'],
    ['客服电话 400-820-8820', '400-820-8820'],
    ['版本 v1.2.3', 'v1.2.3'],
  ]
  for (const [input, expected] of cases) {
    const { text, slots } = protectSegments(input)
    assert.ok(slots.includes(expected), `${input} → 未保护 ${expected}（slots=${JSON.stringify(slots)}）`)
    assert.ok(text.includes('{{P'), `${input} 未产生占位符`)
    assert.equal(restoreSegments(text, slots), input)
  }
})

test('邮箱地址与 HTML 实体被保护', () => {
  const a = protectSegments('发件人 noreply@shop.example.com')
  assert.ok(a.slots.includes('noreply@shop.example.com'))
  const b = protectSegments('A&nbsp;B&amp;C')
  assert.ok(b.slots.includes('&nbsp;'))
  assert.equal(restoreSegments(b.text, b.slots), 'A&nbsp;B&amp;C')
})

test('同一片段重复出现复用同一个占位符（不让提示词被撑大）', () => {
  const { text, slots } = protectSegments('见 https://a.com 与 https://a.com 相同')
  assert.equal(slots.length, 1, `应去重，实际 ${JSON.stringify(slots)}`)
  assert.equal((text.match(/\{\{P0\}\}/g) || []).length, 2)
  assert.equal(restoreSegments(text, slots), '见 https://a.com 与 https://a.com 相同')
})

test('模型改写占位符编号时不至于毁掉正文（未命中的占位符原样留下）', () => {
  // 模拟模型把 {{P0}} 改成了 {{PO}}（字母 O）——还原不该崩，也不该删词
  const out = restoreSegments('见 {{PO}} 谢谢', ['https://a.com'])
  assert.match(out, /\{\{PO\}\}/)
})

// ── 分块 ────────────────────────────────────────────────────────────────────

test('短正文不切块', () => {
  assert.equal(splitForTranslation('很短的一封信').length, 1)
  assert.equal(splitForTranslation('').length, 0)
})

test('长正文按段落边界切块，不把句子劈开', () => {
  const para = 'x'.repeat(500)
  const body = Array.from({ length: 30 }, () => para).join('\n\n')
  const chunks = splitForTranslation(body)
  assert.ok(chunks.length > 1)
  for (const c of chunks) {
    assert.ok(c.length <= TRANSLATE_MAX_CHARS, `块过大 ${c.length}`)
    // 每块都应以段落边界收尾（除硬切情况）
  }
  // 拼回去必须与原文完全一致（顺序与内容都不能丢）
  assert.equal(chunks.join(''), body)
})

test('单段超硬上限时按行硬切，不丢内容', () => {
  const line = 'y'.repeat(500)
  const body = Array.from({ length: 40 }, (_, i) => `${i}${line}`).join('\n')
  const chunks = splitForTranslation(body)
  assert.ok(chunks.length > 1)
  assert.equal(chunks.join(''), body)
})

test('分块时每块不超过硬上限', () => {
  const body = Array.from({ length: 200 }, (_, i) => `段落${i} `.repeat(80)).join('\n\n')
  for (const c of splitForTranslation(body)) {
    assert.ok(c.length <= TRANSLATE_MAX_CHARS, `块 ${c.length} 超过硬上限`)
  }
  assert.ok(TRANSLATE_MAX_CHARS > TRANSLATE_CHUNK_CHARS)
})

// ── 翻译管线 ────────────────────────────────────────────────────────────────

test('translateWithProtection 端到端还原被保护的片段', async () => {
  // 假 chat：把占位符原样搬过去，模拟「模型守规矩」
  const fakeChat = async (prompt) => {
    assert.match(prompt, /\{\{P0\}\}/, '提示词里应带占位符')
    assert.match(prompt, /原样输出这些占位符/, '提示词应告知占位符规则')
    return '请访问 {{P0}} 查看订单 {{P1}}'
  }
  const out = await translateWithProtection(
    'Please visit https://a.com/x for order A-999',
    'zh-CN',
    fakeChat,
  )
  assert.equal(out, '请访问 https://a.com/x 查看订单 A-999')
})

test('翻译长正文时逐块处理并按序拼回', async () => {
  const body = Array.from({ length: 30 }, (_, i) => `第${i}段内容。`.repeat(20)).join('\n\n')
  let calls = 0
  const out = await translateBodyPipeline(body, 'zh-CN', async (p) => {
    calls++
    return `[译]${p.slice(-4)}`
  })
  assert.ok(calls > 1, '长正文应分多次请求')
  assert.ok(out.startsWith('[译]'))
})

test('单块翻译失败时降级为原文，不影响其余块', async () => {
  const body = Array.from({ length: 30 }, (_, i) => `段落${i}`.repeat(60)).join('\n\n')
  let n = 0
  const out = await translateBodyPipeline(body, 'zh-CN', async () => {
    n++
    if (n === 2) throw new Error('模拟网络失败')
    return '译文'
  })
  // 有一块失败 → 那一块保留原文，其余是译文
  assert.match(out, /段落/)
  assert.match(out, /译文/)
})

test('短正文单块失败时整段退回原文（不抛）', async () => {
  const out = await translateBodyPipeline('Hello there', 'zh-CN', async () => {
    throw new Error('boom')
  })
  assert.equal(out, 'Hello there')
})

test('目标为 original 时不请求模型', async () => {
  let called = false
  const out = await translateBodyPipeline('Hello', 'original', async () => {
    called = true
    return 'x'
  })
  assert.equal(out, 'Hello')
  assert.equal(called, false)
})

// ── 源语识别 ────────────────────────────────────────────────────────────────

test('detectSourceLang: 日文靠假名与中文区分开', () => {
  assert.equal(detectSourceLang('請求書を発行しました。'), 'ja')
  assert.equal(detectSourceLang('こんにちは、ご確認をお願いします。'), 'ja')
  // 纯汉字无假名 → 中文
  assert.equal(detectSourceLang('您的发票已开具'), 'zh')
})

test('detectSourceLang: 韩文 / 英文 / 中文', () => {
  assert.equal(detectSourceLang('청구서가 발행되었습니다.'), 'ko')
  assert.equal(detectSourceLang('Your invoice is ready.'), 'en')
  assert.equal(detectSourceLang('您的发票已开具，请查收。'), 'zh')
  assert.equal(detectSourceLang(''), 'other')
})

test('detectSourceLang: 中英混排按字数占比', () => {
  // 汉字多于拉丁 → 中文
  assert.equal(detectSourceLang('您的 invoice 已开具，请注意查收附件。'), 'zh')
  // 拉丁多于汉字 → 英文（技术邮件常见）
  assert.equal(detectSourceLang('Please review the attached invoice document now'), 'en')
})

test('detectSourceLang: HTML 标签不干扰判定', () => {
  assert.equal(detectSourceLang('<div class="wrapper"><p>您好，账单已生成。</p></div>'), 'zh')
  assert.equal(detectSourceLang('<div class="a"><p>Your bill is ready.</p></div>'), 'en')
})

test('detectSourceLang: 泰文/西里尔等判为 other（交给模型）', () => {
  assert.equal(detectSourceLang('สวัสดีครับ'), 'other')
  assert.equal(detectSourceLang('Здравствуйте'), 'other')
})

// ── 是否需要翻译 ────────────────────────────────────────────────────────────

test('shouldTranslate: 源语 == 目标语则跳过', () => {
  assert.equal(shouldTranslate('您好，账单已生成。', 'zh-CN'), false)
  assert.equal(shouldTranslate('Your invoice is ready.', 'en-US'), false)
  assert.equal(shouldTranslate('請求書を発行しました。', 'ja-JP'), false)
})

test('shouldTranslate: 源语 != 目标语则翻译', () => {
  assert.equal(shouldTranslate('Your invoice is ready.', 'zh-CN'), true)
  assert.equal(shouldTranslate('您好，账单已生成。', 'en-US'), true)
  assert.equal(shouldTranslate('請求書を発行しました。', 'zh-CN'), true)
})

test('shouldTranslate: 简繁视为同一语系，简体正文不因目标是 zh-TW 而重翻', () => {
  // 简体原文 + zh-TW 目标：不翻。硬做简繁转换反而容易把「发」改成「發」
  // 之外的错字（各地用字差异极大），得不偿失；真需要繁体的用户仍可手动选。
  assert.equal(shouldTranslate('您的账单已生成。', 'zh-TW'), false)
  // 但英文原文选 zh-TW 仍然要翻
  assert.equal(shouldTranslate('Your bill is ready.', 'zh-TW'), true)
})

test('shouldTranslate: 识别不出的语言选择翻译（宁可翻不错过）', () => {
  assert.equal(shouldTranslate('สวัสดีครับ', 'zh-CN'), true)
  assert.equal(shouldTranslate('', 'zh-CN'), false)
  assert.equal(shouldTranslate('hello', 'original'), false)
})
