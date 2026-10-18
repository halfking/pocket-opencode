// v-html-outlet-audit.test.ts — §851：`v-html` 绑定的内容**必须有净化**。
//
// 缺陷背景（RssItemDetail.vue:124）：
//   <div v-if="item.content" class="content" v-html="item.content"></div>
//   ⛔ 取证链：
//     ① 服务端 `backend/internal/rss/store_entities.go` 把 `content` **原样**入库，
//        全仓 grep 不到任何针对 rss 的 sanitize；
//     ② `frontend/index.html` **没有 CSP**（实测 grep 无命中），
//        所以没有任何东西拦 `onerror=` 这类内联事件；
//     ③ 订阅源作者对正文**完全可控** —— 一条订阅里的
//        `<img src=x onerror="...">` 会在设备 WebView 里执行，
//        而这个 WebView 带着 Capacitor bridge（读得到 localStorage 里的登录态）。
//
// ⭐ 本仓**已经有**正确的做法：`utils/markdown.ts::sanitizeHtml`
//   （配置明写 FORBID_TAGS: ['style','script']、ALLOWED_ATTR 不含 style）。
//   聊天 / 笔记 / 会议摘要 / Agent 提示词预览全都走它 ——
//   **RSS 是 10 处 `v-html` 里唯一没走的那个。**
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join, relative } from 'node:path'

const HERE = dirname(fileURLToPath(import.meta.url))
const SRC = join(HERE, '..')
const read = (rel: string) => readFileSync(join(SRC, rel), 'utf8')
const relOf = (p: string) => relative(SRC, p).split('\\').join('/')

function collectVue(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(join(SRC, dir))) {
    if (name === '__tests__' || name === 'node_modules') continue
    const full = join(SRC, dir, name)
    let st
    try { st = statSync(full) } catch (err: any) {
      if (err?.code !== 'ENOENT') throw err
      continue
    }
    if (st.isDirectory()) collectVue(dir ? relative(SRC, full) : '.', out)
    else if (name.endsWith('.vue')) out.push(full)
  }
  return out
}
const VUE_FILES = collectVue('.')

const templateOf = (src: string): string => {
  const scriptAt = src.search(/<script\b/)
  const head = scriptAt === -1 ? src : src.slice(0, scriptAt)
  if (/<template[\s>]/.test(head)) return head
  const t0 = src.indexOf('<template')
  const t1 = src.lastIndexOf('</template>')
  return t0 >= 0 && t1 > t0 ? src.slice(t0, t1 + '</template>'.length) : head
}
/** 模板区 + 剥 HTML 注释（注释里的 v-html 不算）。 */
const tplCode = (src: string): string => templateOf(src).replace(/<!--[\s\S]*?-->/g, '')

interface Binding { file: string; expr: string }
function bindings(): Binding[] {
  const out: Binding[] = []
  for (const f of VUE_FILES) {
    for (const m of tplCode(readFileSync(f, 'utf8')).matchAll(/v-html\s*=\s*"([^"]+)"/g)) {
      out.push({ file: relOf(f), expr: m[1].trim() })
    }
  }
  return out
}

describe('§851 · `v-html` 必须经过净化', () => {
  it('★ 尺子非空转：全仓 .vue 至少 100 个', () => {
    assert.ok(VUE_FILES.length > 100, `只扫到 ${VUE_FILES.length} 个 .vue`)
  })

  it('★ 扫到 10 处绑定（基线，口径变化时这里会先红）', () => {
    // 棘轮：**只钉住数量上界**，新增 v-html 会被这条抓住并逼你判断。
    const n = bindings().length
    assert.ok(n > 0 && n <= 10, `扫到 ${n} 处 v-html，超出本节记录的基线 10`)
  })

  it('★★★ RssItemDetail 的正文必须走 sanitizeHtml', () => {
    const src = read('features/rss/RssItemDetail.vue')
    const tpl = tplCode(src)
    assert.match(tpl, /v-html="safeContent"/,
      'RSS 正文又变回直接渲染原始 item.content ⇒ 订阅源作者可在设备上执行脚本')
    assert.match(src, /const safeContent = computed\(\(\) => sanitizeHtml\(/,
      'safeContent 必须由共享净化器产出，不能自己写一套')
  })

  it('★★★ 全仓每处 v-html 的表达式都必须落在「已净化」清单里', () => {
    // ⚠ 第一版按**文件**放行，判据太粗：把表达式从 `safeContent` 改回
    //   `item.content` 时它一声不响 —— 文件还在清单里，判据却已经不成立了。
    //   ⇒ 清单必须钉**表达式形状**，而不是文件名（同「判据的识别词是契约的一部分」）。
    const ALLOWED_EXPR: Record<string, RegExp> = {
      'features/rss/RssItemDetail.vue': /^safeContent$/,              // ← sanitizeHtml
      'features/agents/AgentDetailView.vue': /^promptPreview$/,      // ← renderMarkdown
      'features/ai-chat/AIChatView.vue': /^rendered\(/,             // error 分支还显式 escapeHtml
      'features/email/EmailSummaryView.vue': /^renderedMarkdown$/,
      'features/notes/NoteDetailView.vue': /^renderedMarkdown$/,
      'features/sessions/RoundTimeline.vue': /^renderedHtml\(/,     // ← renderMarkdown
      'features/email/EmailDetailView.vue': /^(htmlBody|quotedHtml|signatureHtml)$/, // ← sanitizeEmailHtml
    }
    const bad = bindings()
      .filter((b) => !(ALLOWED_EXPR[b.file]?.test(b.expr)))
      .map((b) => `${b.file}: ${b.expr}`)
    assert.deepEqual(bad, [],
      `这些 v-html 表达式不在「已净化」清单里：\n  ${bad.join('\n  ')}\n` +
      '⇒ 要么它的值确实来自净化器（把**表达式**登记进 ALLOWED_EXPR 并写清链路），要么就是漏净化')
  })

  it('★★ 共享净化器必须真的禁掉 style/script（否则 RSS 净化等于半截）', () => {
    const md = read('utils/markdown.ts')
    assert.match(md, /FORBID_TAGS:\s*\[[^\]]*'style'[^\]]*'script'/,
      '共享净化器不再 FORBID style/script ⇒ 注入面又开了')
    const attrBlock = /ALLOWED_ATTR:\s*\[([^\]]*)\]/.exec(md)
    assert.ok(attrBlock, '找不到 ALLOWED_ATTR')
    assert.equal(/\bstyle\b/.test(attrBlock[1]), false,
      'ALLOWED_ATTR 里出现了 style ⇒ CSS 注入面又开了')
  })

  it('★ 阴性读数固定：另外 6 处 v-html 的净化链路仍在', () => {
    // 「有出口」不等于「净化没被改掉」——这三条是防回归的抽查。
    assert.match(read('features/rss/RssItemDetail.vue'), /import \{ sanitizeHtml \}/)
    assert.match(read('features/email/EmailDetailView.vue'), /sanitizeEmailHtml\(prepared\)/)
    assert.match(read('utils/markdown.ts'), /DOMPurify\.sanitize\(rawHtml, PURIFY_CONFIG\)/)
  })
})
