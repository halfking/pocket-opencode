# Phase C: ENEX 导入 — 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在前端解析 ENEX（Evernote export XML），写入 `local_assets` 表（source='enex_import'），让用户从 Evernote 导入笔记到 Pocket。

**Architecture:**
- 前端用 `fast-xml-parser` 解析 ENEX XML。
- 附件（`<resource>` 中的 base64）走 `assetStore.addBlob()` 加密存储（保留 SQLCipher 全库加密边界）。
- 笔记正文 ENML → Markdown 转换（基础标签映射即可）。
- 去重基于 `metaJson.enexId + originalCreated`。
- UI 入口在 `NoteListView` 顶栏"导入"按钮。

**Tech Stack:** Vue 3 + TypeScript / fast-xml-parser + jszip (optional) / 现有 assetStore。

---

## 文件结构

```
frontend/
├── package.json                      # (改) 加 fast-xml-parser + jszip
├── src/
│   ├── features/
│   │   └── imports/
│   │       ├── evernote-parser.ts    # (新增) ENEX 解析器
│   │       ├── enml-to-markdown.ts   # (新增) ENML → Markdown
│   │       └── EvernoteImporter.vue  # (新增) 导入对话框
│   ├── api/
│   │   └── imports.ts                # (新增) 导入 API（本地落库）
│   └── features/notes/
│       └── NoteListView.vue          # (改) 顶栏加"导入"按钮
```

---

## Task 1: 加依赖 fast-xml-parser + jszip

**Files:**
- Modify: `frontend/package.json`

- [ ] **Step 1: 安装依赖**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/frontend
npm install fast-xml-parser@^4 jszip@^3
```

期望：`package.json` 中增加 `dependencies."fast-xml-parser"` 与 `"jszip"`。

- [ ] **Step 2: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add frontend/package.json frontend/package-lock.json
git commit -m "deps: 加 fast-xml-parser + jszip 支持 ENEX 导入"
```

---

## Task 2: enml-to-markdown 基础标签转换

**Files:**
- Create: `frontend/src/features/imports/enml-to-markdown.ts`

- [ ] **Step 1: 创建转换函数**

新建 `frontend/src/features/imports/enml-to-markdown.ts`：

```ts
/**
 * enmlToMarkdown — 极简 ENML → Markdown 转换器。
 * ENML 是 Evernote 的 XHTML 子集；这里只覆盖常用标签，复杂排版保留 HTML。
 *
 * 支持：<en-note>, <en-media>, <h1>-<h6>, <p>, <div>, <span>, <b>, <i>, <u>,
 *       <strike>, <ul>, <ol>, <li>, <a>, <br>, <code>, <pre>, <blockquote>,
 *       <img>, <en-todo>
 */

const ENML_BLOCK_TAGS = ['p', 'div', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'ul', 'ol', 'li', 'pre', 'blockquote', 'hr']
const ENML_INLINE_TAGS = ['b', 'i', 'u', 'strike', 'code', 'span', 'a', 'br', 'img']

function escapeMd(text: string): string {
  return text.replace(/([\\`*_{}[\]()#+\-.!>])/g, '\\$1')
}

function attrsToString(attrs: Record<string, string> = {}): string {
  return Object.entries(attrs).map(([k, v]) => ` ${k}="${v}"`).join('')
}

export function enmlToMarkdown(enml: string): string {
  if (!enml) return ''
  // ENML 包在 <en-note> 里，先剥离外层
  const body = enml.replace(/<\/?en-note[^>]*>/g, '')
  // 使用 DOMParser 解析
  const doc = new DOMParser().parseFromString(`<root>${body}</root>`, 'text/html')
  return walk(doc.body.firstElementChild?.parentElement || doc.body).trim()
}

function walk(node: Node | null, listMarker = '-'): string {
  if (!node) return ''
  let result = ''
  for (const child of Array.from(node.childNodes)) {
    if (child.nodeType === Node.TEXT_NODE) {
      result += child.textContent || ''
      continue
    }
    if (child.nodeType !== Node.ELEMENT_NODE) continue
    const el = child as Element
    const tag = el.tagName.toLowerCase()
    const inner = walk(el, listMarker)

    switch (tag) {
      case 'h1': result += `\n# ${inner}\n\n`; break
      case 'h2': result += `\n## ${inner}\n\n`; break
      case 'h3': result += `\n### ${inner}\n\n`; break
      case 'h4': result += `\n#### ${inner}\n\n`; break
      case 'h5': result += `\n##### ${inner}\n\n`; break
      case 'h6': result += `\n###### ${inner}\n\n`; break
      case 'p': result += `\n${inner}\n\n`; break
      case 'div': result += `\n${inner}\n`; break
      case 'br': result += '\n'; break
      case 'hr': result += '\n---\n'; break
      case 'b':
      case 'strong': result += `**${inner}**`; break
      case 'i':
      case 'em': result += `*${inner}*`; break
      case 'u': result += `<u>${inner}</u>`; break
      case 'strike':
      case 's':
      case 'del': result += `~~${inner}~~`; break
      case 'code': result += `\`${inner}\``; break
      case 'pre': result += `\n\`\`\`\n${inner}\n\`\`\`\n\n`; break
      case 'blockquote': result += `\n> ${inner.replace(/\n/g, '\n> ')}\n\n`; break
      case 'a': {
        const href = el.getAttribute('href') || ''
        result += `[${inner}](${href})`
        break
      }
      case 'img': {
        const src = el.getAttribute('src') || ''
        const alt = el.getAttribute('alt') || ''
        result += `![${alt}](${src})`
        break
      }
      case 'ul': result += `\n${walk(el, '-')}\n`; break
      case 'ol': result += `\n${walk(el, '1.')}\n`; break
      case 'li': result += `${listMarker} ${inner.trim()}\n`; break
      case 'en-todo': {
        const checked = el.getAttribute('checked') === 'true'
        result += checked ? `- [x] ${inner}\n` : `- [ ] ${inner}\n`
        break
      }
      case 'en-media': {
        const hash = el.getAttribute('hash') || ''
        result += `\n[附件: ${hash}]\n\n`
        break
      }
      case 'span': result += inner; break
      default: result += inner
    }
  }
  return result
}
```

- [ ] **Step 2: 单元测试**

新建 `frontend/src/features/imports/enml-to-markdown.test.ts`：

```ts
import { describe, it, expect } from 'vitest'
import { enmlToMarkdown } from './enml-to-markdown'

describe('enml-to-markdown', () => {
  it('基本段落转换', () => {
    const html = '<en-note><p>Hello <b>World</b></p></en-note>'
    expect(enmlToMarkdown(html)).toBe('Hello **World**')
  })

  it('标题转换', () => {
    const html = '<en-note><h1>Title</h1><h2>Sub</h2></en-note>'
    expect(enmlToMarkdown(html)).toContain('# Title')
    expect(enmlToMarkdown(html)).toContain('## Sub')
  })

  it('列表转换', () => {
    const html = '<en-note><ul><li>A</li><li>B</li></ul></en-note>'
    expect(enmlToMarkdown(html)).toContain('- A')
    expect(enmlToMarkdown(html)).toContain('- B')
  })

  it('en-todo checkbox', () => {
    const html = '<en-note><en-todo checked="true">done</en-todo><en-todo checked="false">todo</en-todo></en-note>'
    const md = enmlToMarkdown(html)
    expect(md).toContain('- [x] done')
    expect(md).toContain('- [ ] todo')
  })

  it('链接转换', () => {
    const html = '<en-note><a href="https://x.com">X</a></en-note>'
    expect(enmlToMarkdown(html)).toBe('[X](https://x.com)')
  })
})
```

- [ ] **Step 3: 运行测试**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/frontend
npx vitest run src/features/imports/enml-to-markdown.test.ts
```

期望：5 个 test 通过。

- [ ] **Step 4: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add frontend/src/features/imports/enml-to-markdown.ts \
        frontend/src/features/imports/enml-to-markdown.test.ts
git commit -m "feat(imports): enml-to-markdown 转换器 + 单测"
```

---

## Task 3: evernote-parser 解析 ENEX

**Files:**
- Create: `frontend/src/features/imports/evernote-parser.ts`

- [ ] **Step 1: 创建 parser**

新建 `frontend/src/features/imports/evernote-parser.ts`：

```ts
import { XMLParser } from 'fast-xml-parser'
import { enmlToMarkdown } from './enml-to-markdown'

export interface EvernoteResource {
  data: string  // base64
  mime: string
  filename: string | null
  hash: string
}

export interface EvernoteNote {
  title: string
  content: string  // ENML
  contentMarkdown: string
  createdAt: number | null
  updatedAt: number | null
  tags: string[]
  author: string | null
  sourceUrl: string | null
  sourceApp: string | null
  latitude: number | null
  longitude: number | null
  attributes: Record<string, string>
  enexId: string | null  // 通常是 guid hash
  resources: EvernoteResource[]
}

const parser = new XMLParser({
  ignoreAttributes: false,
  attributeNamePrefix: '@_',
  parseAttributeValue: false,
  parseTagValue: false,
  trimValues: true,
})

export function parseEnex(xmlContent: string): EvernoteNote[] {
  const doc = parser.parse(xmlContent)
  const exportNode = doc['en-export'] || doc['en-tombstone'] || {}
  const notes = Array.isArray(exportNode.note) ? exportNode.note : exportNode.note ? [exportNode.note] : []

  return notes.map((n: any) => parseNote(n))
}

function parseNote(n: any): EvernoteNote {
  const title = unescape(n.title || '')
  const content = n.content || ''
  const created = parseDate(n.created)
  const updated = parseDate(n.updated)
  const tagNode = n.tag
  const tags = Array.isArray(tagNode) ? tagNode.map(String) : tagNode ? [String(tagNode)] : []
  const resources = parseResources(n.resource)
  return {
    title,
    content,
    contentMarkdown: enmlToMarkdown(content),
    createdAt: created,
    updatedAt: updated,
    tags,
    author: n['note-attributes']?.author ?? null,
    sourceUrl: n['note-attributes']?.['source-url'] ?? null,
    sourceApp: n['note-attributes']?.['source-application'] ?? null,
    latitude: parseFloatOrNull(n['note-attributes']?.['latitude']),
    longitude: parseFloatOrNull(n['note-attributes']?.['longitude']),
    attributes: parseAttributes(n['note-attributes']),
    enexId: hashContent(`${title}|${created}|${updated}`),
    resources,
  }
}

function parseResources(node: any): EvernoteResource[] {
  if (!node) return []
  const arr = Array.isArray(node) ? node : [node]
  return arr
    .map((r: any) => {
      const data = r?.data?.[''] || r?.data || ''
      const mime = r?.mime || 'application/octet-stream'
      const filename = r?.['resource-attributes']?.['file-name'] ?? null
      const hash = r?.['resource-attributes']?.['attachment-hash'] || hashContent(data)
      return { data: String(data), mime: String(mime), filename: filename ? String(filename) : null, hash: String(hash) }
    })
    .filter((r: EvernoteResource) => r.data)
}

function parseDate(s: any): number | null {
  if (!s) return null
  const d = new Date(String(s))
  return isNaN(d.getTime()) ? null : d.getTime()
}

function parseFloatOrNull(s: any): number | null {
  if (s === undefined || s === null) return null
  const v = parseFloat(String(s))
  return isNaN(v) ? null : v
}

function parseAttributes(node: any): Record<string, string> {
  if (!node) return {}
  const result: Record<string, string> = {}
  for (const [k, v] of Object.entries(node)) {
    if (k.startsWith('@_') || k === 'author' || k === 'source-url' || k === 'source-application') continue
    result[k] = String(v ?? '')
  }
  return result
}

function unescape(s: string): string {
  return String(s || '')
    .replace(/&amp;/g, '&')
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&quot;/g, '"')
    .replace(/&apos;/g, "'")
}

function hashContent(s: string): string {
  // 简易 hash；生产用 crypto.subtle.digest
  let h = 0
  for (let i = 0; i < s.length; i++) {
    h = ((h << 5) - h + s.charCodeAt(i)) | 0
  }
  return `enex-${Math.abs(h).toString(36)}-${s.length}`
}
```

- [ ] **Step 2: 单元测试**

新建 `frontend/src/features/imports/evernote-parser.test.ts`：

```ts
import { describe, it, expect } from 'vitest'
import { parseEnex } from './evernote-parser'

const SAMPLE_ENEX = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE en-export SYSTEM "http://xml.evernote.com/pub/evernote-export3.dtd">
<en-export export-date="20240101" application="Evernote" version="6.x">
  <note>
    <title>Test Note</title>
    <content><![CDATA[<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE en-note SYSTEM "http://xml.evernote.com/pub/enml2.dtd"><en-note><p>Hello</p></en-note>]]></content>
    <created>20240101T120000Z</created>
    <updated>20240101T120000Z</updated>
    <tag>tag1</tag>
    <tag>tag2</tag>
    <note-attributes>
      <author>Tester</author>
      <source-url>https://example.com</source-url>
    </note-attributes>
  </note>
</en-export>`

describe('evernote-parser', () => {
  it('解析单条 note', () => {
    const notes = parseEnex(SAMPLE_ENEX)
    expect(notes).toHaveLength(1)
    expect(notes[0].title).toBe('Test Note')
    expect(notes[0].tags).toEqual(['tag1', 'tag2'])
    expect(notes[0].author).toBe('Tester')
    expect(notes[0].contentMarkdown).toContain('Hello')
    expect(notes[0].enexId).toMatch(/^enex-/)
  })

  it('空 ENEX 返回空数组', () => {
    expect(parseEnex('<?xml version="1.0"?><en-export></en-export>')).toEqual([])
  })
})
```

- [ ] **Step 3: 运行测试**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/frontend
npx vitest run src/features/imports/evernote-parser.test.ts
```

期望：2 个 test 通过。

- [ ] **Step 4: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add frontend/src/features/imports/evernote-parser.ts \
        frontend/src/features/imports/evernote-parser.test.ts
git commit -m "feat(imports): evernote-parser 解析 ENEX + 单测"
```

---

## Task 4: imports API（本地落库）

**Files:**
- Create: `frontend/src/api/imports.ts`

- [ ] **Step 1: 创建 API**

新建 `frontend/src/api/imports.ts`：

```ts
/**
 * imports.ts — 本地资源导入（不经云端，保留 E2EE 边界）。
 */
import { assetStore } from '../native/asset-store'
import { useAuthStore } from '../stores/auth'
import type { EvernoteNote } from '../features/imports/evernote-parser'

export interface ImportProgress {
  total: number
  imported: number
  skipped: number
  failed: number
  errors: string[]
}

export interface ImportResult {
  progress: ImportProgress
  durationMs: number
}

export async function importEvernoteNotes(
  notes: EvernoteNote[],
  onProgress?: (p: ImportProgress) => void,
): Promise<ImportResult> {
  const auth = useAuthStore()
  const workspaceId = auth.workspaceId || 'default'
  const start = Date.now()
  const progress: ImportProgress = { total: notes.length, imported: 0, skipped: 0, failed: 0, errors: [] }

  for (const note of notes) {
    try {
      // 去重：基于 enexId + originalCreated 查 local_assets
      const existing = await assetStore.findByMeta({
        workspaceId,
        source: 'enex_import',
        metaKey: 'enexId',
        metaValue: note.enexId || '',
      })
      if (existing) {
        progress.skipped++
        onProgress?.(progress)
        continue
      }

      const asset = await assetStore.upsert({
        kind: 'note',
        workspaceId,
        title: note.title || '(无标题)',
        bodyText: note.contentMarkdown,
        source: 'enex_import',
        metaJson: {
          enexId: note.enexId,
          originalCreated: note.createdAt,
          originalUpdated: note.updatedAt,
          tags: note.tags,
          author: note.author,
          sourceUrl: note.sourceUrl,
          sourceApp: note.sourceApp,
          latitude: note.latitude,
          longitude: note.longitude,
          attributes: note.attributes,
        },
        createdAt: note.createdAt || Date.now(),
        updatedAt: note.updatedAt || Date.now(),
      })

      // 附件
      for (const resource of note.resources) {
        try {
          const blob = base64ToBlob(resource.data, resource.mime)
          await assetStore.addBlob({
            assetId: asset.id,
            kind: 'attachment',
            mime: resource.mime,
            filename: resource.filename,
            data: blob,
          })
        } catch (e: any) {
          progress.errors.push(`附件失败 ${resource.filename || resource.hash}: ${e?.message || e}`)
        }
      }
      progress.imported++
    } catch (e: any) {
      progress.failed++
      progress.errors.push(`笔记失败 ${note.title}: ${e?.message || e}`)
    }
    onProgress?.(progress)
  }

  return { progress, durationMs: Date.now() - start }
}

function base64ToBlob(base64: string, mime: string): Blob {
  const bytes = Uint8Array.from(atob(base64), (c) => c.charCodeAt(0))
  return new Blob([bytes], { type: mime })
}
```

- [ ] **Step 2: 验证 assetStore.findByMeta / addBlob API 存在**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
grep -n "findByMeta\|addBlob" frontend/src/native/asset-store.ts
```

若 `findByMeta` 不存在，需要在 `asset-store.ts` 补充：

```ts
async findByMeta(opts: { workspaceId: string; source: string; metaKey: string; metaValue: string }): Promise<LocalAsset | null> {
  const row = await localDB.get(
    `SELECT * FROM local_assets WHERE workspace_id = ? AND source = ? AND json_extract(meta_json, '$.${opts.metaKey}') = ? LIMIT 1`,
    [opts.workspaceId, opts.source, opts.metaValue]
  )
  return row ? toLocalAsset(row) : null
}
```

如不存，补充后一起提交。

- [ ] **Step 3: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add frontend/src/api/imports.ts
[ -n "$(git diff frontend/src/native/asset-store.ts)" ] && git add frontend/src/native/asset-store.ts
git commit -m "feat(imports): imports API 本地落库 (ENEX → local_assets)"
```

---

## Task 5: EvernoteImporter 对话框 UI

**Files:**
- Create: `frontend/src/features/imports/EvernoteImporter.vue`

- [ ] **Step 1: 创建组件**

新建 `frontend/src/features/imports/EvernoteImporter.vue`：

```vue
<!--
  EvernoteImporter — 文件选择 + 进度 + 结果对话框。
-->
<template>
  <div v-if="open" class="importer-mask" @click.self="close">
    <div class="importer-dialog">
      <h3>导入 Evernote 笔记</h3>
      <p class="hint">选择 .enex 文件，笔记会保存到本地加密存储。</p>

      <input v-if="!parsed" ref="fileInput" type="file" accept=".enex,application/xml,text/xml" @change="onFileChange" />

      <div v-if="parsing" class="state">解析中…</div>

      <div v-if="parsed && !importing && !result" class="preview">
        <p>共 {{ parsed.length }} 条笔记</p>
        <button class="primary" @click="startImport">开始导入</button>
        <button class="ghost" @click="reset">重新选择</button>
      </div>

      <div v-if="importing" class="progress">
        <div class="bar" :style="{ width: progressPercent + '%' }" />
        <p>{{ progressLabel }}</p>
      </div>

      <div v-if="result" class="result">
        <p>✓ 导入 {{ result.progress.imported }} 条</p>
        <p v-if="result.progress.skipped > 0">⊘ 跳过 {{ result.progress.skipped }} 条（已存在）</p>
        <p v-if="result.progress.failed > 0" class="error">✗ 失败 {{ result.progress.failed }} 条</p>
        <p class="hint">耗时 {{ Math.round(result.durationMs / 1000) }}s</p>
        <button class="primary" @click="close">完成</button>
      </div>

      <div v-if="errors.length" class="errors">
        <p class="error-title">错误详情：</p>
        <ul><li v-for="(e, i) in errors.slice(0, 10)" :key="i">{{ e }}</li></ul>
      </div>

      <button v-if="!importing && !result" class="ghost close-btn" @click="close">取消</button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import { parseEnex, type EvernoteNote } from './evernote-parser'
import { importEvernoteNotes, type ImportProgress, type ImportResult } from '../../api/imports'

const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{ (e: 'close'): void; (e: 'imported', count: number): void }>()

const fileInput = ref<HTMLInputElement | null>(null)
const parsing = ref(false)
const parsed = ref<EvernoteNote[] | null>(null)
const importing = ref(false)
const result = ref<ImportResult | null>(null)
const liveProgress = ref<ImportProgress | null>(null)
const errors = computed(() => liveProgress.value?.errors || [])

const progressPercent = computed(() => {
  if (!liveProgress.value || liveProgress.value.total === 0) return 0
  return Math.round((liveProgress.value.imported + liveProgress.value.skipped + liveProgress.value.failed) / liveProgress.value.total * 100)
})

const progressLabel = computed(() => {
  if (!liveProgress.value) return ''
  const p = liveProgress.value
  return `${p.imported + p.skipped + p.failed} / ${p.total}`
})

async function onFileChange(e: Event) {
  const file = (e.target as HTMLInputElement).files?.[0]
  if (!file) return
  parsing.value = true
  parsed.value = null
  try {
    const text = await file.text()
    parsed.value = parseEnex(text)
  } catch (err: any) {
    alert('ENEX 解析失败: ' + (err?.message || err))
  } finally {
    parsing.value = false
  }
}

async function startImport() {
  if (!parsed.value) return
  importing.value = true
  try {
    result.value = await importEvernoteNotes(parsed.value, (p) => {
      liveProgress.value = { ...p }
    })
    emit('imported', result.value.progress.imported)
  } finally {
    importing.value = false
  }
}

function reset() {
  parsed.value = null
  liveProgress.value = null
  if (fileInput.value) fileInput.value.value = ''
}

function close() {
  reset()
  result.value = null
  emit('close')
}
</script>

<style scoped>
.importer-mask { position: fixed; inset: 0; background: rgba(0,0,0,0.5); display: flex; align-items: center; justify-content: center; z-index: 100; }
.importer-dialog { background: var(--bg-card); border-radius: var(--radius-md); padding: var(--space-4); width: min(440px, 92vw); max-height: 80vh; overflow: auto; display: flex; flex-direction: column; gap: var(--space-3); }
.importer-dialog h3 { margin: 0; font-size: 16px; }
.hint { font-size: 12px; color: var(--text-secondary); }
.state { padding: var(--space-3); text-align: center; }
.preview { display: flex; flex-direction: column; gap: var(--space-2); }
.progress .bar { height: 8px; background: var(--brand-primary); border-radius: 4px; transition: width 0.2s; }
.progress { background: var(--bg-subtle); border-radius: var(--radius-sm); padding: var(--space-3); }
.progress p { margin: var(--space-2) 0 0; font-size: 13px; }
.result { padding: var(--space-3); background: var(--bg-subtle); border-radius: var(--radius-sm); }
.result p { margin: var(--space-1) 0; }
.error { color: var(--danger); }
.errors { max-height: 200px; overflow: auto; font-size: 12px; }
.errors ul { padding-left: var(--space-4); margin: var(--space-1) 0; }
.primary, .ghost { padding: var(--space-2) var(--space-3); border-radius: var(--radius-sm); cursor: pointer; font-size: 14px; }
.primary { background: var(--brand-primary); color: white; border: 0; }
.ghost { background: transparent; border: 1px solid var(--border); }
.close-btn { align-self: flex-end; }
</style>
```

- [ ] **Step 2: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add frontend/src/features/imports/EvernoteImporter.vue
git commit -m "feat(imports): EvernoteImporter 对话框 UI"
```

---

## Task 6: NoteListView 顶栏"导入"按钮

**Files:**
- Modify: `frontend/src/features/notes/NoteListView.vue`

- [ ] **Step 1: 引入 EvernoteImporter**

在 NoteListView 的 `<script setup>` 顶部加：

```ts
import EvernoteImporter from '../imports/EvernoteImporter.vue'
```

- [ ] **Step 2: 增加状态**

```ts
const showImporter = ref(false)
async function onImported() {
  await load()
}
```

- [ ] **Step 3: 顶栏加按钮**

在 `.search-bar` 之前加：

```vue
<div class="toolbar">
  <button class="import-btn" @click="showImporter = true">📥 导入 ENEX</button>
</div>
```

并在 template 末尾：

```vue
<EvernoteImporter :open="showImporter" @close="showImporter = false" @imported="onImported" />
```

- [ ] **Step 4: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add frontend/src/features/notes/NoteListView.vue
git commit -m "feat(notes): NoteListView 顶栏加 ENEX 导入入口"
```

---

## Task 7: e2e 验收

**Files:**
- Create: `frontend/tests/e2e/enex-import.spec.ts`

- [ ] **Step 1: 测试 fixture**

新建 `frontend/tests/e2e/fixtures/sample.enex`：

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE en-export SYSTEM "http://xml.evernote.com/pub/evernote-export3.dtd">
<en-export export-date="20240101" application="Evernote" version="6.x">
  <note>
    <title>测试笔记</title>
    <content><![CDATA[<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE en-note SYSTEM "http://xml.evernote.com/pub/enml2.dtd"><en-note><p>Hello <b>ENEX</b></p></en-note>]]></content>
    <created>20240101T120000Z</created>
    <updated>20240101T120000Z</updated>
    <tag>test</tag>
  </note>
</en-export>
```

- [ ] **Step 2: 创建测试**

新建 `frontend/tests/e2e/enex-import.spec.ts`：

```ts
import { test, expect } from '@playwright/test'
import path from 'path'

test.describe('ENEX 导入', () => {
  test('选择 .enex 文件并导入', async ({ page }) => {
    await page.goto('/#/notes')
    await page.click('button:has-text("📥 导入 ENEX")')
    const filePath = path.join(__dirname, 'fixtures/sample.enex')
    await page.setInputFiles('input[type=file]', filePath)
    await expect(page.locator('text=共 1 条笔记')).toBeVisible()
    await page.click('button:has-text("开始导入")')
    await expect(page.locator('text=✓ 导入 1 条')).toBeVisible({ timeout: 10_000 })
  })

  test('重复导入会跳过', async ({ page }) => {
    await page.goto('/#/notes')
    await page.click('button:has-text("📥 导入 ENEX")')
    const filePath = path.join(__dirname, 'fixtures/sample.enex')
    await page.setInputFiles('input[type=file]', filePath)
    await page.click('button:has-text("开始导入")')
    await page.waitForTimeout(500)
    await page.click('button:has-text("完成")')
    // 再次导入
    await page.click('button:has-text("📥 导入 ENEX")')
    await page.setInputFiles('input[type=file]', filePath)
    await page.click('button:has-text("开始导入")')
    await expect(page.locator('text=⊘ 跳过 1 条')).toBeVisible()
  })
})
```

- [ ] **Step 3: 运行测试**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/frontend
npx playwright test tests/e2e/enex-import.spec.ts --reporter=list
```

期望：2 个 test 通过。

- [ ] **Step 4: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add frontend/tests/e2e/enex-import.spec.ts frontend/tests/e2e/fixtures/sample.enex
git commit -m "test(imports): ENEX 导入 e2e 验收"
```

---

## Self-Review

**1. Spec 覆盖（设计文档 §2.2.B）**：
- [x] fast-xml-parser + jszip 加依赖 → Task 1
- [x] enml-to-markdown → Task 2
- [x] evernote-parser → Task 3
- [x] imports API 写 local_assets → Task 4
- [x] EvernoteImporter UI → Task 5
- [x] NoteListView 入口 → Task 6
- [x] 去重（enexId + originalCreated）→ Task 4 findByMeta
- [x] 附件 base64 → blob → addBlob → Task 4
- [x] e2e + fixture → Task 7

**2. 占位符扫描**：无。

**3. 类型一致性**：
- `EvernoteNote.enexId: string | null` → Task 4 `findByMeta` 与 `metaJson.enexId` 一致。
- `assetStore.addBlob({assetId, kind:'attachment', mime, filename, data: Blob})` 与 `asset-store.ts:238-249` 一致。

**4. 风险**：
- ENEX >100MB 时 WebView 内存可能爆；e2e 不覆盖，需在生产前加"分批导入"（v1.5）。
- ENEX 内嵌资源 base64 解析在某些 ENML 命名空间不规范时可能失败；单测覆盖常见标签。
- `findByMeta` 可能不在 assetStore 中，Task 4 Step 2 已给出补充方案。