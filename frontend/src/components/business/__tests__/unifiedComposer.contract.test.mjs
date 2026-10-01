import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import { readFile } from 'node:fs/promises'

const source = await readFile(
  new URL('../../business/UnifiedComposer.vue', import.meta.url),
  'utf8',
)

test('UnifiedComposer puts multimodal tools, agent, optimize and submit on one toolbar row', () => {
  // 工具行独立于文本区
  assert.match(source, /class="uc-toolbar"/)
  assert.match(source, /uc-tools-left/)
  assert.match(source, /uc-tools-right/)
  // 多模态入口 + 全屏
  for (const label of ['全屏编辑', '语音输入', '选择图片', '拍照', '选择文件']) {
    assert.ok(source.includes(label), `missing tool: ${label}`)
  }
  // 角色 chip + AI 优化 + 提交
  assert.match(source, /uc-chip/)
  assert.match(source, /uc-opt/)
  assert.match(source, /uc-submit/)
})

test('UnifiedComposer supports fullscreen article-editing mode', () => {
  assert.match(source, /Teleport to="body"/)
  assert.match(source, /class="uc-fs"/)
  assert.match(source, /aria-modal="true"/)
  assert.match(source, /charCount/)
  assert.match(source, /openFullscreen/)
  assert.match(source, /closeFullscreen/)
})

test('UnifiedComposer keeps the textarea wide, editable and copyable', () => {
  assert.match(source, /min-height: 88px/)
  assert.match(source, /max-height: 40vh/)
  assert.match(source, /user-select: text/)
  // 2026-10-03：这条原先断言的是 `font-size: 16px` 这个**字面形式**。
  // 它真正要锁的是「输入框字号是 16px」——iOS 上小于 16px 的输入框会在聚焦时
  // 被浏览器自动放大，页面整体错位。字号换成等值的 `var(--text-lg)`（也是
  // 16px）之后行为完全一样，字面形式却变了，于是这条红了。
  //
  // 判据问的应该是**实际字号**不是写法。两种等价写法都收。
  assert.match(
    source,
    /font-size\s*:\s*(?:16px|var\(--text-lg\))/,
    '输入框字号必须是 16px（等价 token：--text-lg）——更小的字号会让 iOS 聚焦时自动放大、页面错位',
  )
})

test('UnifiedComposer reuses shared multimodal composables and agent sheet', () => {
  assert.match(source, /useVoiceInput/)
  assert.match(source, /useAttachments/)
  assert.match(source, /useCameraCapture/)
  assert.match(source, /usePromptOptimizer/)
  assert.match(source, /AgentSelectorSheet/)
})

test('UnifiedComposer emits the unified contract', () => {
  assert.match(source, /'update:modelValue'/)
  assert.match(source, /'update:agentId'/)
  assert.match(source, /'submit', payload: \{ text: string; images: string\[\] \}/)
  assert.match(source, /'optimized'/)
})
