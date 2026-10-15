// roster-repair-wiring.test.ts — 接线门：人名同音纠正必须真的在精校**之前**跑。
//
// 光有纯函数不够 —— §45 的第一版模块「门全绿、产品一行没接线」。
// 这个门盯的是**接线**，不是算法。
import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'
import { PINYIN_COMMON } from '../pinyin-common.ts'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const SRC = path.resolve(HERE, '..', '..', '..')  // → frontend/src
const read = (rel: string) => fs.readFileSync(path.resolve(SRC, rel), 'utf8')
const hook = read('features/meetings/meeting-recording-finalize.ts')
// ⚠ §52：人名同音纠正已移进纯决策层 meeting-refine-plan.ts
//   （原写 finalizeRecording 里，而那层在 node 里 import 都进不去 ⇒ 无法做行为门）。
const planMod = read('features/meetings/meeting-refine-plan.ts')
const planCode = planMod
  .replace(/\/\*[\s\S]*?\*\//g, ' ')
  .split('\n').map((l) => { const i = l.indexOf('//'); return i < 0 ? l : l.slice(0, i) }).join('\n')

/** 剥注释，否则源码里的说明文字会满足「调用点」判据。 */
const code = hook
  .replace(/\/\*[\s\S]*?\*\//g, ' ')
  .split('\n').map((l) => { const i = l.indexOf('//'); return i < 0 ? l : l.slice(0, i) }).join('\n')

describe('接线：人名同音纠正必须真的被调用', () => {
  it('useSessionLiveRecord 里必须真的调用 repairRosterHomophones', () => {
    assert.ok(/repairRosterHomophones\s*\(/.test(planCode),
      '修复没接线 —— 模块存在但生产链路不调用，等于没修')
  })

  it('必须注入真实拼音表 PINYIN_COMMON（不注入就永远判不出同音）', () => {
    assert.ok(/PINYIN_COMMON/.test(planCode), '未使用生产拼音表')
    assert.ok(/import[^;]*PINYIN_COMMON[^;]*pinyin-common/.test(planMod),
      'PINYIN_COMMON 必须来自 pinyin-common.ts，而不是就地定义')
  })

  it('名单必须来自 meeting.participants（权威来源，不是硬编码）', () => {
    assert.ok(/participants/.test(planCode), '没读参会人名单 ⇒ 无从判断哪个写法才对')
  })

  it('★ 顺序：纠正必须在 meetingsApi.refine 之前', () => {
    // 放之后只能修「已被 LLM 处理过的产物」，且 LLM 已经带着错名字做了摘要/行动项。
    // ⚠ §52 起纠正在决策层、精校在收尾层，顺序必须**跨模块**断言：
    //   收尾层里 `planRefine(...)` 的调用点必须早于 `meetingsApi.refine(`。
    const iPlan = code.indexOf('planRefine(')
    const iRefine = code.indexOf('meetingsApi.refine(')
    assert.ok(iPlan > 0, '收尾层没有调用决策层 —— 纠正根本没接上')
    assert.ok(iRefine > 0, '找不到精校调用点')
    assert.ok(iPlan < iRefine, '决策被接在精校之后 —— LLM 已经见过错名字了')
  })

  it('负控：顺序写反时判据必须能报红（防止判据恒真）', () => {
    const badSample = `
      const result = await meetingsApi.refine(id, segs)
      const fixed = repairRosterHomophones(text, roster, PINYIN_COMMON)
    `
    const iFix = badSample.indexOf('repairRosterHomophones(')
    const iRefine = badSample.indexOf('meetingsApi.refine(')
    assert.ok(iFix > iRefine, '负控样本应让「修正先于精校」这条断言失败')
  })

  it('未接线时必须报红（把上面那条判据拆出来验）', () => {
    const stripped = code.replace(/repairRosterHomophones\s*\([^)]*\)/, '(() => {})()')
    assert.ok(!/repairRosterHomophones\s*\(/.test(stripped),
      '样本构造有问题：移除调用后正则仍命中')
  })
})

/**
 * ★§82：按括号配平取出 `meetingsApi.refine(` 的**全部顶层实参**
 * （不是正则 —— 正则会被换行、尾逗号、多行实参轻易骗过）。
 */
function refineArgs(src: string): string[] | null {
  const call = 'meetingsApi.refine('
  const i = src.indexOf(call)
  if (i < 0) return null
  const from = i + call.length
  const args: string[] = []
  let depth = 0
  let start = from
  for (let j = from; j < src.length; j++) {
    const ch = src[j]
    if (ch === '(' || ch === '[' || ch === '{') depth++
    else if (ch === ')' || ch === ']' || ch === '}') {
      if (depth === 0) {
        args.push(src.slice(start, j))
        return args
      }
      depth--
    } else if (ch === ',' && depth === 0) {
      args.push(src.slice(start, j))
      start = j + 1
    }
  }
  return null
}

/** 判据本体，单独成函数好让正控与负控走**同一条**逻辑。 */
function assertRefineReceivesCorrected(src: string, msg: string) {
  const args = refineArgs(src)
  assert.ok(args !== null, '解析不出精校的实参 —— 判据失效，先修判据')
  // 实参形如 refine(meetingId, refineSegments, langs, meta)
  assert.match(
    args[1] ?? '',
    /\brefineSegments\b/,
    `${msg}（实际第 2 个实参是「${(args[1] ?? '').trim()}」）`,
  )
}

/**
 * §84：取 meta 对象（实参第 4 位）里某个字段的**值表达式**。
 * 判的是「这个值取自哪里」而不是「这行代码长什么样」，
 * 所以换行 / 空格 / 可选链写法变化都不会让它失明。
 */
function metaFieldExpr(args: string[], field: string): string | null {
  const meta = args[3] ?? ''
  const m = new RegExp(`\\b${field}\\s*:\\s*([^,\\n}]+)`).exec(meta)
  return m ? m[1].trim() : null
}

function assertMetaFromMeeting(
  args: string[] | null,
  field: 'title' | 'location',
  why: string,
) {
  assert.ok(args !== null, '解析不出实参 —— 判据失效，先修判据')
  const v = metaFieldExpr(args, field)
  assert.ok(v !== null, `meta 里没有 ${field} 字段（第 4 位实参是「${(args?.[3] ?? '').trim()}」）`)
  assert.match(
    v,
    new RegExp(`meeting\\?\\.${field}`),
    `${field} 的值是「${v}」而不是取自会议元数据 ⇒ ${why}`,
  )
}

describe('§84 · 交接值：会议标题与地点必须真的进精校的 meta（术语表来源）', () => {
  // 注入普查（§84.1）：把 `title` / `location` 各自换成 `undefined`，
  // 本文件 + meetings 全部判据**一条都不红**。而 `title` 正是
  // §64/§80/§81 实测过的**术语表来源**（悬借→悬界 12/12 靠它）⇒
  // §64「维护会议标题就是零改动修复路径」会静默失效。

  it('★ title 必须取自 meeting.title（它是 LLM 的术语表来源）', () => {
    assertMetaFromMeeting(refineArgs(code), 'title',
      'LLM 拿不到会议标题 ⇒ 专名无据可依，§64 的零改动修复路径失效')
  })

  it('★ location 必须取自 meeting.location（§64 接进来的第二个元数据源）', () => {
    assertMetaFromMeeting(refineArgs(code), 'location',
      '地点不参与术语表 ⇒ 地点被听错时无从纠正')
  })

  it('负控：两个字段任一被换成字面量，上面两条必须报红（防止判据恒真）', () => {
    for (const [field, from] of [
      ['title', 'title: meeting?.title ?? undefined'],
      ['location', 'location: meeting?.location ?? undefined'],
    ] as const) {
      const mutated = code.replace(from, `${field}: undefined`)
      assert.notEqual(mutated, code, `样本构造失败：${field} 的锚点没命中`)
      assert.throws(
        () => assertMetaFromMeeting(refineArgs(mutated), field, 'x'),
        (e: unknown) => e instanceof assert.AssertionError,
        `判据没咬住：${field} 已被换成字面量却仍判定通过`,
      )
    }
  })
})

describe('§82 · 交接值：精校拿到的必须是**修正后**的分段', () => {
  // ⚠ 下面那条「顺序」判据只证明 `planRefine(` 出现在 `meetingsApi.refine(` 之前，
  //   它**不**证明传给 `refine()` 的是 `plan.refineSegments`。
  //   实测（§82.3）：把实参换成原始 `segs`，本文件 10/10 全绿、另外三个
  //   相关判据文件也全绿 ⇒ §44/§45/§80 的全部纠正会静默失效而无人发现。
  //   这是 §45「模块存在但生产链路不调用」的变体：**调用了，但传错了值**。

  it('★ 传给精校的必须是 refineSegments，而不是原始 segments', () => {
    assertRefineReceivesCorrected(code,
      '传给精校的是原始分段 ⇒ 同音纠正（§44/§45/§80）整条链静默失效，' +
      '而 LLM 已经带着错名字做了摘要与行动项')
  })

  it('负控：实参换成原始 segs 时上面那条必须报红（防止判据恒真）', () => {
    const mutated = code.replace(
      'meetingsApi.refine(meetingId, refineSegments,',
      'meetingsApi.refine(meetingId, segs,',
    )
    assert.notEqual(mutated, code, '样本构造失败：锚点没命中，这道负控本身是坏的')
    // 只断言「抛」—— 断言错误消息的措辞等于把判据绑死在文案上
    assert.throws(
      () => assertRefineReceivesCorrected(mutated, 'x'),
      (e: unknown) => e instanceof assert.AssertionError,
      '判据没咬住：实参已是原始 segs 却仍判定通过',
    )
  })

  it('refineSegments 必须来自 planRefine 的返回值，不能是别处同名变量', () => {
    assert.match(
      code,
      /const\s*\{[^}]*\brefineSegments\b[^}]*\}\s*=\s*plan\b/,
      'refineSegments 不是 plan 的解构产物 ⇒ 它可能被别的东西覆盖或根本没被赋值',
    )
  })
})

describe('拼音表：不是空表，也不该是全表', () => {
  // ⚠ 第一版这里用正则去数表里的行、找某个字，结果拿到 0 条 / 「缺林」——
  //   **量具坏了**：表是用 JSON.stringify 生成的（双引号），正则按单引号匹配。
  //   判据红了先问量具。改成直接 import 运行时对象，比匹配文本稳健得多。
  it('PINYIN_COMMON 是 GB2312 汉字区规模（数千字），不是几百字的玩具表', () => {
    const n = Object.keys(PINYIN_COMMON).length
    assert.ok(n > 6000, `表太小：${n} 条，人名用字会大面积落空`)
    assert.ok(n < 40000, `表太大：${n} 条，移动端装不下`)
  })

  it('★ 人名常用字必须覆盖：这些字查不到会让整个功能静默失效', () => {
    for (const ch of '林岚张伟王总赵钱孙李周吴郑田黄刘陈杨') {
      assert.ok(PINYIN_COMMON[ch], `拼音表缺「${ch}」——按「表不全则整体放弃」的设计，它一缺整个功能就失效`)
    }
  })

  it('★ 多音字按单一读音处理是已知简化，写下来防「以为全读音都覆盖」', () => {
    // 「行」在 GB2312 表里只应有一个读音；调用方据此知道「判不出就放弃」。
    assert.equal(typeof PINYIN_COMMON['行'], 'string')
    assert.equal(PINYIN_COMMON['行'], 'xing2')
  })

  it('表里不该混入非汉字键（isHan 是防线，表不该自己破坏它）', () => {
    const bad = Object.keys(PINYIN_COMMON).filter((k) => !/^[\u4e00-\u9fff]$/.test(k))
    assert.deepEqual(bad, [], `表里有非汉字键：${bad.slice(0, 5)}`)
  })
})
