// stt-price-catalog-load-bearing.test.mjs — §580：ASR 价表现在是**承重结构**，而门禁链够不着它。
//
// ── 为什么这道门存在（实测读数，2026-10-14）──────────────────────────
//
// §579 的核心结论（「能接上的中文专用 ASR 比在册最便宜的贵约 42 倍」）
// **整个架在 `backend/internal/stt/target.go` 的价表上**。
//
// ⛔ 变异实测（两条，都在本文件写下时跑过）：
//   M27  glm-asr-2512 的 `USDPerHour: 0.50` → `0.05`（同时把注释里的
//        「0.06 元/分钟」改成「0.006 元/分钟」）⇒ `go test ./...` **红了**，
//        但红在 `TestRecommendedExternalModelsAreSortedByCost`
//        （消息：「预置未按成本升序」），**跟价格对错无关**。
//   M28  同一处改成 `0.50` → **`0.45`**（**仍是最大、仍是升序**）⇒
//        ⛔ **`go test ./...` EXIT=0**、⛔ **`npm run test:stt` 111/111 全绿**。
//
// ⛔⭐ 而更根本的一层：**`npm run gates` 里根本没有 `go test`**，
//   `frontend/package.json` 里也**没有任何**跑 `go test` / `go vet` 的 script
//   ⇒ 那两道 Go 门（`TestKnownUSDPerHour` / `…SortedByCost`）
//   **不在门禁链上**，只有人手动敲 `go test ./...` 才会碰到。
//
// ⚠ 本门**不能**替代 Go 门（那是双写），它补的是**门禁链里根本不存在的那一层**，
//   并且只覆盖 §579 实际依赖的那几格。
//
// ── 三条判据 ────────────────────────────────────────────────────────
//
//   ① **golden snapshot**：在册 11 条（含 2 条 gateway 档）价逐一钉住。
//      ⭐ 这是**有意**把外部事实双写一遍 —— 代价是改价要多改一行，
//      收益是**改价的人一定会撞到这条消息**，而消息里写着谁依赖它。
//   ② **换算自洽**：注释里写了「X 元/分钟」的条目，其 `USDPerHour` 必须落在
//      一个**合理汇率带**（6.5–7.5）内。⭐ **刻意不钉具体汇率** ——
//      汇率会漂，而 §579 的结论差 42 倍量级、对精度不敏感。
//   ③ **条目数吻合**：解析出的条目数必须与 ① 的清单一致
//      ⇒ 防「有人加了一档却没写价」或「有人悄悄删了一档」。

import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const ROOT = path.resolve(HERE, '..', '..', '..', '..')
const TARGET_GO = path.join(ROOT, 'backend', 'internal', 'stt', 'target.go')
const src = fs.readFileSync(TARGET_GO, 'utf8')

/**
 * §579 依赖的那几格 + 两条 gateway 档。
 *
 * ⚠ 2026-10-17 属主拍板「补价表」：`minimax-asr-1.0` / `glm-asr` 这两条
 * gateway 档此前在册外（`checkRoster` 一直报红），现登记进册。
 * 它们与 external 组的 `asr-1.0` / `glm-asr-2512` **是两档不同的东西**：
 * 前者走网关目录（`target.go:238/239`），后者是外部直连档。
 * 其余 gateway 条目仍不在 §579 的论证链上，故意不钉。
 */
const GOLDEN = {
  'minimax-asr-1.0': 0.38,
  'glm-asr': 0.50,
  'qwen/qwen3-asr-0.6b': 0.012,
  'openai/whisper-large-v3-turbo': 0.012,
  'nvidia/nemotron-3.5-asr-streaming-multilingual-0.6b': 0.012,
  'qwen/qwen3-asr-1.7b': 0.027,
  'microsoft/mai-transcribe-2': 0.10,
  'gpt-4o-mini-transcribe': 0.18,
  'gpt-4o-transcribe': 0.36,
  'asr-1.0': 0.38,
  'glm-asr-2512': 0.50,
}

/**
 * 从 `Model: "x"` 起切块，在块内取 `USDPerHour`。
 *
 * ⛔⚠ 切块边界**不能**依赖行首缩进：这份文件有两种写法 ——
 *   · 紧凑单行（gateway 组，`:233`）：`{Model: "x", Group: ...}`
 *   · 多行展开（external 组，`:309`）：`\t\t\tModel: "x", Group: ...`
 * 第一版前瞻写成 `\n\s*Model:` ⇒ 紧凑行前面的 `{` 不是空白 ⇒
 * **5 条 gateway 被吞成 1 块**，实测只解析出 10 块（应为 14）。
 * ⇒ 锚点只认 `Model:\s*"`，不认它在不在行首。
 */
function parseCatalog(text) {
  const out = []
  const re = /Model:\s*"([^"]+)"([\s\S]*?)(?=Model:\s*"|\nfunc |\Z)/g
  let m
  while ((m = re.exec(text)) !== null) {
    const price = /USDPerHour:\s*([0-9.]+)/.exec(m[2])
    out.push({ model: m[1], usd: price ? Number(price[1]) : null, block: m[2] })
  }
  return out
}

const catalog = parseCatalog(src)
const priced = new Map(catalog.filter((e) => e.usd !== null).map((e) => [e.model, e.usd]))

/** 源码文本 → 有价 map（负控复用同一条路径）。 */
function priceMap(text) {
  return new Map(parseCatalog(text).filter((e) => e.usd !== null).map((e) => [e.model, e.usd]))
}

/**
 * 锚点出现次数（§581：负控必须先证锚点**唯一**）。
 * ⚠ `String.replace` 只换第一处 —— 锚点不唯一时，负控会静默地改到别的地方去，
 *   而它自己照样绿 ⇒ 验的东西漂移了却没人知道。
 *
 * ⛔⚠ **needle 不能带尾随标点**（§581 阳性对照实测）：
 *   第一版写的是 `'USDPerHour: 0.50,'`（带逗号）⇒ 数的是「带逗号的出现几次」，
 *   而真正的风险是「**这个价格值**出现几次」。Go 里字段后面未必跟逗号 ——
 *   gateway 那组就是**紧凑写法** `USDPerHour: 0.50}`。
 *   阳性对照故意插进一条 `USDPerHour: 0.50}` ⇒ 第一版断言仍绿 ⇒ 唯一性检查没生效。
 *   ⇒ needle 一律**不含**任何尾随标点。
 */
function countOccurrences(hay, needle) {
  let n = 0
  for (let i = hay.indexOf(needle); i >= 0; i = hay.indexOf(needle, i + needle.length)) n++
  return n
}

// ── 三条判据的检查体（抽成纯函数，好让负控**真的去调用判据**）────────────
//
// ⛔⚠ 第一版是「判据里写断言、负控里另抄一遍比较」—— 看着有负控，
//   其实负控验的是**负控自己那行 `!==`**，不是判据。
//   那样改判据的实现时负控照样绿 ⇒ 一个有牙也没有的负控。
//   ⇒ 现在判据与负控走同一条代码路径（负控一律用 `assert.throws` 确认它抛）。

/** 判据①：§579 依赖的 9 条价逐一钉住。抛 ⇒ 红。 */
function checkGolden(priceOf) {
  for (const [model, want] of Object.entries(GOLDEN)) {
    assert.equal(priceOf(model), want,
      `${model} 的价变成 ${priceOf(model)}，期望 ${want}。` +
      '⚠ §579 的「中文专用 ASR 贵约 42 倍」这个结论整个架在这张表上；' +
      '改价请连同该结论一起复核（换算汇率由表内「元/分钟」备注隐含给出）。')
  }
}

/** 判据②：写了「元/分钟」的条目，其美元价必须落在合理汇率带内。抛 ⇒ 红。 */
function checkFxSelfConsistent(entries) {
  let checked = 0
  for (const e of entries) {
    YUAN_PER_MIN_RE.lastIndex = 0
    const hit = YUAN_PER_MIN_RE.exec(e.block)
    if (!hit) continue
    checked++
    const yuanPerHour = Number(hit[1]) * 60
    assert.ok(e.usd !== null && e.usd > 0, `${e.model} 写了元/分钟却没有 USDPerHour`)
    const implied = yuanPerHour / e.usd
    assert.ok(implied >= 6.5 && implied <= 7.5,
      `${e.model}：注释说 ${hit[1]} 元/分钟（= ${yuanPerHour} 元/小时），` +
      `而价表写 $${e.usd}/小时 ⇒ 隐含汇率 ${implied.toFixed(2)}，落在 6.5–7.5 之外。` +
      '（价格或注释被改过，两者已不同步。）')
  }
  assert.ok(checked >= 1, '一条「元/分钟」都没解析到 ⇒ 判据② 恒绿，先修解析')
  return checked
}

/** 判据③：增删任何一档都必须被看见。抛 ⇒ 红。 */
function checkRoster(priceMapOf) {
  const registered = Object.keys(GOLDEN).filter((k) => priceMapOf.has(k))
  assert.equal(registered.length, Object.keys(GOLDEN).length,
    `在册条目 ${registered.length} ≠ golden ${Object.keys(GOLDEN).length}：` +
    `被删的是 ${Object.keys(GOLDEN).filter((k) => !priceMapOf.has(k)).join(', ') || '(有重名)'}`)

  // ⛔⚠ 第一版这里写的是 `... || priced.size > GOLDEN.length` ——
  //   那个逃生舱让整条 assert **恒真**（此刻 priced.size 恰等于 9，
  //   将来加到 10 条它照样放行）⇒ 等于没门。判据必须比承诺更强那一档。
  const unknown = [...priceMapOf.keys()].filter((k) => !(k in GOLDEN))
  assert.equal(unknown.length, 0,
    `多出了不在册的档：${unknown.join(' / ')}` +
    `（${unknown.map((k) => `$${priceMapOf.get(k)}/h`).join(' / ')}）` +
    ' ⇒ §579 的「可达集里最便宜的是谁」需要重算，并把它的价补进 GOLDEN')
}

// 元/分钟 → USDPerHour 的自洽带（刻意用带而不是点，见文件头判据②）
const YUAN_PER_MIN_RE = /([0-9.]+)\s*元\/分钟/g

describe('§580 · ASR 价表（§579 的承重结构）', () => {
  it('量具自证：两种写法都要被解析到', () => {
    // ⚠ 这里只验**解析器的形态能力**，不验「每一条都在」——
    //   完整性是判据③ 的活。两边都写就会有一条在对方触发时**说错原因**：
    //   删掉一档时本条会红在「asr-1.0 没解析出自己的价 ⇒ 边界又写坏了」，
    //   而实际是那一档被删了。⭐ 一条会撒谎的失败消息比没有门更糟。
    // 紧凑单行的 gateway 组（第一版被吞成一个块 ⇒ 只解析出 10 块）
    for (const m of ['glm-asr', 'minimax-asr-1.0', 'gpt-audio-mini', 'gpt-audio']) {
      assert.ok(catalog.some((e) => e.model === m), `紧凑单行的 ${m} 没被解析出来 ⇒ 切块边界写坏了`)
    }
    // 多行展开的 external 组：只要还有一条能取到自己的价，就证明这类块切对了。
    // 「某一档被删」由判据③ 报（本条若也管它，就会把删除误报成解析器坏了）。
    const expanded = catalog.filter((e) => Object.keys(GOLDEN).includes(e.model))
    assert.ok(expanded.length >= 1, '一条多行展开的条目都没解析到 ⇒ 切块边界写坏了')
    assert.ok(expanded.every((e) => e.usd !== null),
      `展开写法没能从块里取到 USDPerHour：${expanded.filter((e) => e.usd === null).map((e) => e.model).join(', ')}` +
      ' ⇒ 切块边界写坏了')
  })

  it('★ 判据①：§579 依赖的 9 条价逐一钉住', () => {
    checkGolden((m) => priced.get(m))
  })

  it('★ 判据②：注释里写了「元/分钟」的条目，其美元价必须落在合理汇率带内', () => {
    checkFxSelfConsistent(catalog)
  })

  it('★ 判据③：增删任何一档都必须被看见', () => {
    checkRoster(priced)
  })

  // ── 负控：三条判据必须真的会红（否则上面是装饰）────────────────────────
  //    每条都走**与判据同一个函数**，靠 assert.throws 确认它抛。
  //
  // ⭐⭐⭐ **「变了」≠「变成了我要的那个」（§581）**
  //   第一版只写 `assert.notEqual(mutated, src, '负控样本没插进去')` ——
  //   那只证明源码**变了**。但 `String.replace` 只换**第一处**匹配：
  //   将来若价表里出现第二处 `USDPerHour: 0.50,`，负控① 会照样绿，
  //   而它此后验的其实是**另一档**的价格 —— **没有任何东西会报出这个漂移**。
  //   ⇒ 现在每条负控都先验三件事：① 锚点唯一 ② 插进去的**就是**指定的那处
  //     ③ 变异**落在正确的档上**。（= 仓里既有的第三种负控形态「先证前提」。）

  it('★★ 负控①：只把价格改小但不破坏升序（M28 的形态）⇒ 判据① 必须红', () => {
    // ⚠ 2026-10-15（§738.3）：守卫的探针**必须与 replace 的锚点是同一个串**。
    //   原先这里数的是更宽的 `USDPerHour: 0.50`（无逗号），而替换的是
    //   `USDPerHour: 0.50,`（带逗号）。价表新增 gateway 组 `glm-asr` 之后，
    //   前者 2 处、后者仍只有 1 处 ⇒ **守卫报了漂移，而这条负控其实没漂移**
    //   （那一条以 `},` 结尾，replace 仍只命中 `glm-asr-2512`）。
    //   ⇒ 那是一条**假红**。数「比替换锚点更宽的子串」会把不相关的同值条目算进来。
    assert.equal(countOccurrences(src, 'USDPerHour: 0.50,'), 1,
      '负控前提失败：`USDPerHour: 0.50,`（替换锚点本身）在表里出现多处，replace 只会改第一处 ⇒ 本负控已漂移')
    const mutated = src.replace('USDPerHour: 0.50,', 'USDPerHour: 0.45,')
    assert.ok(mutated.includes('USDPerHour: 0.45,'), '变异没按预期插进去')
    assert.equal(priceMap(mutated).get('glm-asr-2512'), 0.45,
      '负控前提失败：变异没落在 glm-asr-2512 这一档上')
    const m = priceMap(mutated)
    assert.throws(() => checkGolden((k) => m.get(k)), /glm-asr-2512/,
      '负控失效：改价后判据① 仍不抛 ⇒ 判据① 是装饰')
  })

  it('★★ 负控②：只改注释里的元/分钟（M28 的另一半）⇒ 判据② 必须红', () => {
    assert.equal(countOccurrences(src, '0.06 元/分钟'), 1,
      '负控前提失败：`0.06 元/分钟` 在表里出现多处，replace 只会改第一处 ⇒ 本负控已漂移')
    const mutated = src.replace('0.06 元/分钟', '0.006 元/分钟')
    const hit = parseCatalog(mutated).find((e) => e.model === 'glm-asr-2512')
    assert.ok(hit && hit.block.includes('0.006 元/分钟'),
      '负控前提失败：变异没落在 glm-asr-2512 那一档的注释上')
    assert.throws(() => checkFxSelfConsistent(parseCatalog(mutated)), /隐含汇率/,
      '负控失效：改注释后判据② 仍不抛 ⇒ 判据② 是装饰')
  })

  it('★★ 负控③：整档被删掉 ⇒ 判据③ 必须红（「少」那一半）', () => {
    const start = src.indexOf('Model: "asr-1.0"')
    const end = src.indexOf('Model: "glm-asr-2512"')
    assert.ok(start > 0 && end > start, '负控前提失败：找不到 asr-1.0 的块边界')
    const mutated = src.slice(0, start) + src.slice(end)
    const m = priceMap(mutated)
    assert.equal(m.has('asr-1.0'), false, '负控前提失败：asr-1.0 没能删掉')
    assert.throws(() => checkRoster(m), /被删的是 asr-1\.0/,
      '负控失效：删掉一档后判据③ 仍不抛 ⇒ 判据③ 的「少」那一半是装饰')
  })

  it('★★ 负控④：新加一档没写进 GOLDEN ⇒ 判据③ 必须红（「多」那一半）', () => {
    // ⚠ 负控③④ 是**同一条判据的两个方向**（§572 的教训：两个性质必须各配一条，
    //   只配一条的话另一半就是装饰）。负控③ 只验「少」，这条验「多」。
    // ⚠ 2026-10-15（§738.3）：锚点原先是**整行含 Note 全文**的字面量，
    //   上游把 Note 从「网关列了但可能 503 no_provider」改成
    //   「网关列了但实测 503 no_provider（2026-10-06）」⇒ 锚点 0 命中、负控瘫着。
    //   改为**按行取**那一行：Note 怎么改写都不影响它，而「唯一性」仍单独断言。
    const anchorLine = src.split('\n').find((l) => l.includes('Model: "gpt-audio-mini"'))
    assert.ok(anchorLine, '负控前提失败：价表里找不到 gpt-audio-mini 那一行 ⇒ 本负控已漂移')
    assert.equal(countOccurrences(src, anchorLine), 1,
      '负控前提失败：插入锚点在表里出现多处 ⇒ 本负控已漂移')
    const mutated = src.replace(anchorLine, anchorLine + '\n\t\t{Model: "brand-new-asr", Group: "external", USDPerHour: 0.01},')
    const m = priceMap(mutated)
    assert.equal(m.get('brand-new-asr'), 0.01,
      '负控前提失败：新档没落进解析结果（或落在了别的档上）')
    assert.throws(() => checkRoster(m), /brand-new-asr/,
      '负控失效：新增一档后判据③ 仍不抛 ⇒ 判据③ 的「多」那一半是装饰')
  })
})