// style-scan-utils.mjs
//
// 样式扫描器共用的底层工具。**刻意不叫 .test.mjs**——
// 工具模块一旦被 .test.mjs 引用，顶层 describe/it 会跟着注册进当前进程，
// 于是「A 护栏 import B 护栏」会把 B 的测试也跑一遍，两边的生命周期互相取消
// （实测表现为满屏 "test did not finish before its parent and was cancelled"，
//  看起来像判据坏了，其���是模块副作用）。
//
// 单独成文件是「护栏之间不互相 import」的落地方式。

import * as fs from 'node:fs'
import * as path from 'node:path'

/**
 * 把注释换成等长空格（长度必须不变——判据要按偏移回原文取上下文）。
 * 不剥注释的话，「在注释里解释旧写法」会被误判成违规，久了就没人敢写注释。
 *
 * ⚠️ 2026-10-03 修一处**永远绿的盲区**。
 * 第一版没有字符串感知，于是 .vue 模板里 accept 属性值中的 video 加星号
 * 被当成块注释起点，一路吞到下一个注释收尾——NoteEditView.vue 里 7658 个字符的
 * 真实 CSS 被整段抹成空格，其中正好有 2 处 font-size: 12px 是字号护栏
 * 该抓的违规。**方向是 fail-open：判据扫不到，于是测试一直绿。**
 *
 * 修法不是把块注释判据调松，而是让状态机认识引号：
 * 只有当引号紧跟在 = : ( 之后（HTML 属性值 / CSS 字符串 / url()）才进入字符串态。
 * 这么判是因为 Vue 模板正文里会出现英文撇号（don't），无差别地把单引号
 * 当字符串起点会反过来吞掉更多内容——那还是同一个 fail-open 方向。
 */
export function blankComments(src) {
  const out = src.split('')
  const isSpace = (c) => c === ' ' || c === '\t' || c === '\r' || c === '\n'
  /** 前一个非空白字符——用来判断引号是不是属性值/字符串的起点。 */
  const prevNonSpace = (from) => {
    let k = from - 1
    while (k >= 0 && isSpace(src[k])) k--
    return k >= 0 ? src[k] : ''
  }
  let i = 0
  while (i < src.length) {
    if (src.startsWith('/*', i)) {
      const end = src.indexOf('*/', i + 2)
      const stop = end < 0 ? src.length : end + 2
      for (let k = i; k < stop; k++) if (out[k] !== '\n') out[k] = ' '
      i = stop
    } else if (src.startsWith('//', i)) {
      let end = src.indexOf('\n', i)
      if (end < 0) end = src.length
      for (let k = i; k < end; k++) if (out[k] !== '\n') out[k] = ' '
      i = end
    } else {
      const c = src[i]
      if (c === '"' || c === "'" || c === '`') {
        const p = prevNonSpace(i)
        if (p === '=' || p === ':' || p === '(') {
          // 属性值 / CSS 字符串：跳到配对的收尾引号为止，内部一律不动。
          const close = src.indexOf(c, i + 1)
          i = close < 0 ? src.length : close + 1
          continue
        }
      }
      i++
    }
  }
  return out.join('')
}

/** 递归收集 .vue / .css 样式文件（跳过 node_modules 与点目录）。 */
export function walkStyleFiles(dir, acc = []) {
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, e.name)
    if (e.isDirectory()) {
      if (e.name === 'node_modules' || e.name.startsWith('.')) continue
      walkStyleFiles(p, acc)
    } else if (e.name.endsWith('.vue') || e.name.endsWith('.css')) acc.push(p)
  }
  return acc
}

/**
 * 只取出 .vue 里的 `<style>` 块（.css 返回整份），并保留**起始行号偏移**。
 *
 * ⚠️ 为什么必须有这个函数（2026-10-06 实吃）。
 * 「按 `}` 平衡切规则块」的扫描器直接吃**整个 .vue 文件**时，`<script>` 里的
 * TypeScript 花括号会把它彻底带跑偏：实测 `AppLayout.vue` 本该解析出 30+ 条
 * 样式规则，结果头几条的选择器是
 *     "shellRuntime.setScope(" 、";(window as unknown as" 、"nextTick(() =>"
 * —— 那是 **JS 代码被当成了 CSS 选择器**。
 *
 * 失败方向是 **fail-open**：真实的 `.top-bar { … }` 一条都没进规则表，
 * 于是「顶栏必须声明 flex-shrink:0」这类判据会**永远绿**（扫不到东西 ⇒
 * 零违规），而看上去一切正常。
 * ⚠️ 同一族的更隐蔽后果：`bottom-chrome-gate.test.mjs` 用的就是这个扫描器，
 * 它的命中集合里混着从 `<script>` 里捞出来的**伪规则**。凡是用「ALLOWLIST 里
 * 的行号」当 key 的判据，都必须先过这个函数，否则 key 指向的可能根本不是 CSS。
 *
 * @returns {{text:string, lineOffset:number}[]} `lineOffset` 是该块首行在原文件
 *   中的行号（1-based）；判据要按行号登记 ALLOWLIST，丢了偏移就全盘失效。
 */
export function extractStyleBlocks(src, file) {
  if (!/\.(vue|html)$/i.test(file)) return [{ text: src, lineOffset: 0 }]
  const lines = src.split('\n')
  const blocks = []
  let open = -1
  let body = []
  let openLine = 0
  for (let i = 0; i < lines.length; i += 1) {
    const line = lines[i]
    if (open < 0) {
      // ⚠️ 必须认属性：`<style scoped>` / `<style lang="scss">` 都算开始。
      if (/^\s*<style(\s[^>]*)?>\s*$/.test(line)) {
        open = 1
        openLine = i + 2 // 块内首行在原文件中的行号
        body = []
      }
      continue
    }
    if (/^\s*<\/style>\s*$/.test(line)) {
      blocks.push({ text: body.join('\n'), lineOffset: openLine })
      open = -1
      body = []
      continue
    }
    body.push(line)
  }
  // 未闭合的 <style>（文件被截断 / 语法坏）也照样交出去 —— 判据扫不到东西时
  // 必须由「采集结果非空」那条自证去红，而不是在这里静默返回空数组。
  if (open >= 0 && body.length) blocks.push({ text: body.join('\n'), lineOffset: openLine })
  return blocks
}

/** 声明的正则：属性名必须由 [\s;{] 起头，避免 border-top / border-bottom 命中。 */
const DECL_RE = /(?:^|[\s;{])([a-z-]+)\s*:\s*([^;{}]+)/g
const countBraces = (s) => (s.match(/\{/g) || []).length - (s.match(/\}/g) || []).length

/**
 * 把一份 .vue / .css 解析成 `{ file, selector, decls }[]`（含 `line`）。
 *
 * 与既有 `bottom-chrome-gate.test.mjs` 内嵌那份扫描器的实测差异：
 * 它吃**整个 .vue 文件**（不切 `<style>` 块），本函数只吃 `<style>`。
 * 实测 `position:fixed + bottom` 的命中集合：它 13 条 / 本函数 15 条，
 * 漏掉的是 `MeetingDetailView.vue:316 .speakers-btn` 与
 * `VaultEntryView.vue:587 .toast`（两条恰好都合规，所以**结论未变**，
 * 但覆盖面确实缺了 2 条）。
 *
 * ⚠️ 这里记一条我自己踩过的坑，免得下次再犯：**不要凭「复刻版扫描器跑出来的
 * 结果」去指控既有门禁。** 我一度照抄那份代码时漏掉了它的一个分支
 * （`depth===0 && !line.includes('{')` 与 `depth===0` 是两个独立分支，
 * 后者**不清 buf**，正是它让 `{` 留在 buf 里、规则才没被
 * `open !== -1` 守卫丢掉），于是在自己的复刻版里看到「顶层规则全丢、
 * 0 条命中」，差点把它当成既有门禁的 fail-open 报上去。
 * ⇒ 拿既有门禁的真实代码跑，别拿复刻版。
 *
 * 三个必须做对的点（各自都单独把结果带偏过）：
 *  ① **只吃 `<style>` 块**（extractStyleBlocks）。否则 `<script>` 里的
 *     TypeScript 会进规则表，产生 "shellRuntime.setScope(" 这种伪选择器。
 *  ② **先涂注释再切块**。`countBraces` 按行数 `{`/`}`；注释里一个不成对的
 *     花括号就会让深度卡在 >0 直到文件末尾，该文件只解析出最后几块。
 *  ③ **选择器行必须留在 buf 里**。`{` 就在那一行；丢了它
 *     `body.indexOf('{')` 恒为 -1，`open !== -1` 守卫会把整条规则丢掉。
 *     跨行选择器用 pending 续上，否则 `.a,\n.b {` 会被解析成 `.b`。
 *
 * `line` 是**选择器首行**的 1-based 文件行号 —— ALLOWLIST 之类按行号登记的
 * 判据依赖它（已用 `components/BottomNav.vue:125` 与既有 ALLOWLIST 的 key
 * 独立对上：两边算出同一个 125）。
 */
export function parseStyleRules(raw, file) {
  const rules = []
  for (const block of extractStyleBlocks(raw, file)) {
    // blankComments 保持长度不变，因此行号与偏移都还对得上。
    const lines = blankComments(block.text).split('\n')
    let depth = 0
    let buf = []
    let pending = []
    let start = 0
    for (let i = 0; i < lines.length; i += 1) {
      const line = lines[i]
      if (depth === 0) {
        if (line.includes('{')) {
          // ⚠️ 这一行必须**进 buf**：`{` 就在它身上，丢了它整条规则会被
          //    下面的 `open !== -1` 守卫丢掉（见文件头注释）。
          buf = pending.concat(line)
          // lineOffset 是 lines[0] 的 1-based 文件行号，故 lines[k] 的行号
          // 就是 lineOffset + k；选择器首行是 lines[i - pending.length]。
          start = block.lineOffset + i - pending.length
          pending = []
          depth += countBraces(line)
        } else if (line.trim()) {
          pending.push(line)
        }
        continue
      }
      buf.push(line)
      depth += countBraces(line)
      if (depth > 0) continue
      const body = buf.join('\n')
      const open = body.indexOf('{')
      const close = body.lastIndexOf('}')
      if (open !== -1 && close > open) {
        const decls = {}
        for (const m of body.slice(open + 1, close).matchAll(DECL_RE)) {
          if (!(m[1] in decls)) decls[m[1]] = m[2].trim()
        }
        rules.push({ file, line: start, selector: body.slice(0, open).trim(), decls })
      }
      depth = 0
      buf = []
    }
  }
  return rules
}

/** 递归扫全仓样式文件并解析成规则表。file 用相对 SRC 的正斜杠路径。 */
export function collectStyleRules(srcDir) {
  const acc = []
  for (const f of walkStyleFiles(srcDir)) {
    const rel = f.slice(srcDir.length + 1).split(path.sep).join('/')
    acc.push(...parseStyleRules(fs.readFileSync(f, 'utf8'), rel))
  }
  return acc
}
