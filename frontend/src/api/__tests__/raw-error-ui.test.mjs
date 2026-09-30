/**
 * 「原始错误文本直接上屏」的回归护栏。
 *
 * 缺陷背景（真机 Redmi 14R 5G 实测，四次独立发现同一类问题）：
 *   1. RSS 页         → `rss_unavailable: store not configured`
 *   2. 登录页         → `Failed to fetch`
 *   3. AI 网关设置页  → `✗ Failed to fetch`（`setStatus('error', '✗ ' + err.message)`，
 *                      写法与前两次不同，被前一轮 codemod 的模式漏掉）
 *   4. 笔记录音面板   → 转写失败时显示 recordingRuntime 透传的原始异常
 *
 * 同一个缺陷类反复从不同写法里漏出来，说明必须用扫描守住，而不是靠逐个 review。
 * 判定逻辑见 scripts/audit-raw-error-ui.mjs（这里只做断言）。
 *
 * 刻意接受的例外：
 *   - 自定义错误类的展示分支（如 DownloadUnsupportedError，message 本就是写给用户看的）
 *   - store 里 `error.value = e?.message` 暂存原始值 —— 配套护栏是
 *     store-error-consumers.test.mjs（查有没有视图不归一就渲染）
 *
 * Run: node --test src/api/__tests__/raw-error-ui.test.mjs
 */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, dirname, extname, relative } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..', '..', '..')
const SRC = join(ROOT, 'src')
const SKIP = new Set(['node_modules', '.git', 'dist', '__tests__', 'android', 'ios'])

const UI_SINKS = /(?:error\.value\s*=|testResult\.value\s*=|status\.value\s*=|\btext\s*:|setStatus\(|toast\.(?:error|warn)\(|showToast\()/
const RAW_SOURCE = /(?:\b(?:err|e|error|ex|exc)\b\s*\??\s*\.\s*(?:message|body|response)|String\(\s*(?:err|e|error)\s*\))/
const NORMALIZED = /(?:toUserMessage|apiError|useApiError|resolveErrorI18nKey)/
const CURATED_BRANCH = /instanceof\s+(\w+)/

function collect(dir, out = []) {
  for (const name of readdirSync(dir)) {
    if (SKIP.has(name)) continue
    const full = join(dir, name)
    if (statSync(full).isDirectory()) collect(full, out)
    else if (['.vue', '.ts'].includes(extname(name))) out.push(full)
  }
  return out
}

function isCuratedBranch(lines, i) {
  for (let k = Math.max(0, i - 3); k <= i; k++) {
    const m = lines[k].match(CURATED_BRANCH)
    if (m && m[1] !== 'Error') return true
  }
  return false
}

function isStoreStash(file, line) {
  // \b 词边界即可：`error.value` 与 `this.error.value` 都要认出来
  if (!/\berror\.value\s*=/.test(line)) return false
  const rel = relative(SRC, file).replace(/\\/g, '/')
  return (
    /^stores\//.test(rel)
    || /(^|\/)store\.ts$/.test(rel)
    || /\/store\/[^/]+\.ts$/.test(rel)
    // native/*Runtime.ts 同属「暂存原始、消费侧归一」：
    // recordingRuntime 转写失败时存原始异常，NoteRecordingStudio 渲染前归一。
    || /^native\/[A-Za-z]*Runtime\.ts$/.test(rel)
  )
}

function findHits() {
  const hits = []
  for (const file of collect(SRC)) {
    const lines = readFileSync(file, 'utf8').split(/\r?\n/)
    lines.forEach((line, i) => {
      if (/^\s*(?:const|let|var)?\s*\/\//.test(line)) return
      if (!UI_SINKS.test(line) || !RAW_SOURCE.test(line)) return
      if (NORMALIZED.test(line)) return
      if (isCuratedBranch(lines, i)) return
      if (isStoreStash(file, line)) return
      hits.push(`${relative(SRC, file).replace(/\\/g, '/')}:${i + 1}  ${line.trim().slice(0, 100)}`)
    })
  }
  return hits
}

describe('原始错误文本不得直接上屏', () => {
  const hits = findHits()

  it('扫描到了足够的源文件（防止路径写错导致空跑通过）', () => {
    assert.ok(collect(SRC).length > 400, '扫描范围可能失效')
  })

  it('不存在把后端/网络原始错误文本直接赋给可见 UI 的写法', () => {
    assert.equal(
      hits.length,
      0,
      `发现 ${hits.length} 处原始错误文本上屏：\n  - ${hits.join('\n  - ')}\n` +
      '  修法：组件里用 useApiError()（两行接入），store 里用 i18n.global.t + toUserMessage；\n' +
      '  原始信息 console.warn 留档。',
    )
  })
})
