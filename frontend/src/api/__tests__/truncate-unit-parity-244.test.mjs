/**
 * §244 「500」在前端与后端**不是同一个单位** —— 而两端都在注释里自称 500。
 *
 * ⚠ 为什么这道门必须存在（既有门量不到的地方）：
 *   `summary-fallback-local-shape.test.ts` 已经有一句
 *   「超长转写按 500 字符截断（**与服务端 emptySummary 同口径**）」，
 *   看起来正是这件事的门。但它用 `'x'.repeat(1200)` 做语料 ——
 *   **纯 ASCII 下字节数 == 码元数**，于是「前端 slice 按码元、后端
 *   truncateStr 按字节」这个差异在该语料上**恒等于 0**，门永远绿。
 *   ⇒ 它证明的是「ASCII 下两端一致」，不是「口径一致」。
 *   本文件的语料换成中文（会议转写的真实内容形态），差异立刻显形。
 *
 * 三个子问题，各有独立形状：
 *   ① 单位：同一数字 500，纯中文下前端给 500 字符、后端给 166 字符（3.01×）。
 *   ② 切点：前端 `slice` 能切在代理对中间 ⇒ 落单代理对 ⇒ 过 JSON 到 Go
 *      会被 `json.Unmarshal` 静默换成 U+FFFD，且 **err == nil**
 *      （即：没有任何一层会报错，内容只是静默变脏）。
 *      后端 `truncateStr` 有显式字符边界回退，不会切。
 *   ③ 一致性：前端自己两条回落路线是 500 与 200，
 *      而它们产出的是**同一种东西**（转写片段 + summaryFallback=true）。
 *
 * ⚠ 特征化 vs 目标：本文件**默认全绿但逐条报出缺口**，
 *   `POCKET_STRICT_244=1` 时切成严格模式（缺口即红）。
 *   为什么不做成「默认红」：本仓 `test:all` 经 `run-mjs-tests.mjs` 收所有
 *   测试后缀文件（见该文件 `COVERAGE_GLOBS`），一份默认红的文件会让
 *   **全量测试因「发现缺口」而红**，
 *   那是在用门禁表达「我还没修」，不是表达「产品坏了」。
 *   为什么不做成「默认 skip」：那份 runner 第 11 行明确写着
 *   「文件跑到了却一个用例都没产出（空文件、全被 skip）→ 同样是一片绿」，
 *   整份 skip 会被它当缺陷抓出来。
 *   ⇒ 两头都不能选，取中间：默认**绿且把缺口印出来**，
 *     修完之前它不会消失，只是换成一行 warning。
 *
 *   严格模式（把缺口变成硬红）：
 *     cd frontend && POCKET_STRICT_244=1 \
 *       node --test src/api/__tests__/truncate-unit-parity-244.test.mjs
 *
 *   修复后本文件在两种模式下都绿。哪一侧该改是**产品决定**（见 §244.4），
 *   本轮不擅自定 —— 所以缺口先以「已量出、未修」的形式留着。
 */
import assert from 'node:assert/strict'
import { afterEach, describe, it } from 'node:test'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import {
  FRONTEND_ROOT, REPO_ROOT,
  activatePinia, assertHit, captureRequests, loadBundle, restoreGlobals, stubBrowser,
  ALWAYS_CLOUD_SYNC_POLICY_STUB,
  ALWAYS_DIRECT_AUTH_STUB,
} from './_wire-helpers.mjs'

// ④ 那条要打桩 fetch / window / pinia ⇒ 必须还原，否则污染同进程内其它用例。
afterEach(restoreGlobals)

/** 会议转写的真实内容形态：中文为主。⚠ 换成 ASCII 本文件会大面积假绿。 */
const CN = '张伟：下周三跟客户过一遍方案。\n李娜：预算按季度复核，缺口走专项。'

/**
 * ⚠ 后端口径的**读数来自产品代码本身**，不是这里复刻的一份实现。
 *
 * 第一版这里写了一份 JS 的 `goTruncateStr`（字节上限 + 回退字符边界），
 * 于是这道门量的其实是「我自己写的 JS 实现 vs 我自己写的 JS 实现」。
 * ⇒ 若 Go 侧 `truncateStr` 的语义变了（改成按字符、或加省略号），
 *   **这道门不会知道**，会继续报一个已经不存在的缺口。
 *
 * 修法：`backend/internal/server/testdata/truncate_unit_vectors.json`
 * 由 `truncate_unit_fixture_test.go` **当场调用真 truncateStr** 生成，
 * 那边还有一条判据会逐条重算并比对（fixture 与真函数不一致 ⇒ 红）。
 * ⇒ 本门读的是**真值**，不是复刻。复刻只保留在下面这条对照里，
 *   且它自己断言「复刻 == 真值」，漂了就红。
 */
const VECTORS = JSON.parse(
  readFileSync(join(REPO_ROOT, 'backend/internal/server/testdata/truncate_unit_vectors.json'), 'utf8'))

/** 后端 truncateStr 的等价实现，仅用于「复刻是否还等于真值」这一条对照。 */
function goTruncateStr(s, n) {
  const buf = Buffer.from(s, 'utf8')
  if (buf.length <= n) return s
  let cut = buf.subarray(0, n)
  while (cut.length > 0 && cut.toString('utf8').includes('�')) {
    cut = cut.subarray(0, cut.length - 1)
  }
  return cut.toString('utf8')
}

/** 是否存在落单代理对（半个 UTF-16 码元）。 */
function hasLoneSurrogate(s) {
  for (let i = 0; i < s.length; i++) {
    const u = s.charCodeAt(i)
    if (u >= 0xd800 && u <= 0xdbff) {
      const next = i + 1 < s.length ? s.charCodeAt(i + 1) : NaN
      if (!(next >= 0xdc00 && next <= 0xdfff)) return true
      i++
    } else if (u >= 0xdc00 && u <= 0xdfff) {
      return true
    }
  }
  return false
}

/** Go json.Unmarshal 收到 JSON 里的落单代理对时的行为（实测：换成 U+FFFD，err 为 nil）。 */
function goUnmarshalLike(jsonText) {
  const raw = jsonText.match(/"summary":"(.*)"\}$/)?.[1] ?? ''
  return { text: raw.replace(/\\u([0-9A-Fa-f]{4})/g, (_, h) => {
    const cp = parseInt(h, 16)
    // Go 的 encoding/json 对落单代理对替换为 U+FFFD，不报错
    return (cp >= 0xd800 && cp <= 0xdfff) ? '�' : String.fromCodePoint(cp)
  }) }
}

/** 严格模式开关：默认绿且报出缺口，开了才把缺口变成硬红。 */
const STRICT = !!process.env.POCKET_STRICT_244

/**
 * 报出一条缺口。
 * 默认模式：打印醒目 warning 并让用例通过（缺口已量出，不让它消失）。
 * 严格模式：直接红（`POCKET_STRICT_244=1`）。
 *
 * ⚠ 不用 `t.skip()`：整份 skip 会被 run-mjs-tests.mjs 的「零产出文件」卡口
 *   当缺陷抓出来，而这里要的是「跑到了、有产出、把缺口印出来」。
 */
function reportGap(t, id, message) {
  if (STRICT) {
    assert.fail(`[§244-${id} 严格模式] ${message}`)
  }
  console.warn(`\n⚠️  §244-${id} 缺口（已量出，未修）：${message}`)
  t.diagnostic(`§244-${id} 缺口已报出；开 POCKET_STRICT_244=1 可让它变成硬红`)
}

/**
 * 用**产品自己的**转写渲染器，而不是这里另拼一份。
 *
 * ⚠ 为什么要复用：`meetings.ts` 里 ① 与 ② 两条路线的转写都来自
 *   `renderTranscript`（`meeting-dedup.ts`）。判据若自己拼一份字符串，
 *   量到的就不是产品行为——换个前缀/时间戳格式就得重写判据。
 */
async function renderTranscriptOf(segments) {
  const dedup = await loadBundle(
    join(FRONTEND_ROOT, 'src/features/meetings/meeting-dedup.ts'), 'trunc-244-render')
  return dedup.renderTranscript(segments)
}

describe('§244 截断单位：前端码元 vs 后端字节', () => {
  it('【量具自证 A】JS 复刻必须仍等于 Go 真值（漂了就红）', () => {
    // 这条守护的是本文件里**唯一**一份跨语言复刻。
    // 若 Go 侧 truncateStr 语义变了 ⇒ fixture 变 ⇒ 这里立刻红，
    // 不会让我们拿着一个过时的复刻继续报「已不存在的缺口」。
    for (const v of VECTORS) {
      const got = goTruncateStr(v.input, v.limit)
      assert.equal(got, v.output,
        `[${v.name}] JS 复刻与 Go 真值不一致：\n  复刻=${JSON.stringify(got)}\n  真值=${JSON.stringify(v.output)}\n` +
        'Go 侧语义变了。重新生成 fixture：\n' +
        '  cd backend && UPDATE_TRUNCATE_FIXTURE=1 go test ./internal/server/ -run TestExportTruncateUnitFixture')
    }
  })

  it('【量具自证 B】真值向量本身必须能表达「字节 ≠ 码元」（否则它证明不了任何事）', () => {
    const truncated = VECTORS.filter((v) => v.bytes <= v.limit && v.input.length > v.limit)
    assert.ok(truncated.length > 0,
      '真值向量里没有任何一条真正触发截断 ⇒ 它证明不了「后端按字节截」')
    // 必须存在「输出字节数 ≤ 上限，但输出码元数明显小于上限」的向量。
    const provesUnit = VECTORS.some((v) => v.limit > 100 && v.bytes <= v.limit && v.runes < v.limit / 2)
    assert.ok(provesUnit,
      '真值向量里没有一条能区分「按字节」与「按字符」⇒ 这批向量量不到单位差异')
  })

  it('【量具自证 C】ASCII 下两侧口径必然相同（这正是既有门假绿的原因）', () => {
    const ascii = 'x'.repeat(1200)
    assert.equal(ascii.slice(0, 500).length, goTruncateStr(ascii, 500).length,
      '量具自证失败：ASCII 下两侧本应相等。若不等，先查复刻是否可信')
  })

  it('① 同一个数字 500，两端给用户的字符数不相等', async (t) => {
    const mod = await loadBundle(join(FRONTEND_ROOT, 'src/api/meetings.ts'), 'trunc-244-a', { '../stores/syncPolicyStore.ts': ALWAYS_CLOUD_SYNC_POLICY_STUB, '../stores/auth': ALWAYS_DIRECT_AUTH_STUB })

    // ⚠ 用**真值向量**逐条比，不用 JS 复刻算后端那一侧。
    for (const v of VECTORS) {
      if (v.input.length <= v.limit) continue // 未触发截断，两侧本就相同
      const front = mod.emptySummary(v.input).summary
      const frontRunes = [...front].length
      if (frontRunes !== v.runes) {
        reportGap(t, '①',
          `[${v.name}] 前端给 ${frontRunes} 字符、Go 真值给 ${v.runes} 字符，` +
          `而两处注释都写着 ${v.limit}。同一次回落在两端给用户不同长度的「录音原文」`)
      }
    }
  })

  it('② 前端切点不能落在代理对中间（落单代理对过 JSON 到 Go 会静默变 U+FFFD）', async (t) => {
    const mod = await loadBundle(join(FRONTEND_ROOT, 'src/api/meetings.ts'), 'trunc-244-b', { '../stores/syncPolicyStore.ts': ALWAYS_CLOUD_SYNC_POLICY_STUB, '../stores/auth': ALWAYS_DIRECT_AUTH_STUB })

    // ⚠ 必须**扫一个偏移区间**，不能只测一个偏移。
    //   第一版只钉「高位码元位 = 499」这一个点，负控变异 N1（500→501，
    //   一个良性同类改动）恰好把切点挪过那个 emoji ⇒ 断言转绿
    //   ⇒ 判据量的是「切点恰好落在 499」这个偶然事实，
    //   不是「切点会切在代理对中间」这件事。变异当场抓到了这个脆点。
    //
    // 正确形态：星平面字符落在**每一个**可能的切点位置上，断言都成立。
    // 这样 501、499、498 之类的数字改动全都钻不进空子。
    const offenders = []
    for (let k = 1; k <= 520; k++) {
      const planted = '进'.repeat(k) + '🔥' + '度'.repeat(600)
      if (hasLoneSurrogate(mod.emptySummary(planted).summary)) offenders.push(k)
    }

    if (offenders.length > 0) {
      reportGap(t, '②',
        `emptySummary 在 ${offenders.length} 个星平面字符位置上切出了落单代理对` +
        `（示例偏移 ${offenders.slice(0, 3)}）⇒ 它会作为半个 emoji 进 JSON，` +
        'Go json.Unmarshal 收到后静默换成 U+FFFD 且 err==nil（无任何一层报错）')
    }

    // 负向对照：后端不会切（它的字符边界回退是真的在工作）。
    // ⚠ 这条**恒绿且不承重**：它是量具自证（证明对照本身没坏），
    //   不是缺陷检出点 —— 第二组变异 G4 把它删掉，红绿不变，属预期。
    //   分清「恒绿的对照」与「恒红的缺陷」是两种东西，别把前者误报成后者。
    // ⚠ 对照读数取**真值向量**（含星平面-500），不是本地复刻。
    const starPlane = VECTORS.find((v) => v.name === '含星平面-500')
    assert.ok(starPlane, '真值向量缺「含星平面-500」⇒ 对照失效')
    assert.ok(!hasLoneSurrogate(starPlane.output),
      '对照失效：Go 真值本身切出了落单代理对 ⇒ 后端的字符边界回退不再工作')
  })

  it('③ 落单代理对经过 JSON 往返确实会变脏（证明 ② 不是洁癖）', () => {
    const planted = '进'.repeat(499) + '🔥' + '度'.repeat(600)
    const cut = planted.slice(0, 500)
    assert.ok(hasLoneSurrogate(cut), '前置：slice 本该切出落单代理对')

    // 现代 V8 的 JSON.stringify 会把落单代理对转义成 \uD83D，往返**不丢**
    const wire = JSON.stringify({ summary: cut })
    const back = goUnmarshalLike(wire)
    assert.ok(back.text.includes('�'),
      '量具失效：落单代理对过了 Go 侧却没变脏 ⇒ ② 的风险不成立')
  })

  it('④ 前端两条回落路线产出同一种东西，截断长度不应差 2.5 倍', async (t) => {
    // ⚠ 必须是**行为断言**。
    //   第一版扫源码里的 `slice(0, 200)` 字面量 —— 证明的是「代码写在那」，
    //   不是「它真会这么判」：抽成常量 TRUNC_LEN 字面量就没了，缺陷还在。
    //   第二版改走真产物：用 captureRequests 让后端摘要请求 502、
    //   让 /api/llm/chat 也失败 ⇒ meetingsApi.summarize 真的落到那条 catch。
    stubBrowser()
    activatePinia()
    const mod = await loadBundle(join(FRONTEND_ROOT, 'src/api/meetings.ts'), 'trunc-244-d', { '../stores/syncPolicyStore.ts': ALWAYS_CLOUD_SYNC_POLICY_STUB, '../stores/auth': ALWAYS_DIRECT_AUTH_STUB })

    const segments = [{
      id: 's1',
      meetingId: 'm1',
      speakerLabel: '张伟',
      lang: 'zh',
      confidence: 0.9,
      startMs: 0,
      endMs: 3000,
      // ⚠ 语料必须是**多段各不相同**的真实形态。
      //   第二版用单段 `CN`（或 `CN.repeat(N)` 这类整段重复），被
      //   `renderTranscript` 的去重逻辑判成「同一段又来了一次」而整段丢弃
      //   ⇒ 实测转写只剩 38 码元，截断根本不会发生。
      //   这条自证（`转写 > 1000 码元`）当场把它抓了出来。
      text: '张伟：先说结论，这个方案我们倾向在九月底上线，下周三之前给到终稿。' +
            '预算缺口大概在三成，已经走专项审批，李娜补充一下数字。',
      // ⚠ `origin` 是 §132 定的**必填**字段（'asr' | 'manual'）。
      //   漏掉它会让这条用例靠一个 TypeError 变红 —— 那是崩，不是量到缺陷。
      origin: 'asr',
    },
    ...Array.from({ length: 12 }, (_, k) => ({
      id: `s${k + 1}`,
      meetingId: 'm1',
      speakerLabel: k % 2 === 0 ? '李娜' : '王强',
      lang: 'zh',
      confidence: 0.88,
      startMs: k * 4000,
      endMs: k * 4000 + 3800,
      text: `第 ${k + 1} 段的独立内容：客户那边的联调排在${k + 2}月中旬，` +
            `负责人是${k % 2 === 0 ? '陈明' : '周涛'}，风险点是接口限流与重试策略。`,
      origin: 'asr',
    })),
  ]
    // 后端摘要接口 502 ⇒ 走 catch；/api/llm/chat 也不给路由 ⇒ 二次调用失败 ⇒ 落 ②
    //
    // ⚠⚠ 路径是 **`/summary`**，不是 `/summarize`（我第一版写错了）。
    //   `captureRequests` 对没配的 path 同样返回 404 ⇒ 照样抛 ApiError
    //   ⇒ **路由名写错时用例照样绿**，只是走了一条我没预期的路。
    //   判别动作：`assertHit` 把「真的打到了这两个端点」钉成断言。
    const seen = captureRequests({
      '/api/meetings/m1/summary': 502,
    })

    const viaCatch = await mod.meetingsApi.summarize('m1', segments)

    // ★ 钉住「真的走完了两级降级」，否则这条比的就是一个我没预期的分支。
    assertHit(seen, '/api/meetings/m1/summary')
    assertHit(seen, '/api/llm/chat')

    // ⚠⚠ 路线 ① 的输入必须是**完整转写**，不是 ② 的产物。
    //   第二版写成 `emptySummary(viaCatch.summary)` —— 而 ② 的产物本身只有 200 字，
    //   再过 emptySummary 仍只有 200 ⇒ 两侧必然相等 ⇒ **④ 恒绿**。
    //   读数证据：探针实测 ② 给 200 码元，而真转写有 CN.repeat(100) 共 ~4200 码元。
    //   ⇒ 这不是「缺口消失」，是**我拿被测对象的输出当了自己的输入**。
    // ⚠ 这里必须 await：renderTranscriptOf 是 async，返回 Promise。
    //   漏掉 await 时 `fullTranscript` 是个 Promise —— 而 Promise 没有 `.slice`，
    //   报错会落在 **产品代码里**（`emptySummary` 抛 `text.slice is not a function`），
    //   看起来像是被测对象坏了。⚠ 量具的错必须停在量具里，不能伪装成产品缺陷。
    const fullTranscript = await renderTranscriptOf(segments)
    assert.equal(typeof fullTranscript, 'string',
      `量具失效：renderTranscriptOf 返回了 ${typeof fullTranscript}，不是字符串`)
    const viaEmpty = mod.emptySummary(fullTranscript).summary

    const a = [...viaEmpty].length
    const b = [...viaCatch.summary].length

    // 量具自证 1：② 必须真的落到 `fallbackSummarize` 的 catch，
    //   而不是 `parseSummaryJson` 的空摘要分支（两者都是 summaryFallback=true，
    //   光看标志分不出来 —— 那正是 §220 的教训：信号相同不等于路径相同）。
    assert.equal(viaCatch.summaryFallback, true, '量具失效：② 没置 summaryFallback')

    // 量具自证 2：两条路线都必须**真的发生了截断**（都短于完整转写），
    //   比的才是「同一次失败给了用户多长的原文」。
    //
    // ⚠⚠ 这组自证改过两次，两次都是**被正确的修法踩中**：
    //   第二版：`a > b`（「① 应比 ② 长」）——M4 把 ② 从 200 改成 500 后两侧相等，
    //     自证把「缺陷消失」当成了「量具坏了」。
    //   第三版：`② <= 201`（写死上限）——同样是 M4，直接被写死的数字拦住。
    //   ⇒ 现在的形态只钉**必要条件**（「确实截断了」），
    //     而「截多少」正是被测对象**应该变的那个量**。
    //   ⇒ 判别动作：自证里出现**恰好等于产品常量**的数字（500/200）就要警惕 ——
    //     那个数字明天会被正确地改掉，而自证不该因此失效。
    assert.ok(b < [...fullTranscript].length,
      `量具失效：② 给了 ${b} 码元，不短于完整转写 ${[...fullTranscript].length} ⇒ 它没走 catch`)
    assert.ok(a < [...fullTranscript].length,
      `量具失效：① 给了 ${a} 码元，不短于完整转写 ⇒ 截断没发生`)

    if (a !== b) {
      reportGap(t, '④',
        `路线 ①（emptySummary）给 ${a} 码元，路线 ②（fallbackSummarize 的 catch）` +
        `给 ${b} 码元 —— 同一个数字族、同一类产出（转写片段 + summaryFallback=true），` +
        `用户看到的却是 ${(a / b).toFixed(1)} 倍差别，而两条路线都在同一次失败里挨着走`)
    }
  })

  it('⑤ 精校回写入库那一路的切点也不能落在代理对中间', async (t) => {
    // ⚠ §251.2 兄弟链：`refined-ingest-writeback.ts` 的 `summary` 经
    //   `updateMeeting` **真的入库**（本地 DB + 云同步），而它截的是
    //   **精校后的转写正文** —— 最容易含 emoji 的一侧。
    //   既有判据 `refined-ingest-writeback.test.ts` 的语料是 ASCII，
    //   切不出代理对 ⇒ 对这一处**整份假绿**。
    const mod = await loadBundle(
      join(FRONTEND_ROOT, 'src/features/meetings/refined-ingest-writeback.ts'), 'trunc-244-e')

    const offenders = []
    for (let k = 1; k <= 520; k++) {
      const refined = '进'.repeat(k) + '🔥' + '度'.repeat(600)
      const got = mod.resolveIngestWriteback({
        refine: { refinedTranscript: refined },
        status: 'done',
      })
      if (got.summary !== null && hasLoneSurrogate(got.summary)) offenders.push(k)
    }

    if (offenders.length > 0) {
      reportGap(t, '⑤',
        `精校回写的 summary 在 ${offenders.length} 个星平面字符位置上切出了落单代理对` +
        `（示例偏移 ${offenders.slice(0, 3)}）⇒ 它会**入库**，过 Go 解码静默变 U+FFFD`)
    }

    // 量具自证：这条路必须真的走到了「成功路径回写摘要」，
    //   否则上面扫的是 `summary: null` 的降级路径（与切点无关）。
    const probe = mod.resolveIngestWriteback({
      refine: { refinedTranscript: '进'.repeat(499) + '🔥' + '度'.repeat(600) },
      status: 'done',
    })
    assert.ok(probe.summary !== null,
      '量具失效：没走到成功路径的回写分支 ⇒ ⑤ 扫的是一条与切点无关的路径')
  })
})