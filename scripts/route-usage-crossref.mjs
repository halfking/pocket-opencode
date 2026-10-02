#!/usr/bin/env node
// route-usage-crossref.mjs —— 把后端注册的路由与前端**实际调用点**对账。
//
// 与 route-coverage-sweep.mjs 互补：
//   route-coverage-sweep 问「注册了、打过去会怎样」（可用性）
//   本脚本问「注册了、**产品代码里有人调吗**」（接线）
//
// 为什么值得单独查：本项目已经因为"能力写好了却没人接线"吃过亏——
// frontend/scripts/check-dead-api.mjs 的注释记着 /api/assets/sync
// 「后端已注册、store 有 listDirty、api 客户端也写好了，但 assetsApi.sync()
// 除了定义处没有任何调用方」。那类问题编译通过、类型通过、gates 全绿、
// 运行时也完全正常，**只是那件事从来没发生过**。
//
// ---------------------------------------------------------------------------
// v1 的判据是坏的，这里记下它坏在哪（别再退回那个写法）
// ---------------------------------------------------------------------------
// v1 用 `/\b(?:http|fetch)\s*(?:<[^>]*>)?\s*\(\s*[`'"]([^`'"]+)[`'"]/g`
// 抽调用点。四个各自独立的失效：
//   1. 模板字面量里带嵌套反引号就被截断。真实代码 `email.ts:374` 写的是
//      http(`/api/emails${q ? `?${q}` : ''}`) —— `[^`'" ]+` 吃到第二个反引号
//      就停，抽出 `/api/emails${q ? ` ，与路由 `/api/emails` 比不相等，
//      于是**明明天天在调的列表接口被报成"没人调"**。v1 因此输出 59 条假阳性。
//   2. 嵌套泛型 `http<Record<string, unknown>>(...)` 匹配不到（`[^<>]*`
//      顶不住外层 `<>` 里再套 `<string, unknown>`），meeting/stt 调用点整体消失。
//   3. 跨文件常量 `const base = '/api/marketplace'` 后 `` `${base}/packages` ``，
//      调用点里看不到 base 的值。
//   4. 注释与文档里的路径会被当成调用点——这条方向是**安全**的（多算"有人调"），
//      但会让"没人调"这个结论显得比实际更可信，同样要治。
//
// ---------------------------------------------------------------------------
// v2 的做法
// ---------------------------------------------------------------------------
// 1) stripComments：字符级扫描，识别 // 与 /* */ 与正则字面量，注释抹成空格。
//    这是为了第 4 条：注释里的路径不计入。
// 2) 真正的字符串/模板解析：模板按 ${ } 深度配对，嵌套模板与字符串都跳过。
//    插值分三类——
//      简单标识符 / 成员访问 / encodeURIComponent(x)  → 段通配 [^/]*
//      三元表达式（`${q ? '?x' : ''}` 这种查询串）        → 可选查询串
//      其它（函数结果整体）                                → 任意 .*
//    这样 `/api/emails${q ? '?${q}' : ''}` 抽出 `/api/emails` + 可选查询串，
//    精确匹配得上；`/api/emails/${id}` 抽成 `/api/emails/` + 段通配。
// 3) 常量解析：收集 const/let/var 的字符串/模板/别名初值，本地优先于全局，
//    同名不同值判为冲突并排除（冲突名单会打印，不静默）。
// 4) 分层判定：把"产品调用点"与"仅测试调用点"分开，因为**只有测试调用的路由
//    正是"能力写好了没人接线"的典型形态**。
//
// 判据自检 + 盲版对照在 runSelfChecks() 里，任一自检不过就 exit(3)，
// 因为判据不可信时输出比没有输出更糟。
//
// Run: node scripts/route-usage-crossref.mjs [--csv out.csv] [--json]
import { readFileSync, writeFileSync, readdirSync, statSync } from 'node:fs'
import { join, dirname, relative, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..')
const SRC = join(ROOT, 'frontend', 'src')
const GO_FILES = ['server.go', 'server_rss.go'].map((f) => join(ROOT, 'backend', 'internal', 'server', f))

const argv = process.argv.slice(2)
const CSV = argv.includes('--csv') ? argv[argv.indexOf('--csv') + 1] : ''
const AS_JSON = argv.includes('--json')

// 段通配 / 任意通配 / 查询串：解析期用哨兵，匹配期转成正则。
const T_SEG = 0 // 未知的一段路径（id 之类）
const T_ANY = 1 // 未知的一整块（函数返回值、运行期拼出来的）
const T_QRY = 2 // 可选查询串

// ---------------------------------------------------------------------------
// 1) 注释抹除
// ---------------------------------------------------------------------------

const REGEX_PRECEDERS = new Set(['(', ',', '=', ':', '[', '!', '&', '|', '?', ';', '{', '}', '}', ')', '\n'])
const REGEX_KEYWORDS = new Set(['return', 'typeof', 'case', 'in', 'of', 'new', 'delete', 'void', 'do', 'else', 'yield', 'await', 'instanceof'])

/** 从 i 处（src[i] 是引号）读一个字符串/模板，返回 {parts, end}。 */
function readStringish(src, i) {
  const q = src[i]
  if (q !== '`') {
    let j = i + 1
    let out = ''
    while (j < src.length) {
      const c = src[j]
      if (c === '\\') { out += src[j + 1] ?? ''; j += 2; continue }
      if (c === q) { j++; break }
      if (c === '\n') break // 未闭合，放弃
      out += c
      j++
    }
    return { parts: [{ lit: out }], end: j }
  }
  // 模板：`${` … `}` 深度配对，内部可能再嵌模板
  const parts = []
  let lit = ''
  let j = i + 1
  while (j < src.length) {
    const c = src[j]
    if (c === '\\') { lit += src[j + 1] ?? ''; j += 2; continue }
    if (c === '`') { j++; break }
    if (c === '$' && src[j + 1] === '{') {
      if (lit) { parts.push({ lit }); lit = '' }
      const e = readBraced(src, j + 2)
      parts.push({ expr: src.slice(j + 2, e.inner) })
      j = e.end
      continue
    }
    if (c === '\n') { /* 模板允许换行 */ }
    lit += c
    j++
  }
  if (lit) parts.push({ lit })
  return { parts, end: j }
}

/** 从 i 处开始读一个 { … }（调用方已消费 `${`），返回 {inner, end}。inner 是 `}` 的下标。 */
function readBraced(src, i) {
  let depth = 1
  let j = i
  while (j < src.length) {
    const c = src[j]
    if (c === '\'' || c === '"' || c === '`') { j = readStringish(src, j).end; continue }
    if (c === '/' && src[j + 1] === '/') { while (j < src.length && src[j] !== '\n') j++; continue }
    if (c === '/' && src[j + 1] === '*') { j += 2; while (j < src.length && !(src[j] === '*' && src[j + 1] === '/')) j++; j += 2; continue }
    if (c === '{') { depth++; j++; continue }
    if (c === '}') { depth--; if (depth === 0) return { inner: j, end: j + 1 }; j++; continue }
    j++
  }
  return { inner: src.length, end: src.length }
}

function isRegexStart(src, i) {
  // 往前找最近的非空白字符
  let j = i - 1
  while (j >= 0 && /\s/.test(src[j])) j--
  if (j < 0) return true
  const c = src[j]
  if (!/[A-Za-z0-9_$)\]]/.test(c)) return true
  if (c === ')' || c === ']' || /[0-9]/.test(c)) return false
  // 是标识符结尾：看看那个标识符是不是 return/typeof 这类关键字
  let k = j
  while (k >= 0 && /[A-Za-z0-9_$]/.test(src[k])) k--
  const word = src.slice(k + 1, j + 1)
  return REGEX_KEYWORDS.has(word)
}

export function stripComments(src) {
  let out = ''
  let i = 0
  const n = src.length
  while (i < n) {
    const c = src[i]
    const c2 = src[i + 1]
    if (c === '/' && c2 === '/') {
      while (i < n && src[i] !== '\n') { out += ' '; i++ }
      continue
    }
    if (c === '/' && c2 === '*') {
      out += '  '
      i += 2
      while (i < n && !(src[i] === '*' && src[i + 1] === '/')) { out += src[i] === '\n' ? '\n' : ' '; i++ }
      out += '  '
      i += 2
      continue
    }
    if (c === '/' && isRegexStart(src, i)) {
      // 正则字面量：跳到未转义的 `/`（[] 内不算）
      out += ' '
      i++
      let inClass = false
      while (i < n) {
        const d = src[i]
        if (d === '\\') { out += '  '; i += 2; continue }
        if (d === '[') inClass = true
        else if (d === ']') inClass = false
        else if (d === '/' && !inClass) { out += ' '; i++; break }
        else if (d === '\n') { break }
        out += ' '
        i++
      }
      continue
    }
    if (c === '\'' || c === '"' || c === '`') {
      const j = readStringish(src, i).end
      out += src.slice(i, j)
      i = j
      continue
    }
    out += c
    i++
  }
  return out
}

// ---------------------------------------------------------------------------
// 2) 表达式 → 路径 token 序列
// ---------------------------------------------------------------------------

/** 插值表达式分类。 */
function classifyInterp(expr) {
  const e = expr.trim()
  if (e === '') return T_ANY
  if (/^[A-Za-z_$][\w$]*$/.test(e)) return T_SEG // 局部变量：id / q / code
  if (/^encodeURIComponent\s*\(/.test(e) || /^encodeURI\s*\(/.test(e)) return T_SEG
  // 三元：多半是 `${q ? '?..' : ''}` 这种查询串尾巴
  if (e.includes('?') && e.includes(':')) return T_QRY
  if (/^[A-Za-z_$][\w$]*(\.[A-Za-z_$][\w$]*|\[[^\]]*\])+$/.test(e)) return T_SEG
  if (/^[A-Za-z_$][\w$]*\s*\(/.test(e)) return T_ANY
  return T_ANY
}

/**
 * 常量表里的通配哨兵（flattenForConst 写进去的 \u0001 / \u0002）在**再解析**时
 * 必须还原成通配 token，不能当成普通字面字符。
 *
 * 这不是假想：`frontend/src/services/flashcards.ts` 就是这个形状——
 *   const path = `${BASE}${query ? `?${query}` : ''}`   // BASE 是 const，尾部是查询串
 *   const body = await http<...>(path)                    // 调用点只有一个标识符
 * 哨兵没还原的话，模式变成 `^/api/flashcards<哨兵>$`，与路由永不相等，
 * 于是**真实调用被判成 test-only**（方向危险：把活着的东西报成死的）。
 */
const SENTINELS = { '': T_SEG, '': T_QRY }

function pushLit(out, text) {
  if (!text) return
  let buf = ''
  for (const ch of text) {
    const k = SENTINELS[ch]
    if (k === undefined) { buf += ch; continue }
    if (buf) { out.push({ t: 'lit', v: buf }); buf = '' }
    out.push({ t: 'wild', k })
  }
  if (buf) out.push({ t: 'lit', v: buf })
}

function tokensFromParts(parts, ctx) {
  const toks = []
  for (const p of parts) {
    if (p.lit !== undefined) {
      pushLit(toks, p.lit)
      continue
    }
    const e = p.expr.trim()
    const simple = /^[A-Za-z_$][\w$]*$/.test(e) ? e : null
    const resolved = simple ? resolveIdent(simple, ctx) : null
    if (resolved) {
      toks.push(...tokensFromString(resolved, ctx))
      continue
    }
    toks.push({ t: 'wild', k: classifyInterp(e) })
  }
  return toks
}

function tokensFromString(s, ctx) {
  const fake = readStringish('`' + s.replace(/`/g, '') + '`', 0)
  return tokensFromParts(fake.parts, ctx)
}

/**
 * 合并相邻字面量，并把查询串从**第一个 `?`** 起切出来。
 * 两处都要治：`'/api/tasks' + '?all=1'`（问号在新字面量的开头），
 * 以及 `` `/api/mobile/sessions?${params}` ``（问号在同一字面量的末尾）——
 * 第二种第一版没治，于是正则里带上了字面的问号，跟路由 `/api/mobile/sessions`
 * 比不相等，真调用被判成没人调。
 * （注：这段注释里刻意不写正则字面量，因为 `?[^/]*` 与前导的任意匹配拼在一起
 *  会出现 `*` + `/` 相邻，等于提前结束块注释，把后半句变成代码。）
 */
export function normalizeToks(toks) {
  const out = []
  for (const tk of toks) {
    if (tk.t === 'lit') {
      const qi = tk.v.indexOf('?')
      if (qi === 0) { out.push({ t: 'wild', k: T_QRY }); continue }
      if (qi > 0) {
        const last = out[out.length - 1]
        if (last && last.t === 'lit') last.v += tk.v.slice(0, qi)
        else out.push({ t: 'lit', v: tk.v.slice(0, qi) })
        out.push({ t: 'wild', k: T_QRY })
        continue
      }
      const last = out[out.length - 1]
      if (last && last.t === 'lit') out[out.length - 1] = { t: 'lit', v: last.v + tk.v }
      else out.push(tk)
      continue
    }
    out.push(tk)
  }
  return out
}

/** 解析一个可能带 `+` 链的路径表达式，返回 {toks, end}。 */
function readPathExpr(src, i, ctx) {
  const toks = []
  let j = i
  let any = false
  for (;;) {
    j = skipWs(src, j)
    const c = src[j]
    if (c === '\'' || c === '"' || c === '`') {
      const r = readStringish(src, j)
      toks.push(...tokensFromParts(r.parts, ctx))
      j = r.end
      any = true
    } else if (/[A-Za-z_$]/.test(c ?? '')) {
      let k = j
      while (k < src.length && /[\w$]/.test(src[k])) k++
      const name = src.slice(j, k)
      const after = skipWs(src, k)
      if (src[after] === '.') {
        // 成员访问：base.replace(...) 这类，值不可静态确定
        toks.push({ t: 'wild', k: T_ANY })
        k = skipToStatementEnd(src, after)
      } else {
        const resolved = resolveIdent(name, ctx)
        if (resolved) toks.push(...tokensFromString(resolved, ctx))
        else toks.push({ t: 'wild', k: T_SEG })
      }
      j = k
      any = true
    } else if (c === '(') {
      toks.push({ t: 'wild', k: T_ANY })
      j = skipBalanced(src, j)
      any = true
    } else {
      break
    }
    const nx = skipWs(src, j)
    if (src[nx] === '+') { j = nx + 1; continue }
    break
  }
  return { toks: normalizeToks(toks), end: j, any }
}

function skipWs(src, i) {
  let j = i
  while (j < src.length && /\s/.test(src[j])) j++
  return j
}
function skipBalanced(src, i) {
  let depth = 0
  let j = i
  while (j < src.length) {
    const c = src[j]
    if (c === '\'' || c === '"' || c === '`') { j = readStringish(src, j).end; continue }
    if (c === '(') depth++
    else if (c === ')') { depth--; if (depth === 0) return j + 1 }
    j++
  }
  return j
}
function skipToStatementEnd(src, i) {
  // 从 `.` 开始跳到下一个顶层 `,` `)` `;` 换行（够用了：值不可静态确定）
  let j = i
  let depth = 0
  while (j < src.length) {
    const c = src[j]
    if (c === '\'' || c === '"' || c === '`') { j = readStringish(src, j).end; continue }
    if (c === '(' || c === '[' || c === '{') depth++
    else if (c === ')' || c === ']' || c === '}') { if (depth === 0) return j; depth-- }
    else if (depth === 0 && (c === ',' || c === ';' || c === '\n')) return j
    j++
  }
  return j
}

// ---------------------------------------------------------------------------
// 3) 常量表
// ---------------------------------------------------------------------------

const CONST_RE = /(?:^|[\s;{}()])(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=\s*/g

export function collectConsts(src, ctx) {
  CONST_RE.lastIndex = 0
  let m
  while ((m = CONST_RE.exec(src)) !== null) {
    const name = m[1]
    const at = m.index + m[0].length
    if (ctx.local.has(name)) continue
    const r = readPathExpr(src, at, ctx)
    if (!r.any) continue
    const toks = r.toks
    // 纯别名：const b2 = b1
    if (toks.length === 1 && toks[0].t === 'wild' && toks[0].k === T_SEG) {
      // 无法静态求值，记为动态
      ctx.local.set(name, '@dyn')
      continue
    }
    const s = flattenForConst(toks)
    if (s === null) { ctx.local.set(name, '@dyn'); continue }
    ctx.local.set(name, s)
  }
}

function flattenForConst(toks) {
  // 把 token 序列压成一个字符串（用于常量表的再解析）。含 T_ANY 的返回 null。
  let s = ''
  for (const tk of toks) {
    if (tk.t === 'lit') { s += tk.v; continue }
    if (tk.k === T_ANY) return null
    if (tk.k === T_SEG) { s += '\u0001'; continue }
    if (tk.k === T_QRY) { s += '\u0002'; continue }
  }
  return s
}

function resolveIdent(name, ctx, depth = 0) {
  if (depth > 8) return null
  const v = ctx.local.has(name) ? ctx.local.get(name) : ctx.global.get(name)
  if (v === undefined || v === null) return null
  if (v === '@dyn') return null
  if (v.startsWith('@id:')) return resolveIdent(v.slice(3), ctx, depth + 1)
  return v
}

// ---------------------------------------------------------------------------
// 4) 匹配
// ---------------------------------------------------------------------------

export function toRegex(toks) {
  let s = '^'
  for (const tk of toks) {
    if (tk.t === 'lit') s += tk.v.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
    else if (tk.k === T_SEG) s += '[^/]*'
    else if (tk.k === T_QRY) s += '(?:\\?[^#]*)?'
    else s += '.*'
  }
  return new RegExp(s + '$')
}

/**
 * 静态前缀与「家族根」。
 *
 * 第一版这里有个空转的规则：它对**任何**模式都切出最后一段 '/' 之前的部分当「祖先」，
 * 于是 `http('/api/agents')` 这种完整路径也拿到了 ancestor='/api'，
 * 而 `/api/audit/logs'.startsWith('/api/')` 恒真 ⇒ **每一条 /api/* 路由都被判成 prefix**，
 * 47 条真死接线（/api/audit/* 等）被这条空规则吃掉了。
 *
 * 正确语义：只有当模式里真的出现了动态段（T_SEG / T_ANY）时，它才是一个「家族」，
 * 才有「家族根」这回事。完整路径（无通配，或只有查询串通配）的 ancestor 必须是 null。
 */
export function staticParts(toks) {
  let acc = ''
  let stop = 'none' // none=完整路径 | qry=只有查询串尾巴 | wild=有动态段
  for (const tk of toks) {
    if (tk.t === 'lit') {
      if (stop === 'none') acc += tk.v
      continue
    }
    if (tk.k === T_QRY) {
      if (stop === 'none') stop = 'qry'
      continue
    }
    stop = 'wild'
    break // 动态段之后的字面量不再计入静态前缀
  }
  const isFamily = stop === 'wild'
  const cut = isFamily ? acc.slice(0, acc.lastIndexOf('/') + 1) : acc
  return {
    prefix: acc,
    cut,
    isFamily,
    sawAny: toks.some((t) => t.t === 'wild' && t.k === T_ANY),
    ancestor: isFamily && cut !== '' ? cut.replace(/\/$/, '') : null,
  }
}

/**
 * 模式里是否把 `path` 原样写成了字面量（忽略尾部查询串）。
 * 根级单段路由（/healthz、/ws、/plugin/ws）只认这个：
 * 形如 `/` + 段通配 的模式是**根级动态路径**，它能正则匹配上 /healthz，
 * 但那是"根下有某个名字"的家族，不是"这个根端点被调过"。
 */
function literalCover(toks, path) {
  let s = ''
  for (const tk of toks) {
    if (tk.t === 'lit') { s += tk.v; continue }
    if (tk.k === T_QRY) break
    return false
  }
  return s === path || s === path + '/'
}

/** 这个模式对某个注册路由的覆盖强度。null = 不覆盖。 */
export function matchStrength(toks, route) {
  const isSub = route.endsWith('/')
  const base = isSub ? route.slice(0, -1) : route
  const sp = staticParts(toks)
  const re = toRegex(toks)
  // 根级单段路由的"强证据"门槛（见 literalCover 的说明）
  const singleSeg = /^\/[^/]+$/.test(base)
  const strong = !singleSeg || literalCover(toks, base)
  if (strong) {
    if (re.test(base) || re.test(route)) return { tier: 'exact', any: sp.sawAny }
    if (sp.ancestor !== null && sp.ancestor === base) return { tier: 'exact', any: sp.sawAny }
    if (isSub && (sp.prefix === base || sp.prefix.startsWith(base + '/'))) return { tier: 'exact', any: sp.sawAny }
  }
  // 下面两条是「弱证据」的前缀命中。家族根深度必须 ≥ 2 段：
  // `/api/${x}/detail` 的家族根是 `/api`，而 `'/api/audit/logs'.startsWith('/api/')`
  // 恒真 —— 深度 1 的家族根会让每一条 /api/* 路由都"沾边"，那一档就等于没有判别力。
  const depth = sp.ancestor ? sp.ancestor.split('/').filter(Boolean).length : 0
  if (depth >= 2) {
    if (!isSub && sp.ancestor !== null && base.startsWith(sp.ancestor + '/')) return { tier: 'prefix', any: sp.sawAny }
    if (isSub && sp.prefix.startsWith(base + '/')) return { tier: 'prefix', any: sp.sawAny }
  }
  // 根级动态路径命中根级单段路由：只算弱证据，绝不算精确
  if (!strong && re.test(base)) return { tier: 'prefix', any: sp.sawAny }
  return null
}

/**
 * 外部 / 浏览器入口：不是被前端 fetch 调用的，而是被**别的东西**打到后端的。
 * 这一档存在的理由：把它们算进 uncalled 会误导人去删端点——
 * 例如 /callback/feishu 被报成"前端从不调用"，但它恰恰是飞书事件回调的落点。
 *
 * 白名单而非黑名单：默认仍按"没调用点"处理，**举证责任在豁免这一侧**。
 * 每条都必须写明依据；依据不成立就别进这张表。
 * （/plugin/ws 故意**不**在这张表里：全仓只有 docs/DEPLOYMENT_SUCCESS.md 里
 *  一条手动 wscat 命令，够不上"产品有调用方"，所以它留在 uncalled 里等人裁决。）
 */
export const EXTERNAL_ENTRIES = {
  '/callback/feishu': '飞书事件回调，由飞书服务器 POST（server.go:681 注释：56 nginx 转发到 9010）',
  '/callback/weixin': '企业微信事件回调，由企业微信服务器 POST；刻意不套 requireAuth，靠 msg_signature 校验',
  '/callback/email/oauth': '邮箱 OAuth provider 认证后浏览器跳转回本端，用 state 而非 JWT 校验',
  '/api/auth/sso/callback': 'IdP 认证后的浏览器跳转目标：LoginView.vue:378 把它当 redirect_url 传给 /api/auth/sso/login，由后端消费绑定 cookie 后 302 到 SPA',
  '/api/app/download': '应用内更新下载：前端 utils/version.ts 的 downloadAPK() 走 window.open(url)，url 来自后端版本配置的 downloadUrl（docs/AUTO_UPDATE_FEATURE.md 里就配成本端点），所以路径不由前端书写',
  '/ws': '客户端用 new WebSocket(...) 握手（api/websocket.ts:37、services/websocket-hub.ts:48、stores/opencode.ts:277），URL 由 buildWebSocketUrl 拼 pathname 为 .../ws，不经过 fetch',
  '/healthz': '部署侧健康检查：nginx 把 /healthz 代理到 pocketd（PLAN.md 反代约定）',
}

const VERDICTS_BASE = ['called', 'test-only', 'referenced-only', 'prefix-only', 'uncalled']
// 外部入口表的自检：key 必须是真实注册路由，否则一个打错的 key 会静默豁免一条真死路由。
export function checkExternalEntries(getRoutes) {
  const routes = new Set(getRoutes())
  const problems = []
  for (const [k, why] of Object.entries(EXTERNAL_ENTRIES)) {
    if (!routes.has(k)) problems.push(`${k} 不是已注册路由（打错字？）`)
    if (!why || why.length < 12) problems.push(`${k} 缺少依据`)
  }
  return problems
}

/**
 * 分档判定。
 *
 * 第一版用「一个初始为 99 的分数，越小越好」来选最佳模式，而带主机前缀的
 * exact 匹配（`${resolveApiBase()}/api/tasks` 这种**真实调用**）算出来是 100
 * —— 于是它们被静默丢掉，路由被判成 uncalled。方向很坏：把真调用报成死接线。
 * 分数、取整、任何"越小越好"的技巧都会重演这个坑，所以这里改成**按档位顺序短路**。
 * （注：本文件所有注释都不写正则字面量——`?[^/]*` 之类里相邻的星号加斜杠
 *  会提前结束块注释，把后半句变成代码。这个坑我今天踩了两次。）
 */
export function classify(route, pats) {
  let productExact = null
  let testExact = null
  let productPrefix = null
  for (const p of pats) {
    const m = matchStrength(p.toks, route)
    if (!m) continue
    if (m.tier === 'exact') {
      if (p.fromTest) testExact = testExact ?? p
      else productExact = productExact ?? p
    } else if (!p.fromTest) {
      productPrefix = productPrefix ?? p
    }
  }
  if (productExact) return { verdict: 'called', via: productExact }
  if (testExact) return { verdict: 'test-only', via: testExact }
  if (productPrefix) return { verdict: 'prefix-only', via: productPrefix }
  return { verdict: 'uncalled', via: null }
}

// ---------------------------------------------------------------------------
// 5) 扫描
// ---------------------------------------------------------------------------

function walk(dir, out = []) {
  for (const n of readdirSync(dir)) {
    const p = join(dir, n)
    if (statSync(p).isDirectory()) walk(p, out)
    else if (/\.(ts|tsx|vue|js|mjs)$/.test(p)) out.push(p)
  }
  return out
}

function isTestFile(p) {
  return /(^|[\\/])(__tests__|tests|e2e)[\\/]/.test(p) || /\.(test|spec)\.[a-z]+$/.test(p)
}

export function isTestFilePath(p) { return isTestFile(p) }
export { walk, isTestFile }

export function collectPatterns(stripped, ctx, fromTest, file) {
  const pats = []
  const n = stripped.length
  let i = 0
  while (i < n) {
    const c = stripped[i]
    if (c !== '\'' && c !== '"' && c !== '`') { i++; continue }
    // 只对看起来是路径的字面量解析，避免把 base64 / 正则 / 文案当路径。
    // 模板必须额外放过 `${...}` 开头——那正是「整段来自常量」的写法
    // （S2/S6 就是这种），v1 与本版的第一版都死在这里。
    const head = stripped[i + 1] ?? ''
    const tmplStart = c === '`' && (head === '$' || head === '{')
    if (head !== '/' && head !== '.' && !tmplStart) { i = readStringish(stripped, i).end; continue }
    const r = readPathExpr(stripped, i, ctx)
    if (!r.any) { i = r.end; continue }
    const toks = r.toks
    const firstLit = toks.find((t) => t.t === 'lit')
    if (!firstLit) { i = r.end; continue }
    // 路径特征：以 / 开头，或以 . 开头且第二段像路径
    const v0 = firstLit.v
    const looksPath = v0.startsWith('/') || v0.startsWith('./') || v0.startsWith('../')
    if (!looksPath) { i = r.end; continue }
    const callSite = isCallSite(stripped, i)
    pats.push({ toks, fromTest, callSite, file, at: i })
    i = r.end
  }
  const all = pats.concat(scanIdentArgCalls(stripped, ctx, fromTest, file))
  // 完全相同的 (模式, 是否实参位置) 记录去重：重复不影响判定，但会虚增计数。
  // 注意**不能**把 callSite 不同的两条合成一条——那正是本轮修的缺陷本身。
  const seen = new Set()
  return all.filter((p) => {
    const k = JSON.stringify(p.toks) + '|' + p.callSite
    if (seen.has(k)) return false
    seen.add(k)
    return true
  })
}

/**
 * 第二遍：抓「实参只有一个标识符」的调用。
 *
 * 为什么必须单独一遍：collectPatterns 是从**引号**起扫的，
 * `const path = \`...\`` 产生的那条模式 callSite=false（它在声明处，不在实参处）；
 * 而 `await http<...>(path)` 这个调用点**一个引号都没有**，第一遍完全看不见它。
 * 于是 flashcards.ts:59-60 这种写法（先存变量再传）整条链被判成 test-only。
 * 方向危险：把真实调用报成只有测试在调。
 */
function scanIdentArgCalls(stripped, ctx, fromTest, file) {
  const out = []
  for (let i = 0; i < stripped.length; i++) {
    if (stripped[i] !== '(') continue
    if (!isCalleeBefore(stripped, i)) continue
    let j = skipWs(stripped, i + 1)
    const c = stripped[j]
    // 只接**标识符**开头的实参；引号开头的已由第一遍处理，不重复计入
    if (!c || !/[A-Za-z_$]/.test(c)) continue
    if (resolveIdent(stripped.slice(j).match(/^[A-Za-z_$][\w$]*/)[0], ctx) === null) continue
    const r = readPathExpr(stripped, j, ctx)
    if (!r.any || !looksLikePath(r.toks)) continue
    out.push({ toks: r.toks, fromTest, callSite: true, file, at: j })
  }
  return out
}

function isCallSite(src, i) {
  let j = i - 1
  while (j >= 0 && /\s/.test(src[j])) j--
  if (src[j] !== '(') return false
  return isCalleeBefore(src, j)
}

/** 这个 `(` 前面是不是一个被调用者（允许 `http<...>(` 这种带泛型的形态）。 */
function isCalleeBefore(src, paren) {
  let j = paren - 1
  while (j >= 0 && /\s/.test(src[j])) j--
  if (src[j] === '>') { // 泛型实参：<...>，深度配对
    let depth = 1
    j--
    while (j >= 0 && depth > 0) {
      if (src[j] === '>') depth++
      else if (src[j] === '<') depth--
      j--
    }
    while (j >= 0 && /\s/.test(src[j])) j--
  }
  if (j < 0 || !/[\w$)\]]/.test(src[j])) return false
  while (j >= 0 && /[\w$.]/.test(src[j])) j--
  return true
}

/** 调用实参里只有一个标识符时，字面量扫描是**看不见**这条调用的。 */
function looksLikePath(toks) {
  const first = toks.find((t) => t.t === 'lit')
  if (!first) return false
  const v = first.v
  return v.startsWith('/') || v.startsWith('./') || v.startsWith('../')
}

// ---------------------------------------------------------------------------
// 6) 后端路由
// ---------------------------------------------------------------------------

export function registeredRoutes(goSrc) {
  const out = []
  const re = /mux\.HandleFunc\(\s*"([^"]+)"/g
  let m
  while ((m = re.exec(goSrc)) !== null) out.push(m[1])
  return [...new Set(out)]
}

// ---------------------------------------------------------------------------
// 7) 判据自检 + 盲版对照
// ---------------------------------------------------------------------------

export function blindCandidatePaths(src) {
  // v1 的写法，留着做盲版对照：它会漏掉嵌套泛型、模板嵌套反引号、跨常量拼接
  const out = new Set()
  const re = /\b(?:http|fetch)\s*(?:<[^>]*>)?\s*\(\s*[`'"]([^`'"]+)[`'"]/g
  let m
  while ((m = re.exec(src)) !== null) out.add(m[1])
  return [...out]
}

export function runSelfChecks() {
  const results = []
  const check = (name, fn) => {
    try {
      const detail = fn()
      results.push({ name, ok: true, detail })
    } catch (e) {
      results.push({ name, ok: false, detail: e.message })
    }
  }
  const ctx = () => ({ local: new Map(), global: new Map() })
  /**
   * 自检夹具必须和 main 走同一条流水线：先收常量，再抽模式。
   * 第一版夹具只调 collectPatterns，于是 `${base}` 恒解析不出来，
   * S2/S6 报红——那是夹具的错，不是判据的错。流水线不一致的夹具毫无意义。
   */
  const scan = (src, name) => {
    const c = ctx()
    const stripped = stripComments(src)
    collectConsts(stripped, c)
    return { c, stripped, pats: collectPatterns(stripped, c, false, name) }
  }
  const hits = (pats, route) => pats.filter((p) => matchStrength(p.toks, route)?.tier === 'exact')
  const dump = (pats) => JSON.stringify(pats.map((p) => p.toks))

  // --- 正样本：必须抽到 ---
  const S1 = "async function f(){ return http(`/api/emails${q ? `?${q}` : ''}`) }"
  check('S1 模板里嵌三元+嵌套反引号 → /api/emails', () => {
    const { pats } = scan(S1, 'S1')
    if (hits(pats, '/api/emails').length === 0) throw new Error('没匹配上，抽到 ' + dump(pats))
    if (!pats[0].callSite) throw new Error('callSite 判定失败')
    return 'ok'
  })
  const S2 = "const base = '/api/marketplace'\nconst x = http(`${base}/packages`)"
  check('S2 跨常量拼接 → /api/marketplace/packages', () => {
    const { pats } = scan(S2, 'S2')
    if (hits(pats, '/api/marketplace/packages').length === 0) throw new Error('没匹配上：' + dump(pats))
    return 'ok'
  })
  const S3 = "const y = http<Record<string, unknown>>(`/api/meetings/${id}`)"
  check('S3 嵌套泛型 + 段插值 → 覆盖 /api/meetings/', () => {
    const { pats } = scan(S3, 'S3')
    // 注意：这里必须用 hits() 而不是 find()?.tier —— find 返回的是**元素**，
    // 元素上没有 .tier，?.tier 恒为 undefined，断言会假红（第一版就栽在这）。
    if (hits(pats, '/api/meetings/').length === 0) throw new Error('没匹配上：' + dump(pats))
    if (!pats[0].callSite) throw new Error('嵌套泛型下 callSite 判定失败')
    return 'ok'
  })
  const S4 = "await authFetch('/api/redclaw/knowledge/search', { method: 'POST' })"
  check('S4 另一个客户端 authFetch → /api/redclaw/knowledge/search', () => {
    const { pats } = scan(S4, 'S4')
    if (hits(pats, '/api/redclaw/knowledge/search').length === 0) throw new Error('没匹配上')
    return 'ok'
  })
  const S5 = "const u = '/api/tasks' + '?all=1'\nhttp(u)"
  check('S5 字符串 + 拼接 → /api/tasks', () => {
    const { pats } = scan(S5, 'S5')
    if (hits(pats, '/api/tasks').length === 0) throw new Error('没匹配上：' + dump(pats))
    return 'ok'
  })
  const S6 = "const b = '/api/scheduled-tasks'\nconst run = () => http(`${b}/run`)\nconst dead = '/api/never/called'"
  check('S6 同一个常量既被调用也被单独声明 → 两个都在册', () => {
    const { pats } = scan(S6, 'S6')
    const a = hits(pats, '/api/scheduled-tasks/run').length > 0
    const b = hits(pats, '/api/never/called').length > 0
    if (!a || !b) throw new Error(`run=${a} dead=${b}；抽到 ` + dump(pats))
    return 'ok'
  })

  // --- 负样本：必须抽不到 ---
  const N1 = "// http('/api/comment/line/only')\nconst a = 1"
  check('N1 行注释里的路径不算调用点', () => {
    const { pats } = scan(N1, 'N1')
    if (pats.length !== 0) throw new Error('抽到了 ' + dump(pats))
    return 'ok'
  })
  const N2 = "/*\n  fetch('/api/comment/block/only')\n*/\nconst b = 2"
  check('N2 块注释里的路径不算调用点', () => {
    const { pats } = scan(N2, 'N2')
    if (pats.length !== 0) throw new Error('抽到了 ' + dump(pats))
    return 'ok'
  })
  const N3 = "const s = 'data:text/plain;base64,AAA='\nconst t = `/api/ok`\nfetch(t)"
  check('N3 base64/非路径字面量不误当路径', () => {
    const { pats } = scan(N3, 'N3')
    // 断言的是**去重后的首个字面量集合**只有 /api/ok，不是记录条数、也不是正则源码
    // （正则源码里 `/` 被转义成 `\/`，拿它去 includes('/api/ok') 会假红）：
    // 同一个路径会出现在「声明处」与「实参处」两条记录里（callSite 不同，语义也不同）。
    const paths = [...new Set(pats.map((p) => (p.toks.find((t) => t.t === 'lit') || {}).v))]
    if (paths.length !== 1 || paths[0] !== '/api/ok') {
      throw new Error('抽到的路径不止 /api/ok：' + JSON.stringify(paths))
    }
    return 'ok'
  })
  const N4 = "const q = String(x) / y / z\nconst p2 = http('/api/plain')"
  check('N4 除法不破坏后续解析', () => {
    const { pats } = scan(N4, 'N4')
    if (hits(pats, '/api/plain').length === 0) throw new Error('漏了 /api/plain：' + dump(pats))
    return 'ok'
  })

  // --- 负控：判据本身必须能红 ---
  check('NEG 完整路径不得覆盖任意 /api/* 路由（第一版 ancestor 空转，把 47 条真死接线吃成了 prefix）', () => {
    const agents = [{ t: 'lit', v: '/api/agents' }]
    if (matchStrength(agents, '/api/audit/logs') !== null) {
      throw new Error('一条完整路径 /api/agents 覆盖了 /api/audit/logs —— ancestor 空转规则还在生效')
    }
    if (matchStrength(agents, '/api/agents')?.tier !== 'exact') throw new Error('反过来把自己也丢了')
    const fam = [{ t: 'lit', v: '/api/' }, { t: 'wild', k: T_SEG }, { t: 'lit', v: '/detail' }]
    if (matchStrength(fam, '/api/audit/detail')?.tier !== 'exact') throw new Error('家族模式的前缀匹配坏了')
    // 深度 1 的家族根（/api）会让每一条 /api/* 都"沾边"，那一档必须不给力
    const shallow = [{ t: 'lit', v: '/api/' }, { t: 'wild', k: T_SEG }, { t: 'lit', v: '/other' }]
    if (matchStrength(shallow, '/api/audit/logs') !== null) {
      throw new Error('家族根只有 /api 一段，却覆盖了 /api/audit/logs —— 门槛没生效')
    }
    return '完整路径 ancestor=null；家族根 ≥2 段才计入 prefix；家族模式仍能按 /api/<x>/detail 命中'
  })
  check('S7 主机前缀拼接 `${resolveApiBase()}/api/x` → 精确命中 /api/x', () => {
    const src = 'const u = `${resolveApiBase()}/api/mobile/sessions?${params}`\nauthFetch(u)'
    const { pats } = scan(src, 'S7')
    if (hits(pats, '/api/mobile/sessions').length === 0) throw new Error('没匹配上：' + dump(pats))
    return 'ok'
  })
  check('NEG 只有主机前缀模式命中的路由，必须是 called 而不是 uncalled', () => {
    // `${resolveApiBase()}/api/tasks` 这种形态：模式自带 `.*` 前缀。
    // 第一版用「越小越好的分数」选最佳模式，初始分 99 比这类匹配的 100 还小，
    // 于是**真实调用被静默丢掉**，/api/tasks 被报成 uncalled。方向极坏。
    const hostPrefixed = [{ toks: [{ t: 'wild', k: T_ANY }, { t: 'lit', v: '/api/tasks' }], fromTest: false, callSite: true, file: 'client.ts' }]
    const r = classify('/api/tasks', hostPrefixed)
    if (r.verdict !== 'called') throw new Error('主机前缀的真实调用被判成 ' + r.verdict)
    const none = classify('/api/nowhere', hostPrefixed)
    if (none.verdict !== 'uncalled') throw new Error('没有模式命中的路由被判成 ' + none.verdict)
    const onlyTest = classify('/api/tasks', [{ ...hostPrefixed[0], fromTest: true }])
    if (onlyTest.verdict !== 'test-only') throw new Error('只有测试在调却被判成 ' + onlyTest.verdict)
    return 'called / uncalled / test-only 三档都按预期'
  })
  check('NEG 根级动态路径不得精确命中根级单段路由（`/${x}` 曾把 /healthz、/ws 判成已接线）', () => {
    const rootFamily = [{ t: 'lit', v: '/' }, { t: 'wild', k: T_SEG }]
    for (const r of ['/healthz', '/ws', '/plugin/ws']) {
      const m = matchStrength(rootFamily, r)
      if (m?.tier === 'exact') throw new Error('根级动态路径把 ' + r + ' 判成了 exact')
    }
    if (matchStrength([{ t: 'lit', v: '/healthz' }], '/healthz')?.tier !== 'exact') {
      throw new Error('字面写出的 /healthz 反过来不算 exact，门槛过严')
    }
    // 判据本身写对了，但这一行曾经假红：`matchStrength(...)?.tier !== null`
    // 在 matchStrength 返回 null 时算出 `undefined !== null` = true。
    // 本文件今天被 `?.` 咬了三次（S3 的 find、S7 的族模式、这里）：
    // **对可能返回 null 的函数，不要用 ?. 取字段再和 null 比**。
    const multi = matchStrength(rootFamily, '/api/notes')
    if (multi !== null) {
      throw new Error('根级动态路径不该覆盖多段路由，却得到 ' + JSON.stringify({ m: multi, re: toRegex(rootFamily).source, sp: staticParts(rootFamily) }))
    }
    return '/healthz /ws /plugin/ws 只认字面；`/${x}` 只算弱证据'
  })
  check('S8 路径先存进变量、再以标识符传入调用（flashcards.ts 的真实形状）', () => {
    const src = [
      "const BASE = '/api/flashcards'",
      'async function pullCards(since = 0) {',
      '  const params = new URLSearchParams()',
      "  if (since > 0) params.set('since', String(since))",
      '  const query = params.toString()',
      '  const path = `${BASE}${query ? `?${query}` : \'\'}`',
      '  return await http<Partial<X>>(path)',
      '}',
    ].join('\n')
    const { pats } = scan(src, 'S8')
    if (hits(pats, '/api/flashcards').length === 0) {
      throw new Error('没匹配上：' + dump(pats) + ' —— 哨兵没被还原成通配就会被当字面字符')
    }
    // 关键：必须存在**实参位置**的命中。上一版这条自检是靠 `const BASE = '...'`
    // 那行字面量过的，而那行不在实参位置 ⇒ 自检因为瞎才绿。
    // 真实缺陷是 `http(path)` 这种"实参只有一个标识符"的调用，第一遍扫不到。
    const viaCall = pats.filter((p) => p.callSite && matchStrength(p.toks, '/api/flashcards')?.tier === 'exact')
    if (viaCall.length === 0) {
      throw new Error('只有声明处的模式命中，实参位置的调用仍然不可见：' + dump(pats))
    }
    return 'ok'
  })
  check('NEG 去掉注释抹除 → N1 立刻变假阳性（证明这一步有判别力）', () => {
    const broken = (s) => s // 故意不抹注释
    const c = ctx()
    const p = collectPatterns(broken(N1), c, false, 'N1')
    if (p.length === 0) throw new Error('注释放开后仍抽不到，说明 N1 本来就没被注释遮住，自检无意义')
    return '盲判据抽到 ' + dump(p)
  })
  check('NEG 段通配不得跨 / —— 否则「精确匹配」这个判定本身就是假的', () => {
    const seg = [{ t: 'lit', v: '/api/emails/' }, { t: 'wild', k: T_SEG }]
    const any = [{ t: 'lit', v: '/api/emails/' }, { t: 'wild', k: T_ANY }]
    const deep = '/api/emails/any/deep'
    if (matchStrength(seg, deep)?.tier === 'exact') throw new Error('T_SEG 越界产生了 exact 判定')
    if (matchStrength(any, deep)?.tier !== 'exact') throw new Error('T_ANY 本该 exact，说明通配分类写坏了')
    if (matchStrength(seg, deep)?.tier !== 'prefix') throw new Error('T_SEG 对深路径应当只算 prefix')
    return 'T_SEG 不跨 /（只落到 prefix 弱证据层），T_ANY 跨 /（落到 exact）'
  })
  check('NEG 把 constant-vs-call 分层去掉 → S6 那个「声明了但没进请求」的路径会被当成接线', () => {
    const { pats } = scan(S6, 'NEG')
    const dead = pats.find((p) => matchStrength(p.toks, '/api/never/called')?.tier === 'exact')
    if (!dead) throw new Error('连字面量都没抽到，负控前提不成立')
    if (dead.callSite) throw new Error('这个字面量被判成调用点，负控前提不成立')
    return 'S6 的 dead 路径 callSite=false，正是 referenced-only 那一档的来源'
  })

  // --- 盲版对照：v1 写法与本判据的差集必须被记录 ---
  const REAL = "const base = '/api/marketplace'\nhttp(`${base}/packages`)\nhttp(`/api/emails${q ? `?${q}` : ''}`)\nhttp<Record<string, unknown>>(`/api/meetings/${id}`)"
  check('DIFF 盲版比好判据少看见的路径必须被量化', () => {
    const { pats } = scan(REAL, 'DIFF')
    const routes = ['/api/marketplace/packages', '/api/emails', '/api/meetings/']
    const goodHits = routes.filter((r) => hits(pats, r).length > 0)
    const blindSet = new Set(blindCandidatePaths(stripComments(REAL)))
    const blindHits = routes.filter((r) => blindSet.has(r) || [...blindSet].some((b) => r.startsWith(b)))
    const miss = goodHits.filter((r) => !blindHits.includes(r))
    if (miss.length === 0) throw new Error('差集为空——说明对照样本没踩到盲版的坑，对照无效')
    return `好判据 ${goodHits.length}/3，盲版 ${blindHits.length}/3，盲版漏 ${miss.join(' ')}`
  })

  return results
}

/** main 里在算分档之前调用：外部入口表的 key 必须与后端注册表对得上。 */
function assertExternalEntries(routes) {
  const problems = checkExternalEntries(() => routes)
  if (problems.length > 0) {
    console.error('外部入口表有问题，拒绝出结论（一张打错字的豁免表比没有表更糟）：')
    for (const p of problems) console.error('  ' + p)
    process.exit(3)
  }
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// main —— 仅在直接运行时执行；被 import 时（探针/测试）只导出函数
// ---------------------------------------------------------------------------

const isEntry = process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)
if (isEntry) await main()

async function main() {
const self = runSelfChecks()
const failed = self.filter((r) => !r.ok)
if (failed.length > 0) {
  console.error('判据自检未通过，输出不可信（先修判据再看数据）：')
  for (const f of self) console.error(`  ${f.ok ? 'PASS' : 'FAIL'}  ${f.name}${f.ok ? ' — ' + f.detail : ' — ' + f.detail}`)
  process.exit(3)
}

let goSrc = ''
for (const g of GO_FILES) goSrc += '\n' + readFileSync(g, 'utf8')
const routes = registeredRoutes(goSrc)
if (routes.length === 0) {
  console.error('没解析出任何路由——判据失效，不要相信输出')
  process.exit(3)
}
assertExternalEntries(routes)

const files = walk(SRC)
// 全局常量表 + 冲突检测
const gctx = { local: new Map(), global: new Map() }
const conflict = new Map()
for (const f of files) {
  const c = { local: new Map(), global: gctx.global }
  collectConsts(stripComments(readFileSync(f, 'utf8')), c)
  for (const [k, v] of c.local) {
    if (gctx.global.has(k) && gctx.global.get(k) !== v) {
      conflict.set(k, (conflict.get(k) ?? 0) + 1)
      gctx.global.set(k, '@dyn')
    } else if (!gctx.global.has(k)) {
      gctx.global.set(k, v)
    }
  }
}
// 第二遍：局部表按文件重建（此时全局表已定型，别名可解析）
const pats = []
let constCount = 0
for (const f of files) {
  const stripped = stripComments(readFileSync(f, 'utf8'))
  const c = { local: new Map(), global: gctx.global }
  collectConsts(stripped, c)
  constCount += c.local.size
  pats.push(...collectPatterns(stripped, c, isTestFile(f), relative(ROOT, f)))
}

const callPats = pats.filter((p) => p.callSite)
const rows = routes.map((r) => {
  const c = classify(r, callPats)
  let verdict = c.verdict
  let via = ''
  if (verdict === 'called') via = c.via ? relative(ROOT, c.via.file) : ''
  else if (verdict === 'external-entry') via = EXTERNAL_ENTRIES[r]
  else if (verdict === 'prefix-only') {
    const contributors = callPats.filter((p) => matchStrength(p.toks, r)?.tier === 'prefix')
    const uniq = [...new Set(contributors.map((p) => relative(ROOT, p.file)))]
    via = `${uniq.length} 个模式沾边，如 ${uniq.slice(0, 2).join('、')}`
  }
  return { route: r, verdict, via }
})

// 外部入口只对**确实没有调用点**的路由生效；已经有前端调用点的照旧算 called，
// 否则这张表会变成一张"想让谁算就算"的万能 excuse。
for (const r of routes) {
  const row = rows.find((x) => x.route === r)
  if (row.verdict === 'uncalled' && EXTERNAL_ENTRIES[r]) {
    row.verdict = 'external-entry'
    row.via = EXTERNAL_ENTRIES[r]
  }
}

const VERDICTS = ['called', 'external-entry', 'test-only', 'referenced-only', 'prefix-only', 'uncalled']

// --why <route>：把某条路由的判定链打出来。否定结论必须能被追到具体模式，
// 否则「uncalled」和「我判错了」在输出里长得一模一样。
const WHY = argv.includes('--why') ? argv[argv.indexOf('--why') + 1] : ''
if (WHY) {
  const row = rows.find((r) => r.route === WHY)
  console.log(`路由 ${WHY} → 判定 ${row ? row.verdict : '(不在注册表里)'}`)
  const cands = callPats
    .map((p) => ({ p, m: matchStrength(p.toks, WHY) }))
    .filter((x) => x.m)
    .slice(0, 12)
  console.log(`命中模式 ${cands.length} 条（最多列 12）：`)
  for (const { p, m } of cands) {
    console.log(`  [${m.tier}${m.any ? ' any' : ''}] callSite=${p.callSite} test=${p.fromTest} ${relative(ROOT, p.file)}`)
    console.log(`      ${toRegex(p.toks).source}`)
  }
  if (cands.length === 0) console.log('  （没有任何模式命中）')
  process.exit(0)
}

// 路由是否有 {id} 形式——若有，判据的匹配规则需要扩展，先说出来而不是静默
const braceRoutes = routes.filter((r) => r.includes('{'))
if (braceRoutes.length > 0) {
  console.error(`注意：有 ${braceRoutes.length} 条路由含 {param} 形式，本判据未覆盖其匹配语义：${braceRoutes.join(' ')}`)
}

const byVerdict = {}
for (const v of VERDICTS) byVerdict[v] = rows.filter((r) => r.verdict === v)

if (AS_JSON) {
  console.log(JSON.stringify({ self, rows, conflicts: [...conflict.keys()], files: files.length, patterns: pats.length, callPatterns: callPats.length }, null, 2))
} else {
  console.log(`后端注册路由 ${routes.length} 条（来自 ${GO_FILES.map((g) => g.split('\\').pop()).join(', ')}）`)
  console.log(`前端扫描 ${files.length} 个文件，常量 ${constCount} 个（同名冲突 ${conflict.size} 个已排除），路径表达式 ${pats.length} 条，其中位于调用实参位置 ${callPats.length} 条`)
  console.log(`判据自检 ${self.length}/${self.length} 通过：`)
  for (const s of self) console.log(`  PASS  ${s.name} — ${s.detail}`)
  console.log('')
  for (const v of VERDICTS) {
    console.log(`【${v}】${byVerdict[v].length} 条`)
    for (const r of byVerdict[v]) console.log(`  ${r.route}${r.via ? '   ← ' + r.via : ''}`)
    console.log('')
  }
  if (conflict.size > 0) {
    console.log('同名不同值的常量（已排除出解析表，相关路径可能落在 uncalled 里，属已知盲区）：')
    console.log('  ' + [...conflict.keys()].join(' '))
    console.log('')
  }
  console.log('这份表的方向性与范围：')
  console.log('  uncalled = 在 **frontend/src 范围内**既没有调用点、也没有任何路径字面量。')
  console.log('    它不等于"全仓没人调"：已实测 25 条里有 2 条被运维/诊断脚本调用')
  console.log('    （/api/integration/status <- scripts/verify-app-llm-chat.mjs；')
  console.log('      /api/opencode/instances/ <- scripts/probe-instances-api.mjs），')
  console.log('    原生 Android 与 e2e 也可能调用，本表都没覆盖。')
  console.log('  called = 有前端调用点，但**不等于运行时可达**：调用点可能在一个没人用的导出函数里。')
  console.log('    已实测这种"接线完整但从不发生"的只有 1 条：/api/assets/sync（assetsApi 无人使用），')
  console.log('    与 frontend/scripts/check-dead-api.mjs 的符号层判定对撞得出。')
  console.log('  test-only = 只有测试在调；referenced-only = 有路径字面量但没进过请求。')
  console.log('  prefix-only = 只在前缀意义上相关，弱证据，不要当成接线。')
  console.log('  external-entry = 由浏览器跳转 / WebSocket 握手 / 外部平台回调打到后端，逐条附依据。')
  console.log('  动态拼接（运行期决定整段路径）对本判据不可见，见 prefix-only 与上面冲突名单。')
}

if (CSV) {
  const esc = (v) => `"${String(v).replace(/"/g, '""')}"`
  const order = [...VERDICTS]
  const body = rows
    .slice()
    .sort((a, b) => order.indexOf(a.verdict) - order.indexOf(b.verdict) || a.route.localeCompare(b.route))
    .map((r) => [r.verdict, r.route, r.via].map(esc).join(','))
  writeFileSync(CSV, ['verdict,route,via'].concat(body).join('\n') + '\n', 'utf8')
  if (!AS_JSON) console.log(`\nCSV 已写入 ${CSV}`)
}
} // end main
