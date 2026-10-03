/**
 * audit-composable-imports — 找出「用了 useXxx() 但文件里没有对应 import」的编译级错误。
 * 这类错误在 Android 真机上表现为整个组件白屏/渲染崩溃，但很容易在只跑 CDP 点查时漏掉。
 */
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, extname } from 'node:path'

const ROOT = new URL('../src/', import.meta.url).pathname.replace(/^\/([A-Za-z]:)/, '$1')
const COMPOSABLES = ['useConfirm', 'useToast']

/** 剥掉 HTML / 行 / 块注释，避免把文档里的 useConfirm() 当成真实调用（零噪声是工具可用性的前提） */
const stripComments = (s) =>
  s
    .replace(/<!--[\s\S]*?-->/g, ' ')
    .replace(/\/\*[\s\S]*?\*\//g, ' ')
    .replace(/(^|[^:])\/\/.*$/gm, '$1')

const walk = (dir, out = []) => {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name)
    if (statSync(p).isDirectory()) walk(p, out)
    else if (['.vue', '.ts'].includes(extname(p))) out.push(p)
  }
  return out
}

let bad = 0
let checked = 0
for (const file of walk(ROOT)) {
  const raw = readFileSync(file, 'utf8')
  if (extname(file) === '.vue' && !raw.includes('<script')) continue
  checked++
  const src = stripComments(raw)
  for (const c of COMPOSABLES) {
    // 负向后顾排除定义处 `export function useConfirm() {`
    const used = new RegExp(`(?<!function )\\b${c}\\s*\\(`).test(src)
    if (!used) continue
    const imported = new RegExp(`import\\s*\\{[^}]*\\b${c}\\b[^}]*\\}\\s*from`).test(src)
    if (!imported) {
      console.log(`MISSING-IMPORT  ${c}  <-  ${file.replace(ROOT, '')}`)
      bad++
    }
  }
}
console.log(`\nchecked ${checked} files, missing imports: ${bad}`)
process.exit(bad ? 1 : 0)
