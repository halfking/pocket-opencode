// refine-rejected-notice.test.ts — 门禁：精校结果被「拦下」与「解析失败」必须**分开说**。
//
// 2026-10-07（§35 后端配套）。服务端新增了一条回落路径：
//
//   guardRefineResult 检出「精校结果里混进了原始转写中不存在的参会人姓名」
//   ⇒ 整体丢弃精校结果、回落原文，并置 refine_rejected=true。
//
// 它**同时**也置 refine_fallback=true（确实回落到原文了）。
// 于是前端若只认 refine_fallback，就会把这条报成
//
//   「精翻未生效（云端返回无法解析）」
//
// 而实际上 JSON 解析得好好的 —— 真正的原因（模型把人名换成了名单里的人）
// 无人知晓，用户会照着错原因去查网络。
//
// ⇒ **桶名是对外承诺**：后端两个共用一个 flag，前端必须把它们拆成两句话。
//
// 三层断言（与 refine-fallback-notice.test.ts 同一套路）：
//   A 纯文案层 —— buildRefineNotice 对四种输入的输出（行为断言，可直接调用）
//   B API 层   —— normalizeRefine 认得 refine_rejected 并独立透出
//   C 调用点   —— useSessionLiveRecord 真的用了 buildRefineNotice
//   D 反向     —— ⚠ 这里**故意没有**自带的变异测试。
//
//	原想在本文件里写一段「变异体自检」，写完发现它只会做字符串比对、
//	**根本没在变异体上重跑断言** —— 那种测试恒绿，是「门禁说自己有牙」
//	而不是真的有牙（同 §35 的第 5、6 处教训）。
//	⇒ 变异验证改为外部脚本真跑：scripts/refine-notice-mutation.mjs
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import path from 'node:path'
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'

import { buildRefineNotice, refineStatusFor } from '../refine-outcome-notice.ts'

// ⚠⚠ 路径基准是**本文件所在目录**（src/features/sessions/__tests__/）。
//
// 这一条我在**本文件里已经犯过两次**：第一次三个 ENOENT，
// 第二次（补 E 层时）又错三处。每一次的表现都是「判据红」，
// 而红的原因是路径写错、不是产品坏了 —— 判据一红就去看产品，
// 会把量具的错当成产品的错。
//
//   __tests__ → sessions → features → src → frontend → 仓库根
//
// 所以：本目录内用 ../，features 内用 ../../，src 内用 ../../../，
// frontend 内用 ../../../../，仓库根用 ../../../../../。
const here = path.dirname(fileURLToPath(import.meta.url))
const read = (p: string) => readFileSync(path.join(here, p), 'utf8')

describe('精校被拦下的文案', () => {
  // ── A 纯文案层：行为断言，直接调函数 ──
  it('「被拦下」必须说名单污染，不能说云端无法解析', () => {
    const n = buildRefineNotice({ fromFallback: true, rejected: true })
    assert.equal(n.kind, 'info', '被拦下不是成功')
    assert.match(n.text, /拦下/, `实际文案：${n.text}`)
    assert.match(n.text, /参会人名单/, `实际文案：${n.text}`)
    assert.doesNotMatch(
      n.text,
      /无法解析/,
      '被拦下不是解析失败 —— 这句会让用户去查错的方向',
    )
  })

  it('rejected 为 false 时不得出现「拦下」二字', () => {
    const n = buildRefineNotice({ fromFallback: true, rejected: false })
    assert.match(n.text, /无法解析/)
    assert.doesNotMatch(n.text, /拦下/, '没被拦下却这么说 = 又一次静默改口')
  })

  it('解析失败与被拦下是两句不同的话（后端共用一个 flag）', () => {
    const parseFail = buildRefineNotice({ fromFallback: true })
    const rejected = buildRefineNotice({ fromFallback: true, rejected: true })
    assert.notEqual(parseFail.text, rejected.text, '两条路径被合并成同一句')
  })

  it('正常精校对成功文案', () => {
    assert.equal(buildRefineNotice({}).text, '录音已结束，精翻完成')
    assert.equal(buildRefineNotice({ adopted: true }).text, '录音已结束，已整段重转并精翻')
  })

  it('重转有分片失败要说清段数', () => {
    const n = buildRefineNotice({ adopted: true, failedSegments: 3 })
    assert.match(n.text, /3 段未转出/, `实际文案：${n.text}`)
  })

  // ── B API 层：normalizeRefine 必须独立透出 rejected ──
  it('normalizeRefine 认得 refine_rejected 与 camelCase 变体', () => {
    const api = read('../../../api/meetings.ts')
    const init = api.slice(api.indexOf('export function normalizeRefine'))
    assert.ok(
      /raw\.refine_rejected\s*===\s*true/.test(init),
      'normalizeRefine 没认 snake_case 的 refine_rejected',
    )
    assert.ok(
      /raw\.refineRejected\s*===\s*true/.test(init),
      'normalizeRefine 没认 camelCase 的 refineRejected',
    )
    assert.ok(
      // ⚠ §908/D77：`rejected: true` 后面现在还跟着一个可选的 `rejectedReason`
      //   （返回对象多带一个字段），所以这里放行 `,` 与 `}` 两种收尾。
      //   **语义没动**：算出了 rejected 就必须写进返回值。
      /rejected\s*\?\s*\{\s*rejected:\s*true\s*[,}]/.test(init),
      'normalizeRefine 算出了 rejected 却没有写进返回值 ⇒ 信号在最后一跳丢掉',
    )
    assert.ok(
      /rejected\?:\s*boolean/.test(api),
      'RefineResult 类型里没有 rejected 字段',
    )
  })

  // ── C 调用点：真的用了纯文案层 ──
  //
  // ⚠ §49：收尾编排抽进共享模块后，「kind → toast」的映射**拆到了两处**：
  //   · 模块（meeting-recording-finalize.ts）：算出 notice 并把
  //     `(notice.kind, notice.text + tail)` 交给注入的 notify
  //   · 调用点（两条录音路径各一份）：把 kind 映射成 toast.info/success/warning
  // 判据跟着拆开守，**不变量一条不少**：kind 必须来自 notice、
  // 文本必须源自 notice.text、两个调用点都必须真的做映射。
  it('收尾模块把 notice 的 kind 与 text 交给调用点，而不是自己写死分支', () => {
    const src = read('../../meetings/meeting-recording-finalize.ts')
    assert.ok(
      /buildRefineNotice\(\{[\s\S]*?rejected:\s*result\.rejected/.test(src),
      '调用点没有把 result.rejected 传给 buildRefineNotice —— ' +
        '信号在最后一跳丢掉，前端会退回「无法解析」那句话',
    )
    // kind 必须原样传出去，不能在模块里被别的东西改判。
    assert.ok(
      /notify\(\s*notice\.kind\s*,/.test(src),
      'notify 没收到 notice.kind —— 降级提示会被当成成功提示弹给用户',
    )
    // 文本必须源自 notice.text（入库结果以 tail 追加，不替换）。
    assert.ok(
      /notice\.text\s*\+/.test(src),
      '弹出去的文本没有源自 notice.text —— 信号在最后一跳又丢了',
    )
    // 旧的硬编码文案必须已经不在了（否则两套话并存）。
    assert.doesNotMatch(
      src,
      /精翻未生效（云端返回无法解析）/,
      '收尾模块还留着旧的硬编码降级文案 —— 与纯文案层并存会出现两个口径',
    )
  })

  it('★ 两条录音路径都必须把 kind 映射到正确的 toast', () => {
    // 这一条是 §49 新增的：§48 修好后只有随手记一条路径，
    // 会议详情页那条（**主入口**）连 refine 都没有 —— 判据若只盯一个文件就会漏。
    for (const site of [
      '../../sessions/useSessionLiveRecord.ts',
      '../../meetings/MeetingDetailView.vue',
    ]) {
      const c = read(site)
      assert.ok(
        /if \(kind === 'success'\)[\s\S]{0,200}?toast\.success\(/.test(c),
        `${site} 没有把 success 映射到 toast.success`,
      )
      assert.ok(
        /if \(kind === 'warning'\)[\s\S]{0,200}?toast\.warning\(/.test(c),
        `${site} 没有把 warning 映射到 toast.warning`,
      )
      assert.ok(
        /toast\.info\(text\)/.test(c),
        `${site} 没有 info 分支 —— 其余 kind 会被吞掉，用户什么都看不到`,
      )
    }
  })

  it('★ 负控：把判据从「文本源自 notice.text」拆出来单独验', () => {
    const decoupled = `
      const notice = buildRefineNotice({ rejected: result.rejected })
      notify(notice.kind, '精翻完成')
    `
    assert.ok(
      !/notice\.text\s*\+/.test(decoupled),
      '负控样本构造错了：与 notice.text 脱钩的写法不该命中',
    )
  })

  it('★ 负控：kind 被改判时必须转红（映射成了 success 之外的别的东西）', () => {
    const hardcoded = `
      notify('success', notice.text + tail)
    `
    assert.ok(
      !/notify\(\s*notice\.kind\s*,/.test(hardcoded),
      '负控样本构造错了：写死 kind 不该命中',
    )
  })

})

// ── E 状态层：降级时不得落 'refined' ──
//
// ⚠ 第四次「桶名与承诺不符」，这次藏在**状态机**里：
//   录音流的 toast 已改诚实，但会议列表徽章 `refined → '已精翻'`
//   与三个落点的 `status: 'refined'` 都还是无条件的。
describe('降级时的会议状态', () => {
  it('refineStatusFor：两条降级路径都不得返回 refined', () => {
    assert.equal(refineStatusFor({}), 'refined')
    assert.equal(refineStatusFor({ adopted: true }), 'refined')
    assert.equal(
      refineStatusFor({ fromFallback: true }),
      'refine-failed',
      '解析失败也算没精成',
    )
    assert.equal(
      refineStatusFor({ fromFallback: true, rejected: true }),
      'refine-failed',
    )
    assert.equal(refineStatusFor({ rejected: true }), 'refine-failed')
  })

  it('MeetingStatus 类型与 statusText 都必须有 refine-failed', () => {
    const store = read('../../meetings/meetings-store.ts')
    // ⚠⚠ 锚点必须钉在**类型字面量那一行**，不能用 `'refine-failed' in 文件`。
    //   本会话已栽过多次「源码扫描被注释满足」—— 而这一条正是我自己踩的：
    //   我在类型上方写了「'refine-failed' = 精校降级或被拦下……」这行说明，
    //   于是把类型里的值删掉（变异 N12），断言**照样绿**。
    assert.ok(
      /type MeetingStatus = [^\n]*'refine-failed'/.test(store),
      "MeetingStatus 类型里没有 refine-failed —— 落不进去这个值" +
        '（注意：锚点要钉在类型字面量上，不能靠注释里出现这个词）',
    )
    const list = read('../../meetings/meeting-list.ts')
    assert.ok(
      /'refine-failed':\s*'精翻未生效'/.test(list),
      'statusText 没有「精翻未生效」—— 会回落到裸字符串 refine-failed 显示给用户',
    )
    // ⚠ 不得复用 'completed'：那会让失败与正常完成共用一个桶
    assert.doesNotMatch(
      list,
      /'refine-failed':\s*'已完成'/,
      '把「精翻未生效」写成「已完成」= 把失败藏进成功桶',
    )
  })

  it('三个落点都按判定取值，不得写死 refined', () => {
    const ingest = read('../../meetings/meeting-ingest.ts')
    // 出现两次（本地 updateMeeting + 云端 syncMeeting），两处都得换
    const hardcoded = ingest.match(/status:\s*'refined'/g) ?? []
    assert.equal(
      hardcoded.length,
      0,
      `meeting-ingest.ts 还有 ${hardcoded.length} 处写死 status: 'refined'`,
    )
    assert.ok(
      /refineStatusFor\(\{/.test(ingest),
      'meeting-ingest.ts 没调 refineStatusFor',
    )

    const live = read('../../meetings/meeting-recording-finalize.ts')
    assert.equal(
      (live.match(/status:\s*'refined'/g) ?? []).length,
      0,
      "useSessionLiveRecord.ts 还有写死的 status: 'refined'",
    )
    assert.ok(/refineStatusFor\(/.test(live), 'useSessionLiveRecord.ts 没调 refineStatusFor')
  })

  it('后端 finalizeMeetingRefine 也不得无条件 refined', () => {
    const go = readFileSync(
      path.join(here, '../../../../../backend/internal/server/server_meeting_ingest.go'),
      'utf8',
    )
    assert.ok(
      /refineDegraded\(result\)/.test(go),
      '后端 finalizeMeetingRefine 没有按 refineDegraded 判定 —— ' +
        'meetingStore 里缓存的 status 会覆盖前端本地的判断，列表读的是它',
    )
    assert.ok(
      /func refineDegraded\(/.test(go),
      'refineDegraded 没定义',
    )
  })
})

/* ═══════════════════════════════════════════════════════════════════════════
 * §908 · D77：`refine_rejected` 有**两个**成因，文案必须对得上真实原因
 * ═══════════════════════════════════════════════════════════════════════════
 * §907 给长会议加了「缩写」护栏（`refineShrinkage`），它**复用了同一个标志位**
 * `refine_rejected` ⇒ 于是前端会对**每一场长会议**说
 *
 *     「检测到与参会人名单不符的人名」
 *
 * 而真实原因是「转写太长，模型回显不完」—— 名单从来没问题。
 * 这正是本文件头写的「**桶名是对外承诺**」：两个成因共用一个 flag，
 * 前端不拆开就会用**一句错的话解释一次正确的行为**。
 * ⇒ 服务端补 `refine_rejected_reason`，一路透传到文案层。
 */
describe('§908 · D77：被拦下的原因必须对上文案', () => {
  it('★★★ shrinkage（缩写）不得说成「参会人名单不符」', () => {
    const n = buildRefineNotice({ fromFallback: true, rejected: true, rejectedReason: 'shrinkage' })
    assert.equal(n.kind, 'info', '降级不是成功')
    assert.match(n.text, /太长|回显/, `实际文案：${n.text}`)
    assert.doesNotMatch(
      n.text,
      /参会人名单/,
      '⚠ 这是 §907 打破的那条纪律：用户会被指向一个根本不存在的错因',
    )
    assert.doesNotMatch(n.text, /无法解析/, '缩写不是解析失败')
  })

  it('★★★ participant_leak 仍然必须说名单污染（不能被新分支吞掉）', () => {
    const n = buildRefineNotice({ fromFallback: true, rejected: true, rejectedReason: 'participant_leak' })
    assert.match(n.text, /参会人名单/, `实际文案：${n.text}`)
    assert.doesNotMatch(n.text, /太长|回显/, '这条不是缩写')
  })

  it('★★ 不给原因时保持既有行为（向后兼容，旧服务端没有这个字段）', () => {
    const n = buildRefineNotice({ fromFallback: true, rejected: true })
    assert.match(n.text, /参会人名单/, `实际文案：${n.text}`)
  })

  it('★★★ 原因必须一路透传到文案层（接线：缺了就退回错的那句）', () => {
    const api = read('../../../api/meetings.ts')
    const init = api.slice(api.indexOf('export function normalizeRefine'))
    assert.match(
      init,
      /raw\.refine_rejected_reason\s*\?\?\s*raw\.refineRejectedReason/,
      'normalizeRefine 没读 refine_rejected_reason ⇒ 原因在 API 层就丢了',
    )
    assert.match(
      init,
      /rejectedReason/,
      'normalizeRefine 没有把原因写进返回值',
    )
    const callSite = read('../../meetings/meeting-recording-finalize.ts')
    assert.match(
      callSite,
      /rejectedReason:\s*result\.rejectedReason/,
      '⚠ 收尾调用点没有把原因传给 buildRefineNotice ⇒ 长会议仍然会被说成名单污染',
    )
  })

  it('★★ RefineResult 类型必须声明这个字段（否则调用点拿不到）', () => {
    const api = read('../../../api/meetings.ts')
    assert.match(api, /rejectedReason\?:\s*'participant_leak'\s*\|\s*'shrinkage'/)
  })
})
