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
  assert.match(source, /font-size: 16px/)
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
