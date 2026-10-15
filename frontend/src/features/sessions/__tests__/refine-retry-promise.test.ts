/**
 * refine-retry-promise.test.ts — 「提示用户去某个地方重试」时，那个地方必须真的有。
 *
 * ── 这道门守的是什么 ──
 * 原文案是 `toast.warning('录音已结束，精翻稍后可在会议详情重试')`，
 * 而 `meetingsApi.refine` 全仓**只有一个调用点**（同一个文件的 stop() 里），
 * `MeetingDetailView` / `use-meeting-studio` 里搜不到任何 refine 触发器
 * ⇒ 用户被指引到一个**不存在**的入口（§44.6）。
 *
 * 它属于本仓反复记的那一族（「桶名是对判据的承诺」「别承诺产品做不到的事」），
 * 但**形态不同**：§39 那道门量的是「代码写没写」，这道量的是
 * **承诺与现实是否一致** —— 所以不能写成「断言文案里不出现『重试』」，
 * 那会把「真的把入口做出来了」也判成红。
 *
 * ⇒ 判据写成**条件式**：
 *   提示里承诺了某个入口 ⇒ 该入口必须存在；
 *   没承诺 ⇒ 本条不表态（不阻止别人把入口补出来）。
 *
 * 三种状态都必须是绿的，只有「承诺 + 没兑现」红：
 *   · 今天：文案已改 ⇒ 绿；
 *   · 旧文案写回去且没入口 ⇒ 红（正是 §44.6 那个缺陷）；
 *   · 入口做出来了又写回承诺 ⇒ 仍绿（承诺兑现了，不该拦）。
 */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const HERE = dirname(fileURLToPath(import.meta.url))
// HERE = frontend/src/features/sessions/__tests__ → 上四级到 frontend，再进 src
const SRC = join(HERE, '../../../..', 'src')
const read = (p: string) => readFileSync(p, 'utf8')

function walk(dir: string, out: string[] = []): string[] {
  for (const n of readdirSync(dir)) {
    if (n === '__tests__' || n === 'node_modules') continue
    const p = join(dir, n)
    try {
      if (statSync(p).isDirectory()) walk(p, out)
    else if (/\.test\.(ts|mjs|js)$/.test(n)) continue // ★ 测试文件与源码并排（本仓约定），
    //   不排掉它们的话，测试里那句「meetingsApi.refine 全仓只有本文件一个调用点」
    //   会被当成一个调用点 —— 实测把 1 处数成 3 处。
    else if (['.ts', '.vue'].includes(p.slice(p.lastIndexOf('.')))) out.push(p)
    } catch (err) {
      // §632：并发下别的用例会往 `frontend/src/` 下**临时建目录再删掉**
      // （z-index-ladder 的 `__probe_fixture__`）。递归时撞上那个窗口会抛
      // ENOENT ⇒ **整条门假红**。这里只吞 ENOENT，其余照抛。
      if (err?.code !== 'ENOENT') throw err
    }
  }
  return out
}

/** 剥注释（行 + 块）。判据的两头都要用同一个口径 —— 见 toastLiterals 的注。 */
function stripComments(source: string): string {
  return source
    .replace(/\/\*[\s\S]*?\*\//g, ' ')
    .split('\n')
    .map((l) => l.replace(/\/\/.*$/, ''))
    .join('\n')
}

/**
 * 精校调用点所在的文件（跨 src 全域扫，不限 meetings）。
 *
 * ⚠ 两次修正的记录，两次都是**射程**问题：
 *  1. v1 不过滤注释 —— `useSessionLiveRecord.ts` 的注释里就写着
 *     「`meetingsApi.refine` 全仓只有本文件一个调用点」，
 *     这句**关于**调用点的说明被当成了一个调用点，实测 1 处数成 3 处。
 *  2. v2 只认字面量 `meetingsApi.refine(` —— 一旦实现入口时用了别名
 *     （`import { meetingsApi as x }` ⇒ `x.refine(`）就漏判。
 *     实测把「已兑现」判成「未兑现」。
 * ⇒ 现在按**方法调用形状**（`*.refine(`）扫，不绑死被调方名字。
 *
 * ⚠ 仍然存在的边界（写出来，别假装没有）：若有人**不用 refine 方法**
 *   （例如新开一个 BFF 流式端点、或走 `summarize` 复用）把入口做出来，
 *   本门仍会红。那种情况请在同一处把判据扩到那条路径 ——
 *   本门守的是「承诺要与现实一致」，判据本身也需要跟得上现实。
 */
function refineCallSites(): string[] {
  const hits: string[] = []
  for (const f of walk(SRC)) {
    if (/\b\w+\.refine\(/.test(stripComments(read(f)))) hits.push(f)
  }
  return hits
}

/**
 * 剥掉注释再取 toast 的字符串字面量。
 *
 * ⚠⚠ 为什么必须先剥：本文件第一版直接对**整份源码**跑
 *   /['"`][^'"`]*重试[^'"`]*['"`]/g，
 *   结果匹配从 `toast.info('已停止精翻')` 的**收尾引号**起、跨过中间整段注释，
 *   到下一处引号才结束 —— 把注释里那句「不该被提示成『稍后重试』」当成了承诺。
 * ⇒ 判据量的是「**注释里提到过某个词**」，不是「产品承诺了什么」。
 *   这与 §42（量字符距离）、§43（量字符存在）是同一族的错，只是方向不同：
 *   那两条多算了，这一条**多算了不该算的**。
 */
function toastLiterals(source: string): string[] {
  const noBlock = source.replace(/\/\*[\s\S]*?\*\//g, ' ')
  const noLine = noBlock.split('\n').map((l) => l.replace(/\/\/.*$/, '')).join('\n')
  return [...noLine.matchAll(/toast\.(?:success|info|warning|error)\(\s*(['"`])([^'"`]*)\1/g)].map(
    (m) => m[2],
  )
}

/** 显示这条 toast 的文件；它自己的 stop() 调用点**不算**重试入口。 */
// §49：收尾编排（含那条 toast）搬进了共享模块，owner 随之改变。
const OWNER = 'features/meetings/meeting-recording-finalize.ts'

/**
 * 判据本体：toast 里承诺了重试入口，但**别的组件**里没有 refine 调用点 ⇒ 判红。
 *
 * ⚠ 第二版修正：v1 写成「全仓 callSites.length > 0 即算兑现」，
 *   而那唯一的一处**就是本文件 stop() 里那次调用** ——
 *   等于「承诺重试」这条判据被承诺的**产地自己**兑现了，恒真。
 * ⇒ 必须扣掉 owner 自己，只数**外部**入口。
 */
function judge(sourceText: string, allSites: string[]): { ok: boolean; why: string } {
  const promises = toastLiterals(sourceText).filter((t) => t.includes('重试'))
  if (!promises.length) return { ok: true, why: '未承诺任何重试入口 ⇒ 本条不表态' }
  const external = allSites.filter((f) => !f.endsWith(OWNER))
  if (external.length > 0) {
    return { ok: true, why: `承诺兑现：owner 之外有 ${external.length} 个 refine 调用点` }
  }
  return {
    ok: false,
    why:
      `toast 向用户承诺了「${promises[0]}」，但 refine 在 stop() 之外**零调用点**` +
      ` ⇒ 承诺的入口不存在。要么实现它，要么别这么写。`,
  }
}

describe('P · 承诺的入口必须真的存在', () => {
  const sites = refineCallSites()

  it('★ 精校提示不得指向不存在的重试入口', () => {
    const r = judge(read(join(SRC, OWNER)), sites)
    assert.ok(r.ok, r.why)
  })

  it('判据的取值方法本身可用（不是「扫不到所以绿」）', () => {
    // 这条盯的是**量具**：必须扫得到那个已知的唯一调用点。
    // 若哪天 walk() 的路径写错、扫不到任何东西，上面那条会**因为 promises 为空**
    // 而「不表态」地绿过去 —— 那正是恒真判据的形状。
    assert.ok(
      sites.some((f) => f.endsWith(OWNER)),
      `量具失明：没扫到已知的 refine 调用点，实得 ${JSON.stringify(sites)}`,
    )
    assert.ok(read(join(SRC, 'features/meetings/MeetingDetailView.vue')).length > 0, '读不到详情页，前提失效')
  })
})

describe('P · 负控：判据必须会在「承诺不兑现」时判红', () => {
  // ⚠ 这些负控**不读活文件**。
  // 第二版是从 useSessionLiveRecord.ts 派生出 mutant 再判红，结果做变异时
  // （把旧承诺真的写回去）负控的前提塌了 —— 它把「活文件当前有没有承诺」
  // 当成了自己的输入，于是判据一变，负控跟着一起变。
  // ⇒ 负控必须用它**自己**的样本，且要能测「判定」而不是「当前文案」。
  const PROMISE = "toast.warning('录音已结束，精翻稍后可在会议详情重试')"
  const NO_PROMISE = "toast.warning('录音已结束，精翻未生效（分段转写已保存）')"
  // 注释里提到「重试」**不算**承诺 —— 第一版正是栽在这里。
  const ONLY_COMMENT =
    "// 「已取消」不该被提示成「稍后重试」\ntoast.info('已停止精翻')"

  it('承诺 + 无外部入口 ⇒ 必须判红', () => {
    const r = judge(PROMISE, [OWNER])
    assert.equal(r.ok, false, `承诺不兑现时判据仍放行（${r.why}）`)
    assert.match(r.why, /零调用点/, '失败原因要指向「入口不存在」')
  })

  it('承诺 + 外部入口存在 ⇒ 必须放行（否则这道门会阻止别人把入口补出来）', () => {
    const r = judge(PROMISE, [OWNER, 'features/meetings/MeetingDetailView.vue'])
    assert.equal(r.ok, true, `入口存在时不该拦（${r.why}）`)
    assert.match(r.why, /兑现/)
  })

  it('只承诺自己那处调用点（owner）不算兑现', () => {
    // v1 的教训：owner 自己的 stop() 调用点一度把这条判据恒真地「兑现」掉。
    assert.equal(judge(PROMISE, [OWNER]).ok, false)
  })

  it('注释里提到「重试」不算承诺', () => {
    const r = judge(ONLY_COMMENT, [OWNER])
    assert.equal(r.ok, true)
    assert.match(r.why, /不表态/, '注释不是承诺')
  })

  it('不承诺任何重试入口 ⇒ 不表态地放行', () => {
    const r = judge(NO_PROMISE, [OWNER])
    assert.equal(r.ok, true)
    assert.match(r.why, /不表态/)
  })
})