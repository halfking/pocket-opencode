// local-todos-write-only.test.ts — §395：`local_todos` 的可见性不变量。
//
// ★ 这道门钉的是一条**架构事实**，不是某段文案
//
// 会议链、随手记链、入库链共三处 `INSERT INTO local_todos`，
// 而全仓**没有任何一处读回 title/due_at 供界面展示**（唯一那处 `listMeetingTodos`
// 已登记在 `dead-features-baseline.json` 里死代码）。
// ⇒ 这些待办是**设备本地、无读回方、不同步**的。
//
// ⚠ 这条事实的后果是：用户点「总结」看到「已加入 N 条待办」，却在应用里找不到它们。
//   **怎么修是产品决策**（§395.3 待拍板 #5），本门**不替它拍板**。
//   本门只做两件与拍板结果无关、永远成立的事：
//   ① 任何读回方都**不得**用 UTC 切片取「哪一天」（§393 那个坑）；
//   ② 把「读回方有几个 / 其中几个是活的」作为**读数**打印出来，
//      而不是打印一句「一致」「通过」——
//      无条件打印的通过语比不打印更坏（§388 教训）。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import path from 'node:path'
import fs from 'node:fs'
import { fileURLToPath } from 'node:url'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const SRC = path.resolve(HERE, '..', '..', '..') // → src（本文件在 src/features/notes/__tests__/）

/** 递归收集 src 下的 .ts / .vue 源文件（跳过测试与声明）。 */
function collect(dir: string, out: string[] = []): string[] {
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, e.name)
    if (e.isDirectory()) {
      collect(p, out)
    } else if (
      /\.(ts|vue)$/.test(e.name) && !/\.(test|spec)\./.test(e.name) && !e.name.endsWith('.d.ts') &&
      // ⚠ 2026-10-29：排除 `native/local-db.ts` —— 它的「INSERT INTO local_todos」
      //   是**归属迁移的一次性搬运语句**（旧行搬进带归属的新表），
      //   不是**业务写入方**。本门要数的是「谁往 local_todos 写业务数据」，
      //   把迁移算进去会让门永远多计一处 ⇒ 门失效。
      !(path.relative(SRC, p) === path.join('native', 'local-db.ts'))
    ) {
      out.push(p)
    }
  }
  return out
}

/**
 * 剥掉注释行，只留代码。
 *
 * ⚠ 不剥会怎样，本门自己撞过一次：`meeting-todo-persist.ts:22` 的文档注释
 * 写着「全仓一共三处 `INSERT INTO local_todos`」——**那行是注释**，
 * 于是「普查写入方」数出 4 处，门当场红了。
 * 这正是「守卫写整文件子串匹配会被注释满足 → 必须先剥注释」那条纪律。
 *
 * 只剥「整行都是注释」的情况（行首是 `*` / `//` / `/*`），
 * 不去动行尾注释 —— 免得把字符串里的 `http://` 当成注释截断。
 * 代价：行尾注释里出现的 `INSERT INTO local_todos` 仍会被数进去。
 * ⚠ 那是一种**偏严**（多计），与「偏松（漏计）」方向相反，会让门变吵而不会让它失明。
 */
function stripCommentLines(text: string): string[] {
  const out: string[] = []
  let inBlock = false
  for (const line of text.split('\n')) {
    const t = line.trim()
    if (inBlock) {
      if (t.includes('*/')) inBlock = false
      out.push('')
      continue
    }
    if (t.startsWith('/*')) {
      if (!t.includes('*/')) inBlock = true
      out.push('')
      continue
    }
    if (t.startsWith('*') || t.startsWith('//')) {
      out.push('')
      continue
    }
    out.push(line)
  }
  return out
}

const files = collect(SRC)

interface Readback {
  file: string
  line: number
  snippet: string
}

const reads: Readback[] = []
const writes: Readback[] = []
for (const f of files) {
  const rel = path.relative(SRC, f)
  stripCommentLines(fs.readFileSync(f, 'utf8')).forEach((line, i) => {
    if (/INSERT\s+INTO\s+local_todos/i.test(line)) writes.push({ file: rel, line: i + 1, snippet: line.trim() })
    if (/FROM\s+local_todos/i.test(line)) {
      // 只把「取回内容」的查询算读回；查重用的 `SELECT id FROM local_todos` 不算。
      const isContentRead = /SELECT\s+[^;]*\b(title|due_at|description)\b/i.test(line)
      if (isContentRead) reads.push({ file: rel, line: i + 1, snippet: line.trim() })
    }
  })
}

/**
 * 该读回方是不是「已登记的死代码」。
 *
 * ⚠ 第一版按「SQL 那一行里含 `function listMeetingTodos`」来数 ⇒ 恒为 0，
 *   于是打印出「其中登记为死代码的 **0** 处」——
 *   **量具在说假话**（docs §388：门自己的输出比没有更坏）。
 *   读回方是「一个函数」，SQL 行只是它体内的一行；判据必须落在**文件**上。
 */
function isRegisteredDead(relFile: string): boolean {
  const abs = path.resolve(SRC, relFile)
  const declares = /export\s+(async\s+)?function\s+(\w+)/g
  const names = new Set<string>()
  const txt = stripCommentLines(fs.readFileSync(abs, 'utf8')).join('\n')
  let m: RegExpExecArray | null
  while ((m = declares.exec(txt)) !== null) names.add(m[2])
  return [...names].some((n) => deadBaseline.includes(`:${n}`))
}

const deadBaseline = fs.readFileSync(
  path.resolve(SRC, '..', 'scripts', 'dead-features-baseline.json'),
  'utf8',
)

describe('§395 · local_todos 可见性不变量', () => {
  it('★ 写入方仍然是三处（链数变化时这条会提醒复查）', () => {
    assert.equal(writes.length, 3,
      `INSERT INTO local_todos 现在有 ${writes.length} 处：\n` +
      writes.map((w) => `  ${w.file}:${w.line}`).join('\n') +
      '\n⇒ 写入链增减了，请复查 §395 的结论与门禁。')
  })

  it('★★ 任何读回方都不得用 UTC 切片取「哪一天」（§393 的坑不得在这张表上复发）', () => {
    // 这条与「待办该不该展示」无关：**只要有人展示，就不许用错时区**。
    //
    // ⚠ 判据必须**剥注释**再看 —— 否则它会命中 `meeting-todo-persist.ts`
    //   里 §393 写的那条「旧写法是 `toISOString().slice(0,10)`」的说明注释，
    //   把**正确的修复**报成违规。这与上面那条是同一个坑的第三次出现。
    const offenders = files
      .map((f) => ({ f, code: stripCommentLines(fs.readFileSync(f, 'utf8')).join('\n') }))
      .filter(({ f }) => /FROM\s+local_todos/i.test(fs.readFileSync(f, 'utf8')))
      .filter(({ code }) => /toISOString\(\)\s*\.\s*slice\(\s*0\s*,\s*10\s*\)/.test(code))
      .map(({ f }) => path.relative(SRC, f))

    assert.deepEqual(offenders, [],
      `这些文件既读 local_todos 又用 UTC 切片取日期 ⇒ 凌晨档会早一天：\n  ${offenders.join('\n  ')}\n` +
      '修法见 calendar-math.ts 的 dayKey + deviceTimeZone（docs §393.5）。')
  })

  it('★★ 读回方的存在性必须与设计文档 §395 的登记一致', () => {
    // 现状：唯一读回方是 listMeetingTodos（死代码）。
    // 一旦有人接上展示面，这条会红 —— 那是**有意的**：
    // 待拍板 #5 一旦落地，必须同步改这里并在 §395 里写明新的形态。
    const aliveReaders = reads.filter((r) => !isRegisteredDead(r.file))
    const listedAsDead = deadBaseline.includes('meeting-todo-persist.ts:listMeetingTodos')

    assert.equal(reads.length, 1,
      `local_todos 的内容读回方现在是 ${reads.length} 处：\n` +
      reads.map((r) => `  ${r.file}:${r.line}  ${r.snippet}`).join('\n') +
      (aliveReaders.length
        ? '\n★ 出现了**活的**读回方 ⇒ 待拍板 #5（待办该不该展示、怎么展示）已落地，\n' +
          '  请同步更新本门与设计文档 §395。'
        : '\n（读回方数量变了但没有活的读回方，请复查 §395 的结论）'))

    assert.ok(listedAsDead,
      '唯一的读回方 listMeetingTodos 已不在死功能基线里 —— ' +
      '若它已被复活，说明展示面已接上，待拍板 #5 已落地。')
  })

  // ⚠ 刻意**不断言**「没有读回方」，只打印读数。
  // 断言它等于现在的值，就会把「有人接好了展示面」变成一条必须改门才能通过的失败。
  // 打印的是事实（数量），不是结论（一致/通过）——
  // 无条件打印的通过语比不打印更坏（docs §388 教训）。
  it('读数：当前读回方与写入方（事实，非结论）', () => {
    const dead = reads.filter((r) => isRegisteredDead(r.file)).length
    process.stdout.write(
      `\n  local_todos：写入方 ${writes.length} 处；内容读回方 ${reads.length} 处` +
      `（其中登记为死代码的 ${dead} 处）\n` +
      reads.map((r) => `    ${r.file}:${r.line}  ${r.snippet}`).join('\n') + '\n' +
      `  ⇒ 这些待办当前是设备本地、无展示面的（docs §395，待拍板 #5）\n`,
    )
    assert.ok(true)
  })
})
