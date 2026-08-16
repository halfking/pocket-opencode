import { strict as assert } from 'node:assert'
import { test } from 'node:test'

import {
  pickVoice,
  splitForSpeech,
  stripMarkdownForSpeech,
} from '../useSpeech.ts'

test('pickVoice：zh-CN 精确前缀 + 本地服务优先（避开 zh-TW）', () => {
  const voices = [
    { lang: 'en-US', localService: true, name: 'English' },
    { lang: 'zh-TW', localService: true, name: 'Tw' },
    { lang: 'zh-CN', localService: false, name: 'CloudZh' },
    { lang: 'zh-CN', localService: true, name: 'LocalZh' },
  ]
  assert.equal(pickVoice(voices, 'zh-CN')?.name, 'LocalZh')
  // 宽前缀 zh 时 zh-TW 也匹配：本地优先取 Tw（前缀语义）
  assert.equal(pickVoice(voices, 'zh')?.name, 'Tw')
  // 全云端时退回首条匹配
  assert.equal(pickVoice(voices.slice(0, 3), 'zh-CN')?.name, 'CloudZh')
})

test('pickVoice：无匹配语言返回 null（用系统默认），大小写不敏感', () => {
  assert.equal(pickVoice([{ lang: 'en-US' }], 'zh'), null)
  assert.equal(pickVoice([{ lang: 'ZH-cn' }], 'zh')?.lang, 'ZH-cn')
})

test('stripMarkdownForSpeech：代码块折叠、行内代码/链接/标题标记剥离', () => {
  const md = [
    '## 标题',
    '正文有 `inline code` 和 [链接](https://x.com)。',
    '```ts',
    'const a = 1',
    '```',
    '**加粗** 与 _斜体_ 列表：',
    '- 项目一',
    '- 项目二',
  ].join('\n')
  const out = stripMarkdownForSpeech(md)
  assert.ok(out.includes('标题'), '标题保留')
  assert.ok(out.includes('inline code'), '行内代码内容保留')
  assert.ok(out.includes('链接') && !out.includes('https://'), '链接语法剥离')
  assert.ok(out.includes('（代码略）') && !out.includes('const a'), '代码块折叠')
  assert.ok(out.includes('加粗') && !out.includes('**'), '强调标记剥离')
  assert.ok(out.includes('项目一') && !/-\s/.test(out), '列表标记剥离')
})

test('stripMarkdownForSpeech：裸 URL 替换为（链接略）', () => {
  const out = stripMarkdownForSpeech('见 https://example.com/a?b=1 说明')
  assert.ok(!out.includes('example.com'))
  assert.ok(out.includes('（链接略）'))
})

test('splitForSpeech：短文本单块返回', () => {
  assert.deepEqual(splitForSpeech('你好'), ['你好'])
})

test('splitForSpeech：超长文本按句末标点切分且不丢内容', () => {
  const sentence = '这是一句话。'
  const text = sentence.repeat(400) // 2400 字 > 1200
  const parts = splitForSpeech(text)
  assert.ok(parts.length >= 2, '应切分多块')
  assert.equal(parts.join(''), text, '拼接后无损')
  for (const p of parts) assert.ok(p.endsWith('。'), '断点在句号后')
})

test('splitForSpeech：无标点长文本硬切不丢内容', () => {
  const text = '字'.repeat(3000)
  const parts = splitForSpeech(text)
  assert.equal(parts.join(''), text)
})
