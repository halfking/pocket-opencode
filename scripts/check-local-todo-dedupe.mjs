// check-local-todo-dedupe.mjs —— 门禁：每一处 `INSERT INTO local_todos` 都必须先查重。
//
// 这道门是被 §153 的一段审计逼出来的
// ---------------------------------
// 2026-10-07 顺着「重复总结会造出两遍待办与两遍日程」那条线修了三处，
// 修完才发现：**同一个缺陷在三条不同的写入点上都存在**，
// 而我此前两次「修好了」都是因为手查链，没有按那件事本身普查
// （`INSERT INTO local_todos` 全仓三处，我只查过两处）。
//
// ⇒ 修完的当轮读数是「三处都有查重」，但那是**读数不是不变量**：
//   没有任何东西阻止第四处裸 INSERT 出现，而这道门当时并不存在。
//   本门就是把它变成不变量。
//
// 判据（三条，缺一不可）
// --------------------
//   1. **集合**：扫出全部含 `INSERT INTO local_todos` 的**生产**文件（测试文件排除），
//      逐条与登记表 `SITES` 比对。多了 → 有人新加了写入点却没来这里表态；
//      少了 → 有人删了写入点，登记表要跟着改。
//   2. **属性**：每一处的**剥注释后**源码里必须有查重查询
//      （`queryOne` + 按来源列过滤 + `extracted_from_voice`）。
//      ⚠ 只查「文件里有 queryOne」不够 —— 查重可能被放在**别的函数**里。
//   3. **覆盖下限**：`sites.length < MIN_SITES` 直接非 0 退出。
//      本门是「集合为空即通过」型判据：抽取器哪天坏了 / glob 写错了，
//      它会安静地全绿，而空集合同样满足「没有违规」。
//      （与 check-pg-schema-scope 的 `MIN_DECLARING_FILES` 同一形状，
//        §77 记过那个共享棘轮库 —— 范式已经在仓里，这里是照抄。）
//
// ⚠ 为什么必须**剥注释**再数：注释里写「INSERT INTO local_todos」是本节文档与
//   源码注释的常态（我自己在三处源码注释里就各写了一遍）⇒ 不剥会把注释算成写入点。
//
// 用法
// ----
//   node scripts/check-local-todo-dedupe.mjs --selftest   # 只跑判据自测（含负控）
//   node scripts/check-local-todo-dedupe.mjs             # 门禁本体
//   （接线方式与其它 check:* 一致：`--selftest && <本体>`）

import { readFileSync, readdirSync, statSync, mkdtempSync, mkdirSync, writeFileSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join, dirname, relative, sep } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..')
const SRC = join(ROOT, 'frontend', 'src')

/**
 * 登记表：全仓 `INSERT INTO local_todos` 的写入点。
 *
 * ★ 加一处写入点就必须来这里加一行 —— 门会强制你表态（这是本门存在的主要理由）。
 *   这与 §143 的 `TestRefineMetaFieldNamesAreStable` 同一形状：
 *   **登记表是有意变更时该改的地方，而不是拿来放宽判据的。**
 *
 * ⚠ 2026-10-07 读数（普查得来，不是手查链得来的）：三处。
 *   读数 + 日期，不是永久值 —— 第四处出现时本门会报红。
 */
const SITES = [
  { file: 'features/notes/note-todo-persist.ts', owner: '随手记行动项' },
  { file: 'features/meetings/meeting-ingest.ts', owner: '会议收尾编排' },
  { file: 'features/meetings/meeting-todo-persist.ts', owner: '会议详情页「总结」按钮' },
]

/**
 * 登记表：`scheduledTasksApi.create(` 的全部出口。
 *
 * ★ 为什么也要登记（§157.5 普查得来的读数，2026-10-07）：
 *   全仓 5 处，**只有 `derived: true` 那一处由总结派生**
 *   （`ensureTodoReminder`，已被查重守卫覆盖，见 `reminderFollowsDedupe`）。
 *   另四处都不派生：`config-sync` 恢复用户自己的任务、`store` 是 UI 建改任务、
 *   `handoffTodoToAcc` / `handoffMeetingToAcc` 是**用户显式点按**的交接
 *   （成功后还会 `router.push` 跳走，二次点击几乎不可能）。
 *   ⇒ 「提醒侧有没有旁路」的答案是**没有** —— 但那是**读数**。
 *   本表把它变成不变量：新增一个出口却不来登记 ⇒ 报红。
 *
 * ⚠ `derived: true` 的行必须落在**已登记的 todo 写入链**所在文件或它直接依赖的提醒层，
 *   否则「查重 ⇒ 跳过 ⇒ 不建提醒」这条保证就断了。
 */
const REMINDER_EXITS = [
  { file: 'native/config-sync/runtime.ts', sites: 1, derived: false, why: '云端配置同步（恢复用户自己的任务）' },
  { file: 'features/scheduled-tasks/store.ts', sites: 1, derived: false, why: 'UI 建 / 改计划任务' },
  { file: 'features/meetings/meeting-todo-persist.ts', sites: 2, derived: false, why: '用户显式「转交 ACC」/「下达任务给 ACC」' },
  { file: 'features/meetings/meeting-due-reminder.ts', sites: 1, derived: true, why: 'ensureTodoReminder —— 唯一由总结派生的一条' },
]

/** 覆盖下限：少于这个数说明抽取器坏了（空集合同样满足「没有新增」）。 */
const MIN_REMINDER_EXITS = 4

/** 覆盖下限：低于这个数说明抽取器/glob 坏了（空集合同样满足「没有违规」）。 */
const MIN_SITES = 3

// ── 剥注释（等长空白替换，保持行结构；不剥会把注释里的 SQL 算成写入点）────
const blank = (m) => m.replace(/[^\n]/g, ' ')
function stripComments(t) {
  return t
    .replace(/\/\*[\s\S]*?\*\//g, blank)
    .split('\n')
    .map((l) => {
      const i = l.indexOf('//')
      return i < 0 ? l : l.slice(0, i)
    })
    .join('\n')
}

/** 是不是测试文件（测试里的桩会写 `INSERT INTO local_todos` 字面量，不算写入点）。 */
function isTestFile(rel) {
  return /(^|[/\\])(__tests__|tests?)([/\\])/.test(rel) || /\.(test|spec)\.[cm]?[jt]sx?$/.test(rel)
}

function walk(dir, out = []) {
  // ⚠ 目录不存在时返回空集而不是抛错：负控 ⑧ 要用「不存在的目录」来证明
  //   **覆盖下限那一格真的在兜**，而那道控制恰恰不能先崩掉。
  let names
  try {
    names = readdirSync(dir)
  } catch {
    return out
  }
  for (const name of names) {
    const p = join(dir, name)
    const st = statSync(p)
    if (st.isDirectory()) {
      if (name === 'node_modules' || name === '__tests__') continue
      walk(p, out)
    } else if (/\.[cm]?[jt]sx?$/.test(name) && !isTestFile(relative(SRC, p))) {
      out.push(p)
    }
  }
  return out
}

/**
 * 扫出全部含 `INSERT INTO local_todos` 的生产文件。
 * @param {string} base 源码根（自检时传临时副本）
 */
export function findTodoInsertSites(base = SRC) {
  const hits = []
  for (const p of walk(base)) {
    const code = stripComments(readFileSync(p, 'utf8'))
    if (!code.includes('INSERT INTO local_todos')) continue
    const rel = relative(base, p).split(sep).join('/')
    hits.push({ rel, code })
  }
  return hits.sort((a, b) => (a.rel < b.rel ? -1 : 1))
}

/**
 * 这一处有没有查重。判据锚在**剥注释后**的三件事上：
 *   · 一次 `queryOne`（查重必然是一次查询）
 *   · 按来源列过滤（`note_id` 或 `meeting_id`）—— 否则跨来源会误判成重复
 *   · `extracted_from_voice` —— 否则用户手工建的同名待办会被当成重复而丢
 */
export function hasDedupeGuard(code) {
  if (!/\bqueryOne\s*\(/.test(code)) return false
  if (!/\bextracted_from_voice\b/.test(code)) return false
  if (!/\b(note_id|meeting_id)\b/.test(code)) return false
  // 必须真的把来源列**用在条件里**，而不是只出现在 INSERT 的列清单里
  return /WHERE[^`]*\b(note_id|meeting_id)\b/is.test(code)
}

/**
 * 取包含 `at` 的那个**块**（花括号配平）。
 *
 * ⚠⚠ 第一版是从「最近的 `function xxx(` 声明」开始配平，**错了**：
 *   本仓三处里有两处的签名是 `): Promise<{ count: number; … }> {` ——
 *   `Promise<` 里的那个 `{` 会被当成函数体的开括号，于是配平从**返回类型的对象字面量**
 *   开始，返回的「函数体」其实是那个类型片段。
 *   ⇒ 后果：那一格里什么都找不到，判据**静默地**恒为「提醒不在此处」。
 *   而它是被 ②（真实仓应当 0 问题）与两条负控同时抓到的 ——
 *   **这两条恰好就是量具坏掉时唯一会响的那两条。**
 *
 * ⇒ 第二版改为**向后找最近一个以 `{` 结尾的行**，又错了：
 *   INSERT 外面还裹着 `try {`，于是取到的是那个 try 块 ——
 *   它**只**包住 INSERT，**不含**查重的 continue、也不含 ensureTodoReminder
 *   ⇒ 三处全部报「这个循环里没有查重的 continue」。
 *
 * ⇒ 第三版：**只认循环头**（`for (...) {` / `while (...) {`）。
 *   本门关心的性质本来就是循环级的：「这一轮新建了待办，才在这一轮建提醒」，
 *   而 try/catch / if 都是更内层的、**不承载这个语义**的结构。
 *
 * ★ 两版都是**静默定位错**：那一格只会恒为「不满足」，而门照样发绿。
 *   是 ②（真实仓应当 0 问题）与两条负控同时把它抓出来的 ——
 *   **这两条恰好就是量具坏掉时唯一会响的那两条。**
 *
 * ⚠ 也不用固定字符窗口：剥注释是等长空白替换 ⇒ 窗口对**注释长度**敏感、
 *   对**代码长度**不敏感（今天绿、明天改个注释就红）。
 */
export function enclosingLoopBody(code, at) {
  const head = code.slice(0, at)
  const lines = head.split('\n')
  for (let i = lines.length - 1; i >= 0; i--) {
    if (!/\b(?:for|while)\b[^{]*\{[ \t]*$/.test(lines[i])) continue
    const open = head.length - (lines.slice(i).join('\n').length) + lines[i].length - 1
    let depth = 0
    for (let k = open; k < code.length; k++) {
      if (code[k] === '{') depth++
      else if (code[k] === '}') {
        depth--
        if (depth === 0) return code.slice(open, k + 1)
      }
    }
  }
  return null
}

/**
 * ★§155.5 关掉的那个缺口：提醒必须**跟着查重的 skip 走**。
 *
 * 问题形态：集合/属性两条判据只保证「INSERT 有查重」，而**提醒是另一个出口**
 * （`ensureTodoReminder` → `scheduledTasksApi.create`）。
 * 若有人把建提醒挪到查重**之前**，或者挪出那个循环，
 * 那么「跳过重复项」只挡住了待办、没挡住提醒
 * ⇒ 同一个时间点仍然会进两次日程，而这道门会照样发绿。
 *
 * 判据只钉两件结构事实，不做语义分析：
 *   ① 同一个**循环体**里既有查重的 `continue`，也有 `ensureTodoReminder(`；
 *   ② 且 `continue` 的位置**早于** `ensureTodoReminder(`。
 * ⇒ 恰好排除了「提醒建在查重之前」与「提醒挪出这个循环」两种失效。
 */
export function reminderFollowsDedupe(code) {
  const at = code.indexOf('INSERT INTO local_todos')
  if (at < 0) return { ok: false, why: '找不到 INSERT（调用方不应这么用）' }
  const body = enclosingLoopBody(code, at)
  if (!body) return { ok: false, why: '定位不到包含 INSERT 的循环（抽取器可能失效）' }
  // ⚠⚠ 这里**必须**把 continue 锚在「await already*」那一格上。
  //   第一版写的是 `/\)\s*continue/`，它匹配的是循环里的**第一个** continue ——
  //   而三个循环在查重之前都还有一句 `if (!item.text.trim()) continue`
  //   ⇒ 判据靠一句与查重无关的 continue 变绿 ⇒ **恒真的一种**。
  //   上一版的负控 E（在查重之前插一次建提醒）之所以插不进红，就是这个原因：
  //   插入点仍然落在那句空文本 continue **之后**，判据照样满意。
  const skip = /if\s*\(\s*await\s+already[A-Za-z]*\s*\([^)]*\)\s*\)\s*continue/.exec(body)
  if (!skip) return { ok: false, why: '这个循环里没有「查重 ⇒ continue」那一格' }
  const rem = body.indexOf('ensureTodoReminder(')
  if (rem < 0) return { ok: false, why: '这个循环里没有 ensureTodoReminder ⇒ 提醒被挪到别处了' }
  if (rem < skip.index) {
    return { ok: false, why: 'ensureTodoReminder 出现在「查重 ⇒ continue」**之前** ⇒ 跳过的只是待办，提醒照样重复' }
  }
  return { ok: true, why: '' }
}

/**
 * 普查 `scheduledTasksApi.create(` 的调用点（生产文件，剥注释，按文件计数）。
 * ⚠ 这是**文本**普查，不判语义 —— 归类由 REMINDER_EXITS 登记表承担。
 */
export function findReminderExits(base = SRC) {
  const hits = new Map()
  for (const p of walk(base)) {
    const code = stripComments(readFileSync(p, 'utf8'))
    const n = (code.match(/scheduledTasksApi\s*\.\s*create\s*\(/g) || []).length
    if (n > 0) hits.set(relative(base, p).split(sep).join('/'), n)
  }
  return hits
}

/** 判据三：create 出口必须全部登记，且派生出口**恰好一个**。 */
export function auditReminderExits(base = SRC) {
  const found = findReminderExits(base)
  const problems = []
  let derived = 0
  for (const reg of REMINDER_EXITS) {
    const n = found.get(reg.file)
    if (n === undefined) {
      problems.push(`${reg.file}：登记在 REMINDER_EXITS 里但全仓已找不到 scheduledTasksApi.create ⇒ 登记表过期`)
    } else if (n !== reg.sites) {
      problems.push(`${reg.file}：create 出口变成 ${n} 处，登记的是 ${reg.sites} 处 ⇒ 请改登记表并说明新出口是否由总结派生`)
    }
    if (reg.derived) derived++
  }
  for (const [file, n] of found) {
    if (!REMINDER_EXITS.some((r) => r.file === file)) {
      problems.push(
        `${file}：新增了 ${n} 处 scheduledTasksApi.create 却没有登记 ⇒ ` +
        `请在 REMINDER_EXITS 加一行并**声明它是否由总结派生**（派生就必须受查重守卫覆盖）`,
      )
    }
  }
  if (found.size < MIN_REMINDER_EXITS) {
    problems.push(
      `只普查到 ${found.size} 个含 scheduledTasksApi.create 的文件，低于覆盖下限 ${MIN_REMINDER_EXITS} ⇒ 抽取器坏了`,
    )
  }
  if (derived !== 1) {
    problems.push(
      `derived 出口变成 ${derived} 个，期望恰好 1 个 ⇒ 「跳过重复项 ⇒ 不建提醒」这条保证只覆盖了其中一条路径`,
    )
  }
  return { found, derived, problems }
}

export function audit(base = SRC) {
  const sites = findTodoInsertSites(base)
  const problems = []
  for (const s of sites) {
    if (!hasDedupeGuard(s.code)) {
      problems.push(`${s.rel}：有 INSERT INTO local_todos 但没有查重 ⇒ 重复总结会造出两条同款待办与两个日程时间点`)
      continue   // ⚠ 查重都没有时，提醒那一格必然也红 ⇒ 只报一次，别刷屏
    }
    const r = reminderFollowsDedupe(s.code)
    if (!r.ok) {
      problems.push(`${s.rel}：${r.why}`)
    }
  }
  const registered = new Set(SITES.map((x) => x.file))
  const found = new Set(sites.map((s) => s.rel))
  for (const f of found) {
    if (!registered.has(f)) {
      problems.push(
        `${f}：新增的写入点没有登记在 SITES 里 ⇒ 请在这里加一行并确认它带查重` +
        `（本门的存在就是为了逼新增者表态，而不是让它悄悄进来）`,
      )
    }
  }
  for (const f of registered) {
    if (!found.has(f)) {
      problems.push(`${f}：登记在 SITES 里但全仓已找不到该写入点 ⇒ 登记表过期，请改这里`)
    }
  }
  const exits = auditReminderExits(base)
  for (const p of exits.problems) problems.push(p)

  return { sites, problems, count: sites.length, exits }
}

// ── 判据自测 ────────────────────────────────────────────────────────────
function selftest() {
  const real = audit()
  let fails = 0
  const ok = (cond, msg) => {
    if (!cond) { console.log(`  ✗ ${msg}`); fails++ }
    else console.log(`  通过  ${msg}`)
  }

  console.log('自检：验证本检查仍能发现「裸 INSERT 混入」与「登记表漂移」')
  ok(real.count >= MIN_SITES,
    `① 覆盖下限：量到 ${real.count} 处（≥ MIN_SITES=${MIN_SITES}）⇒ 抽取器与 glob 有效`)
  ok(real.problems.length === 0,
    `② 真实仓当前无裸 INSERT（问题 ${real.problems.length} 条）`)

  // ★ 正控与负控都在**临时副本**上做，不碰真实源码（与 check-router-parity 同款）。
  const tmp = mkdtempSync(join(tmpdir(), 'todo-dedupe-selftest-'))
  try {
    // 直接复制整棵 src 的代价太大；只复制「被扫描到的那几处」即可，
    // 因为 audit() 只看含 INSERT 的文件，而负控要新增一个第 4 处。
    const rels = real.sites.map((s) => s.rel)
    for (const rel of rels) {
      const from = join(SRC, rel)
      const to = join(tmp, rel)
      mkdirSync(dirname(to), { recursive: true })
      writeFileSync(to, readFileSync(from, 'utf8'))
    }
    ok(audit(tmp).count === real.count,
      `③ 临时副本与真实仓量到同样多的写入点（${real.count}）⇒ 副本装载正确`)

    // 负控 A：把某一处的查重**整段删掉** ⇒ 必须报红
    const victim = join(tmp, rels[0])
    const original = readFileSync(victim, 'utf8')
    writeFileSync(victim, original.replace(/\bqueryOne\s*\(/, 'neverCalled('))
    const a = audit(tmp)
    ok(a.problems.some((p) => p.includes('没有查重')),
      '④ 负控·把查重改成一个不存在的调用 ⇒ 必须报「没有查重」（证明本门不是恒真）')
    writeFileSync(victim, original)

    // 负控 B：删掉 `extracted_from_voice` 这个限定 ⇒ 必须报红
    writeFileSync(victim, original.replace(/extracted_from_voice/g, 'something_else'))
    const b = audit(tmp)
    ok(b.problems.length > 0,
      '⑤ 负控·去掉 extracted_from_voice 限定 ⇒ 必须报红（否则会把用户手工建的待办当重复）')
    writeFileSync(victim, original)

    // 负控 C：新增**第 4 处**未登记的裸写入点 ⇒ 必须报红
    const extraRel = 'features/meetings/zzz-naked-insert.ts'
    mkdirSync(dirname(join(tmp, extraRel)), { recursive: true })
    writeFileSync(join(tmp, extraRel),
      'const q = `INSERT INTO local_todos (id, title) VALUES (?,?)`\nexport default q\n')
    const c = audit(tmp)
    ok(c.problems.some((p) => p.includes('没有登记')),
      '⑥ 负控·新增第 4 处未登记的写入点 ⇒ 必须报红（证明登记表真的是闸，不只是注释）')
    ok(c.count === real.count + 1, `⑦ 计数随新增上升（${real.count} → ${c.count}）`)

    // ⚠ 负控 D：抽取器坏掉时会「安静地全绿」—— 这里必须靠覆盖下限兜住
    ok(!(audit(join(tmp, 'does-not-exist')).count >= MIN_SITES),
      '⑧ 空目录时读数低于下限 ⇒ 覆盖下限那一格真的在兜（否则本门会恒真）')

    // ★ 负控 E/F：把建提醒挪到查重之前 / 挪出那个块 ⇒ 提醒那一格必须报红
    //   （§155.5 自己登记的缺口，本轮补上；没有它们，这两条判据是恒真的）
    const real3 = readFileSync(join(SRC, rels[0]), 'utf8')
    ok(reminderFollowsDedupe(stripComments(real3)).ok,
      '⑧b 基线形状前提：真实文件的「提醒跟着 skip 走」那格是绿的')

    // E：在查重**之前**多插一次建提醒 ⇒ 必须报红。
    //   手法刻意选「插入」而不是「剪切搬迁」：剪切要先找到调用的收尾大括号，
    //   而那一步本身可能出错 ⇒ 会把负控写成一个测不出东西的东西。
    const guardLine = real3.split('\n').findIndex((l) => /if \(await already/.test(l))
    ok(guardLine > 0, '⑧c 样本前提：找得到查重那一行')
    const linesE = [...real3.split('\n')]
    linesE.splice(guardLine, 0,
      '      await ensureTodoReminder({ text: "x", dueText: "", assignee: "", meetingTitle: "", at: 0, source: "x" })')
    writeFileSync(victim, linesE.join('\n'))
    const e = audit(tmp)
    ok(e.problems.some((p) => p.includes('之前')),
      '⑧d 负控·在查重之前就建提醒 ⇒ 必须报红（否则跳过的只是待办，日程仍会重复）')
    writeFileSync(victim, original)

    // F2：抽掉「查重 ⇒ continue」那一格、但**保留** queryOne 调用 ⇒ 必须报红。
    //   这一条专门钉住「锚错了 continue」这个失效：空文本 continue 还在循环里，
    //   而只认 `) continue` 的判据会照样满意。
    writeFileSync(victim, original.replace(
      /if \(await already[A-Za-z]*\([^)]*\)\) continue/g, 'if (await alreadyExistsProbe('))
    const f2 = audit(tmp)
    ok(f2.problems.some((p) => p.includes('没有「查重 ⇒ continue」')),
      '⑧f 负控·查重那格被抽掉（但 queryOne 还在）⇒ 必须报红（证明 skip 锚的是查重那一格，不是别的 continue）')
    writeFileSync(victim, original)

    // F：把建提醒的调用改名（等于「挪去别处建」）⇒ 必须报红
    writeFileSync(victim, original.replace('await ensureTodoReminder(', 'await movedAwayReminder('))
    const f = audit(tmp)
    ok(f.problems.some((p) => p.includes('没有 ensureTodoReminder')),
      '⑧e 负控·建提醒被挪出这个块 ⇒ 必须报红（证明「提醒跟着 skip 走」那格不是恒真）')
    writeFileSync(victim, original)
  } finally {
    rmSync(tmp, { recursive: true, force: true })
  }

  // 负控 G：新增一个未登记的 create 出口 ⇒ 必须报红（证明 REMINDER_EXITS 真的是闸）
  const tmpG = mkdtempSync(join(tmpdir(), 'todo-dedupe-exits-'))
  try {
    for (const rel of [...new Set([...real.exits.found.keys()])]) {
      const from = join(SRC, rel)
      const to = join(tmpG, rel)
      mkdirSync(dirname(to), { recursive: true })
      writeFileSync(to, readFileSync(from, 'utf8'))
    }
    const g0 = auditReminderExits(tmpG)
    ok(g0.problems.length === 0, '⑨ 临时副本的 create 出口与真实仓一致、无问题')
    ok(g0.derived === 1, `⑩ derived 出口恰好 1 个（实际 ${g0.derived}）`)
    mkdirSync(join(tmpG, 'features', 'meetings'), { recursive: true })
    writeFileSync(join(tmpG, 'features', 'meetings', 'zzz-bypass.ts'),
      'export async function x() { return scheduledTasksApi.create(input) }\n')
    const g = auditReminderExits(tmpG)
    ok(g.problems.some((p) => p.includes('没有登记')),
      '⑪ 负控·新增未登记的 create 出口 ⇒ 必须报红（否则「没有旁路」只是当轮读数）')
  } finally {
    rmSync(tmpG, { recursive: true, force: true })
  }

  console.log(`\n自检: ${fails === 0 ? '全部通过' : fails + ' 条失败'}`)
  if (fails) process.exit(2)
  process.exit(0)
}

if (process.argv.includes('--selftest')) {
  selftest()
} else {
  const { sites, problems, count } = audit()
  console.log(`扫描到 ${count} 处 \`INSERT INTO local_todos\`（生产文件，已剥注释）`)
  for (const s of sites) {
    const reg = SITES.find((x) => x.file === s.rel)
    console.log(`  ${reg ? '✔' : '✖'} ${s.rel}${reg ? `（${reg.owner}）` : '（**未登记**）'}`)
  }
  if (count < MIN_SITES) {
    console.error(
      `\n✗ 只量到 ${count} 处，低于覆盖下限 ${MIN_SITES} ⇒ 抽取器或 glob 坏了\n` +
      `  本门是「集合为空即通过」型判据：空集合同样满足「没有违规」。`,
    )
    process.exit(1)
  }
  if (problems.length) {
    console.error(`\n✗ ${problems.length} 处问题：`)
    for (const p of problems) console.error('  · ' + p)
    process.exit(1)
  }
  const { exits } = audit()
  // ⚠ derived 在 exits 里，不在 audit() 的返回值里 ⇒ 上一版这里写成
  //   `const { derived, exits } = audit()`，输出打成「undefined 处」。
  //   **门禁输出打 undefined 是最该修的那类问题**：读数不可信会毁掉对整道门的信任。
  const derived = exits.derived
  const totalExits = [...exits.found.values()].reduce((a, b) => a + b, 0)
  console.log(
    `日程任务出口：${exits.found.size} 个文件 / ${totalExits} 处 create，` +
    `其中**由总结派生**的恰好 ${derived} 处（其余为配置同步 / UI 建改 / 用户显式交接）`,
  )
  console.log(`\n✓ 每一处 INSERT 都有查重（${count}/${count}），且全部已登记`)
}
