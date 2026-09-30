/**
 * audit-vue-mustache.mjs — 扫描 Vue 模板里**漏了 `{{ }}` 插值**的裸表达式。
 *
 * ## 背景：BUG-T（2026-09-30）
 *
 * `StatsView.vue` 模板里写的是
 *
 *     <p class="retention-hint">
 *       t('flashcards.stats.retentionHint', { again: ..., hard: ... })
 *     </p>
 *
 * 少了 `{{ }}` 包裹，浏览器把这段 JS **当正文原样显示**给用户。之前没发现，
 * 因为 BUG-S 的 vue-i18n 崩溃让整页白屏，**把它盖住了** ——
 * 典型的「修好一层露出下一层」。
 *
 * ## 判据
 *
 * 在 `<template>` 区域内，找「以 `t(` / `v-bind` 之类函数调用形态开头、但同行及
 * 后续若干行内没有 `{{`」的文本节点。宁可多报也不漏报，人工复核成本远低于
 * 让用户看到源码。
 *
 * 具体做法：逐文件取 template 块，剥掉注释与所有已有的 `{{ ... }}` 插值，
 * 剩下的文本里再找形如 `函数名(` 且不在标签属性里的片段。
 *
 * 只做**候选提示**，不做判决 —— 模板里合法的裸文本（比如展示代码示例的
 * `<code>` 块）会被报出来，需要人判断。
 *
 * 用法：node scripts/audit-vue-mustache.mjs
 */
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..')
// 允许用 env 覆盖扫描根：verify-audit-detects.mjs 的注入实验要在临时目录里跑，
// 不能为了验证脚本有效性去改真实源码。
const SRC = process.env.MUSTACHE_AUDIT_SRC || join(ROOT, 'frontend/src')
const SKIP = new Set(['node_modules', 'dist', '.vite', '__tests__'])

function collect(dir, out = []) {
  for (const n of readdirSync(dir)) {
    if (SKIP.has(n)) continue
    const p = join(dir, n)
    if (statSync(p).isDirectory()) collect(p, out)
    else if (n.endsWith('.vue')) out.push(p)
  }
  return out
}

/** 取 <template> ... </template> 的正文（第一个顶层 template）。 */
function templateOf(src) {
  const start = src.indexOf('<template>')
  if (start < 0) return null
  // 找到配对的 </template>：从 start 往后找，跳过嵌套（template 内不会有 template 标签）
  const end = src.indexOf('</template>', start)
  return end < 0 ? null : src.slice(start + 10, end)
}

/** 判断某个字符偏移是否落在标签内部（<...>）。标签已在上面整体剥掉，这里恒 false。 */
function insideTag() { return false }

const files = collect(SRC)
const findings = []

for (const file of files) {
  const src = readFileSync(file, 'utf8')
  const tpl = templateOf(src)
  if (!tpl) continue

  // 1) 先剥掉 HTML 注释
  let s = tpl.replace(/<!--[\s\S]*?-->/g, (m) => ' '.repeat(m.length))
  // 2) 再剥掉所有已存在的 {{ ... }} 插值
  s = s.replace(/\{\{[\s\S]*?\}\}/g, (m) => ' '.repeat(m.length))
  // 3) 剥掉 <script> / <style> 块（若 template 内嵌）
  s = s.replace(/<script[\s\S]*?<\/script>/g, (m) => ' '.repeat(m.length))
  // 3.5) **剥掉所有标签本身**。
  //
  //     这一步是误报的根源：`:aria-label="t('settingsMenu.title')"` 是完全合法的
  //     属性绑定，但文本节点扫描看不见标签边界，会把它当成"裸表达式"。
  //     第一版试图用 lastIndexOf('<') / indexOf('>') 判断"在不在标签内"，
  //     逻辑写反了（返回 false 时才该跳过），结果 365 条候选里绝大多数是误报。
  //
  //     正则必须**正确处理带引号的属性值**：`<[^>]*>` 会在
  //     `@click="emit('a', v) > b"` 这种值里提前收尾，把后面的内容漏给文本扫描。
  //     所以属性值部分要单独匹配（双引号或单引号），再以 `>` 收尾。
  s = s.replace(/<(?:[^>"']|"[^"]*"|'[^']*')*>/g, (m) => ' '.repeat(m.length))

  // 4) 找裸的函数调用形态：标识符 + (，且不在标签内
  const re = /\b([A-Za-z_$][\w$.]*)\s*\(/g
  let m
  const seen = new Set()
  while ((m = re.exec(s)) !== null) {
    const name = m[1]
    // 排除属性绑定、组件标签、常见 HTML 片段
    if (/^(v|@|:|#|slot|template|component|transition|keep-alive|router-link|RouterLink)$/i.test(name)) continue
    if (insideTag(s, m.index)) continue
    const line = s.slice(0, m.index).split('\n').length
    const key = `${line}:${name}`
    if (seen.has(key)) continue
    seen.add(key)
    findings.push({ file: relative(ROOT, file), line, name, snippet: s.slice(m.index, m.index + 70).replace(/\s+/g, ' ') })
  }
}

console.log(`扫描 ${files.length} 个 .vue 文件\n`)
if (findings.length === 0) {
  console.log('OK: 没有发现疑似漏 {{ }} 的裸表达式。')
} else {
  console.log(`候选 ${findings.length} 处（需人工复核，可能是在展示代码示例的合法用法）：\n`)
  for (const f of findings) {
    console.log(`  ${f.file}:${f.line}  ${f.name}(...)`)
    console.log(`      ${f.snippet}`)
  }
  process.exitCode = 1
}
