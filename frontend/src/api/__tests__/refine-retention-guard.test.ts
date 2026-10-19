// refine-retention-guard.test.ts — §831：把「**不得删除原文有的内容**」这条
// **写在提示词里却无人强制**的契约，变成一道可测的守卫。
//
// 契约来源：服务端 `buildRefinePrompt` 第 3 条「**不得删除原文有的内容。」」
// 而同一函数的注释记着：这条链的职责被限定为**纠错而非改写**（第一版用「润色」
// 二字时，真网关把「CI」改成「101」、把本来正确的句子改写掉）。
//
// ⚠ 此前链路上只有两道闸，都不在这一档：
//   · 服务端 `refine_truncation_test.go` 防的是**响应被截断**（真实抓包 3867 个截断点）
//   · 客户端 `refineFromLlmContent` 的 `hasRefined` 防的是**空值**
// ⇒ 「响应完整、但模型把精校做成了摘要」这一档此前无人看守。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import {
  assessRefineRetention, REFINE_RETENTION_MIN_INPUT, REFINE_RETENTION_RATIO,
} from '../refine-retention-guard.ts'

const HERE = dirname(fileURLToPath(import.meta.url))
const API = readFileSync(join(HERE, '../../api/meetings.ts'), 'utf8')

/** 一段有真实长度的转写（超过比例判定的绝对下限）。 */
const long = (n: number, seed = '会议讨论了上季度的对比表和预算问题。') => seed.repeat(Math.ceil(n / seed.length)).slice(0, n)

describe('§831 · 留存判定（行为，纯函数）', () => {
  it('★★ 响应完整但被砍到一半以下 ⇒ 必须判不通过', () => {
    const original = long(600)
    const v = assessRefineRetention({ original, refined: long(120) })
    assert.equal(v.ok, false, '把精校做成摘要却放行 ⇒ 用户看到少了很多的正文，徽章还写「已精校」')
    assert.match(v.reason, /refine-too-short/, '原因要可观测，便于与「空 / 截断」区分')
  })

  it('★ 正常精校（只纠错、加 [发言人] 前缀）必须放行', () => {
    const original = long(600)
    const v = assessRefineRetention({
      original,
      // 典型形态：每行加 [发言人] 前缀 ⇒ 变**长**
      refined: original.split('。').map((s) => `[发言人] ${s}。`).join('\n'),
    })
    assert.equal(v.ok, true, '正常纠错被误判 ⇒ 守卫会把产品改坏')
    assert.equal(v.reason, 'ok')
  })

  it('★★ 小样本不判比例（否则「10 字变 4 字」会把正常精校判死）', () => {
    const original = '好的。'
    const v = assessRefineRetention({ original, refined: '好' })
    assert.equal(v.ok, true, `短文本被误判：${v.reason}`)
    assert.match(v.reason, /input-too-short/)
  })

  it('★ 空精校结果判不通过（兜底；正常路径由空值闸先接住）', () => {
    const v = assessRefineRetention({ original: long(600), refined: '   ' })
    assert.equal(v.ok, false)
    assert.equal(v.reason, 'refined-empty')
  })

  it('负控：非法输入不得产出 NaN 读数', () => {
    const v = assessRefineRetention({ original: undefined as never, refined: null as never })
    assert.doesNotMatch(v.reason, /NaN/, v.reason)
  })

  it('阈值是既有口径而不是新拍的', () => {
    assert.equal(REFINE_RETENTION_RATIO, 0.5, '比例变了 —— §783 的口径被改动，须重新登记')
    assert.equal(REFINE_RETENTION_MIN_INPUT, 200)
  })
})

// ── 接线层（源码扫描，⚠ 对「信号变死代码」零牙，只保证两个调用点都没漏）──
describe('§831 · 接线（扫描层，零牙已如实标注）', () => {
  it('★★ **两个**产出 refined_transcript 的调用点都必须过判定', () => {
    const hits = (API.match(/assessRefineRetention\(/g) || []).length
    assert.equal(hits, 2,
      `判定出现 ${hits} 处 —— 只改一处的话，另一条路照样能把摘要当精校展示`)
  })

  it('★ 服务端路径：不通过时必须退回原文并标记降级', () => {
    const at = API.indexOf('assessRefineRetention({ original: fallback')
    assert.ok(at > 0, '找不到 normalizeRefine 里的判定')
    // ⚠ 窗口要够宽：判定点到「正文二选一」隔着 `rejected`/`rejectedReason`/`refineInputRunes`
    //   几段。第一版取 900 ⇒ 正确代码被判红 —— 又是一次「窗口太小 ⇒ 肯定断言变假红」。
    //   900 → 1600 是上一轮加长后的补丁；§908 往 `normalizeRefine` 里补了
    //   `rejectedReason` 的一段之后实测**相距 1566 字符**，只剩 34 字符余量，
    //   任何人再加一行注释就会再红一次 ⇒ 这里一次给到 **2600**，
    //   留出约 1000 字符的余量。**判据指错位置与判据有洞同样要当场修**。
    const win = API.slice(at, at + 2600)
    // ⚠⚠ 必须锚在 **`const fromFallback = …` 那一段**里，不能对整个窗口搜
    //   `!retention.ok`：warn 前面还有一个同名的 `if (!retention.ok)` 守卫，
    //   变异把降级标记改掉之后判据照样命中那个 ⇒ **0 红**（本轮第 5 次「判据指错地方」）。
    const ffBlock = win.slice(win.indexOf('const fromFallback'))
    const hit = ffBlock.indexOf('!retention.ok')
    const close = ffBlock.indexOf(')')
    // ⚠⚠⚠ 这里踩过一个**最隐蔽的假绿**：`indexOf` 找不到时返回 **-1**，
    //   而 `-1 < 任何正数` 恒为真 ⇒ 断言在「判定根本不在」时反而通过。
    //   ⇒ 凡是拿 indexOf 的返回值做大小比较，**必须先判 -1**。
    //   （本轮 D″ 变异就是被这条抓住的 —— 它改掉的正是 fromFallback 里的判定。）
    assert.ok(hit !== -1 && close !== -1 && hit < close,
      '降级标记 fromFallback 没有把留存判定算进去 ⇒ status/badge 不会体现，用户仍被告知「已精校」')
    assert.match(win, /retention\.ok\s*\?\s*String\(serverRefined/,
      '正文没有按判定二选一 ⇒ 不通过时仍在展示那份被砍半的「精校」')
    // ⚠ 钉「这一条语句的形状」而不是「出现过 console.warn」——
    //   否则 `void 0 && console.warn(...)`（信号变死代码）照样全绿。
    //   ⚠⚠ 这个坑我在 §828 的判据上刚犯过一次并写进了记忆，换一份判据又犯了一次
    //   ⇒ 它不是「记下来就够了」的教训，必须变成**每次写扫描断言时的默认动作**。
    assert.match(win, /^\s*console\.warn\(/m, '留存判定不通过却没有出声（或出声被写成了死代码）')
  })

  it('★ 浏览器兜底路径：正文同样按判定二选一', () => {
    const at = API.indexOf('assessRefineRetention({ original: transcript')
    assert.ok(at > 0, '找不到 refineFromLlmContent 里的判定')
    const win = API.slice(at, at + 400)
    assert.match(win, /hasRefined && retention\.ok \? \(raw as string\) : transcript/,
      '兜底路径没有按判定二选一')
  })
})
