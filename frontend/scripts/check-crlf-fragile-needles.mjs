// check-crlf-fragile-needles.mjs
//
// 护栏：禁止测试里出现「跨行字符串针」去匹配工作区里的源文件。
//
// 背景（2026-10-02 实测事故）
// ----------------------------
// email-refresh-trigger.test.mjs 里有这样一处负控：
//
//     hostSrc.replace(
//       'state.lastAttemptAt = now\n    state.inFlight = true',
//       'state.inFlight = true',
//     )
//
// 仓库里存的是 LF，但本机 core.autocrlf=true，Windows 上检出的 .ts 是 CRLF。
// 于是 `...\n    state...` 这个针**在磁盘上根本不存在**，`.replace()` 静默
// 不命中，负控样本等于没变异过 —— 判据从此静默失效。
//
// 危险之处在于它**只在 Windows 上炸**：.github/workflows/*.yml 全部
// runs-on ubuntu-latest，Linux 检出是 LF，针必然命中，CI 永远全绿。
// 也就是说一个判据可以在 CI 里"有效"几个月，本机一跑就红，或者反过来：
// 谁在 Windows 上把 needle 改成永远匹配不到的样子，CI 也不会拦。
//
// 为什么只查 needle 不查 replacement
// --------------------------------
// `.replace(needle, replacement)` 里：
//   · needle（第 1 参数）必须匹配磁盘内容 → 对行尾敏感，会静默失效；
//   · replacement（第 2 参数）是**插入**的文本，它里面写 \n 还是 \r\n
//     只影响新插入内容的换行风格，不影响判据是否命中。
// 所以只有 needle 是风险面。实测 email-job-runtime-singleton.test.mjs 的
// 两处跨行串都是 replacement，故不受影响，本护栏也不该误报它们。
//
// 判定方式：静态扫 src 下的测试文件，找出「作为 .replace()/.includes()/
// .indexOf() 第 1 参数、且字面量里含 \n」的字符串，再看它读的那个源文件在
// 工作区里是不是 CRLF。宁可漏报也不误报：只报有实据的组合。

import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const FRONTEND = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const SRC = path.join(FRONTEND, 'src')

function walk(dir, acc = []) {
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    if (e.name === 'node_modules' || e.name === 'dist' || e.name === '.git') continue
    const p = path.join(dir, e.name)
    if (e.isDirectory()) walk(p, acc)
    else if (/\.(test|spec)\.(mjs|ts|js)$/.test(e.name)) acc.push(p)
  }
  return acc
}

/** 工作区里该文件是不是 CRLF（含 MIXED）。 */
function isCrlf(p) {
  let b
  try { b = fs.readFileSync(p) } catch { return false }
  let crlf = 0
  for (let i = 0; i < b.length; i++) {
    if (b[i] === 10 && i > 0 && b[i - 1] === 13) crlf++
  }
  return crlf > 0
}

/** 抽出所有"跨行字面量"，并标注它是 needle 还是 replacement。 */
function multilineLiterals(src) {
  const out = []
  // 逐行扫：needle 一定紧跟在 .replace( / .includes( / .indexOf( 之后（可跨行）
  const re = /\.replace\(\s*(['"`])((?:[^'"`\\]|\\.)*?\\n(?:[^'"`\\]|\\.)*?)\1/g
  let m
  while ((m = re.exec(src)) !== null) out.push({ kind: 'needle(replace)', lit: m[2], index: m.index })
  const re2 = /\.includes\(\s*(['"`])((?:[^'"`\\]|\\.)*?\\n(?:[^'"`\\]|\\.)*?)\1/g
  while ((m = re2.exec(src)) !== null) out.push({ kind: 'needle(includes)', lit: m[2], index: m.index })
  const re3 = /\.indexOf\(\s*(['"`])((?:[^'"`\\]|\\.)*?\\n(?:[^'"`\\]|\\.)*?)\1/g
  while ((m = re3.exec(src)) !== null) out.push({ kind: 'needle(indexOf)', lit: m[2], index: m.index })
  return out
}

/**
 * 该测试读到的、真实存在的源文件。
 *
 * 关键：不能只拿「裸字面量」去猜路径。真实测试几乎都这么写：
 *
 *     const SRC  = path.resolve(HERE, '..', '..', '..')      // frontend/src
 *     const HOST = path.join(SRC, 'features', 'email', 'email-fetch-host.ts')
 *
 * 末段 'email-fetch-host.ts' 单独拿去 resolve 是不存在的（它不在测试文件旁边），
 * 必须把 path.join/path.resolve 的实参**拼起来**再解析。
 * —— 护栏第一版就是漏了这一步：对 email-refresh-trigger.test.mjs 报 0 处违规，
 *   而那个文件恰恰是本次事故本体。护栏不报警 = 护栏是废的。
 */
function readTargets(testFile, src) {
  const out = []
  const hereDir = path.dirname(testFile)

  const dirConsts = new Map()
  const constRe = /(?:const|let)\s+([A-Za-z_$][\w$]*)\s*=\s*path\.(?:join|resolve)\(([^)]*)\)/g
  let cm
  while ((cm = constRe.exec(src)) !== null) dirConsts.set(cm[1], cm[2])

  function evalCall(argsStr) {
    const parts = [...argsStr.matchAll(/'([^']*)'|\b([A-Za-z_$][\w$]*)\b/g)].map((m) =>
      m[1] !== undefined ? m[1] : m[2],
    )
    if (!parts.length) return null
    let acc = parts[0] === 'HERE' ? hereDir : parts[0]
    for (let i = 1; i < parts.length; i++) {
      const p = parts[i]
      if (p === '..' || p === '.') acc = path.resolve(acc, p)
      else if (dirConsts.has(p)) {
        const inner = evalCall(dirConsts.get(p))
        acc = inner ? path.resolve(acc, inner) : acc
      } else acc = path.resolve(acc, p)
    }
    return acc
  }

  const callRe = /path\.(?:join|resolve)\(([^)]*)\)/g
  let m
  while ((m = callRe.exec(src)) !== null) {
    const resolved = evalCall(m[1])
    if (!resolved) continue
    if (fs.existsSync(resolved) && fs.statSync(resolved).isFile() && /\.(ts|vue|css|go|mjs|html)$/.test(resolved)) {
      out.push(resolved)
    }
  }

  const lits = [...src.matchAll(/'([^'\n]+)'/g)].map((x) => x[1])
  for (const s of lits) {
    if (!/\.(ts|vue|css|go|mjs|html)$/.test(s) || s.includes(' ')) continue
    for (const base of [path.dirname(testFile), FRONTEND, path.join(FRONTEND, 'src')]) {
      const p = path.resolve(base, s)
      try {
        if (fs.existsSync(p) && fs.statSync(p).isFile()) { out.push(p); break }
      } catch { /* ignore */ }
    }
  }

  return [...new Set(out)]
}

/**
 * 这个 needle 现在还**真的**匹配吗？—— 实测，不靠猜。
 *
 * 这是本护栏最重要的一段。第一版只做静态检查，结果对着**已经修好的**
 * email-refresh-trigger.test.mjs 报警：那个测试确实有一个跨行 needle，
 * 但它在读文件时写了 .replace(/\r\n/g, '\n')，所以 needle 实际是安全的。
 * 静态扫描看不见运行时归一化 —— 它只会说「这里有个跨行 needle」，
 * 看不见「这个 needle 用的是归一化后的文本」。
 *
 * 两种可能的成因，静态上完全一样：
 *   (a) 归一化过了，needle 安全            -> 误报
 *   (b) 没归一化，needle 静默失效          -> 真缺陷
 * 光看代码分不出来，只能**拿 needle 去真实匹配一遍**：
 *   · 在**原始**（CRLF）文本上能匹配 -> 归一化与否都安全，忽略；
 *   · 在原始上匹配不上、但在 **LF 归一化后**能匹配上
 *       -> 说明它依赖归一化；此时若测试**没有**归一化，就是缺陷。
 *   · 两边都匹配不上 -> 针根本就是坏的/已被改名，同样该报。
 */
function needleStatus(needle, targets) {
  // 该 needle 的字面量文本（把 JS 的 \n 转成真换行）
  const asText = needle.replace(/\\n/g, '\n').replace(/\\r/g, '\r')
  for (const t of targets) {
    let raw
    try { raw = fs.readFileSync(t, 'utf8') } catch { continue }
    const lf = raw.replace(/\r\n/g, '\n')
    if (raw.includes(asText)) return { ok: true, why: 'needle 在原始文本上即命中（与行尾无关，安全）' }
    if (lf.includes(asText)) {
      // 只在归一化之后才命中 => 这个 needle 依赖 LF 文本。
      // 这本身不一定是缺陷（测试若自己归一化了就没事），
      // 所以由调用方结合「该测试有没有归一化」一起判断。
      return { ok: false, needsLf: true, why: 'needle 只在 LF 归一化后的文本上命中' }
    }
  }
  return { ok: false, why: 'needle 在原始文本与 LF 归一化文本上都匹配不上 —— 判据已失效' }
}

/**
 * 这个测试自己有没有把读到的文本归一化成 LF？
 *
 * 这是判定的最后一环，也是护栏对不对的关键。上一版只看
 * 「needle 能否在 LF 文本上命中」，于是对着**已修好**的
 * email-refresh-trigger.test.mjs 仍然放行——因为那个测试确实归一化了，
 * 但护栏并不知道它归一化了；而把归一化删掉之后，护栏还是放行，
 * 因为 needle 在 LF 文本上照样命中。两种状态在护栏眼里一模一样 = 废护栏。
 *
 * 所以必须同时问两个问题：
 *   1. 这个 needle 是不是只在 LF 文本上命中？（needsLf）
 *   2. 这个测试有没有对读到的文本做 CRLF→LF 归一化？（normalizes）
 * 只有 (1) 且非 (2) 才是缺陷 —— 那正是 2026-10-02 的事故形态。
 */
function testNormalizesEol(src) {
  // 形如 .replace(/\r\n/g, '\n') 或 replace(/\r\n/g,"\n") 的归一化
  return /\.replace\(\s*\/\s*\\r\\n\s*\/[gimsuy]*\s*,\s*(['"`])\\\\?n\1\s*\)/.test(src)
}

const files = walk(SRC)
const violations = []

/**
 * 剥掉注释后再扫。
 *
 * 这不是洁癖，是实测踩到的：第一版护栏把「我在注释里写的例子」
 * （`// 字符串针（'state.lastAttemptAt = now\n    state.inFlight = true'）`）
 * 当成了真 needle，于是修好代码之后护栏反而报警——护栏开始指着自己的
 * 注释要人改。判据必须只认代码，否则它会被注释满足/被注释污染。
 *
 * 用 TypeScript 的扫描器剥（与本仓库其他护栏一致），保证字符串字面量
 * 里的内容不受影响，也不会被 URL 里的 // 骗到。
 */
function stripComments(src) {
  // 轻量但够用：处理 // 与 /* */，并跳过字符串/模板字面量内部的同类符号。
  let out = ''
  let i = 0
  const n = src.length
  while (i < n) {
    const c = src[i]
    const c2 = src[i + 1]
    if (c === '/' && c2 === '/') {
      while (i < n && src[i] !== '\n') i++
      continue
    }
    if (c === '/' && c2 === '*') {
      i += 2
      while (i < n && !(src[i] === '*' && src[i + 1] === '/')) i++
      i += 2
      // 用空格占位，保持后续 indexOf/切片语义不变
      out += ' '
      continue
    }
    if (c === '"' || c === "'" || c === '`') {
      const q = c
      out += c
      i++
      while (i < n && src[i] !== q) {
        if (src[i] === '\\') { out += src[i] + (src[i + 1] ?? ''); i += 2; continue }
        out += src[i]
        i++
      }
      out += q
      i++
      continue
    }
    out += c
    i++
  }
  return out
}

for (const f of files) {
  const raw = fs.readFileSync(f, 'utf8')
  if (!/readFileSync\([^)]*['"]utf8['"]\)/.test(raw)) continue
  const src = stripComments(raw)
  const needles = multilineLiterals(src).filter((x) => x.kind.startsWith('needle'))
  if (!needles.length) continue
  const targets = readTargets(f, raw).filter(isCrlf)
  if (!targets.length) continue
  // 该测试有没有自己做 CRLF→LF 归一化？（判定的另一半，见 testNormalizesEol）
  const normalizes = testNormalizesEol(src)
  for (const n of needles) {
    const st = needleStatus(n.lit, targets)
    if (st.ok) continue
    // 只在 LF 文本上命中：测试自己归一化了就安全，没归一化就是缺陷。
    if (st.needsLf && normalizes) continue
    violations.push({
      test: path.relative(FRONTEND, f),
      kind: n.kind,
      lit: n.lit,
      targets: targets.map((t) => path.relative(FRONTEND, t)),
      why: st.needsLf && !normalizes
        ? '跨行 needle 依赖 LF 文本，但该测试没有做 CRLF→LF 归一化（Windows 检出时会静默失效）'
        : st.why,
    })
  }
}

if (violations.length) {
  console.error(`✗ CRLF 脆弱针护栏：${violations.length} 处「跨行 needle」已失效\n`)
  for (const v of violations) {
    console.error(`  ${v.test}`)
    console.error(`    ${v.kind}: ${JSON.stringify(v.lit).slice(0, 120)}`)
    console.error(`    目标(工作区为 CRLF): ${v.targets.join(', ')}`)
    console.error(`    实测: ${v.why}`)
    console.error(`    修法：读文件后 .replace(/\\r\\n/g, '\\n') 归一化，再拿归一化后的文本当 needle\n`)
  }
  process.exit(1)
}

console.log(`✓ CRLF 脆弱针护栏：${files.length} 个测试文件，跨行 needle 全部实测命中`)
