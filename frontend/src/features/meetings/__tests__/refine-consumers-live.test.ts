// refine-consumers-live.test.ts — §46：三条「消费者真的活着」的门。
//
// 这三个门盯的是**同一类错误**：上一轮的 10 条接线门全绿，
// 但生产链路里三段关键代码从来没执行过。共同形状是——
//   · refine 返回的 `todos` 被丢弃（用户需求「时间点进日程」从未生效）
//   · `meta.participants` 没传 ⇒ 服务端 participantLeak 名单恒空
//   · 名单从 `meeting.participants` 取，随手记路径恒空 ⇒ 修正分支不可达
//
// 教训直接写进门里：**「读了某个字段」不等于「读到非空的值」**。
// 所以 A/B/C 三条一律用**生产形状的实参**跑真实函数，判据落在返回值上，
// 而不是「源码里出现过这个词」。
import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'
import { PINYIN_COMMON } from '../pinyin-common.ts'
import { deriveRoster, isPlaceholderSpeaker, repairRosterHomophones } from '../meeting-roster-homophone.ts'
import { balancedSpan } from '../../../__tests__/source-scan.mjs'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const SRC = path.resolve(HERE, '..', '..', '..')  // → frontend/src
const read = (rel: string) => fs.readFileSync(path.resolve(SRC, rel), 'utf8')
// 收尾编排的实现只有一个地方（§49 抽出的共享模块）；
// 两个录音入口是否真的调它，由本文件末尾的「接线」组守住。
const hook = read('features/meetings/meeting-recording-finalize.ts')

/**
 * 剥注释但**保持行结构与字符位置**（用等长空格替换）。
 * 剥得太狠会让下面按 indexOf 做的顺序判据量到错误的位置。
 */
const blank = (m: string) => m.replace(/[^\n]/g, ' ')
/** 剥注释（等长替换，保持下标可比）。源码判据一律走它 —— 注释里
 *  提到一个调用点就足以让顺序/存在性判据命中假位置（§49 实测踩到两次）。 */
const strip = (t: string) => t
  .replace(/\/\*[\s\S]*?\*\//g, blank)
  .split('\n').map((l) => { const i = l.indexOf('//'); return i < 0 ? l : l.slice(0, i) }).join('\n')
const code = strip(hook)

// ── 生产形状夹具 ────────────────────────────────────────────────────────────
// ★ 用真值而不是自拟：
//   · speakerLabel 的真名来自 §44/§45 夹具里出现过的「张伟」「李娜」
//   · 占位形态来自 speaker-diarization.ts:57 的 `说话人 ${nextId}`
//   · 同音错来自 §44 实测：SenseVoice 把「林岚」听成「林兰」
const PROD_SEGS = [
  { speakerLabel: '张伟', text: '我跟林兰还有李娜下周开个客户评审会' },
  { speakerLabel: '说话人 2', text: '对比表我来准备' },
]

describe('A：精校结果的行动项必须真的落到待办与日程', () => {
  it('★ stop() 必须消费 result（ingestMeetingArtifacts 是唯一会建提醒的出口）', () => {
    assert.ok(/ingestMeetingArtifacts\s*\(/.test(code),
      'stop() 不调用 ingestMeetingArtifacts ⇒ refine.todos 被丢弃，'
      + '录完会记了、时间点也说了，日历上什么也没有')
  })

  it('★ 负控：这段代码曾经就是缺失的（把调用摘掉，判据必须转红）', () => {
    // 变异形状 = §46 修复前的真实代码：refine 结果只取 3 个字段。
    const beforeFix = `
      const result = await meetingsApi.refine(id, refineSegments)
      await updateMeeting(id, {
        refinedTranscript: result.refinedTranscript,
        status: refineStatusFor({ fromFallback: result.fromFallback, rejected: result.rejected }),
      })
    `
    assert.ok(!/ingestMeetingArtifacts\s*\(/.test(beforeFix),
      '负控样本构造错了：修复前的代码不该出现 ingestMeetingArtifacts')
  })

  it('入库失败不得把已经成功的精校说成「未生效」（必须单独兜）', () => {
    // 判据落在**结构**上：ingest 调用点必须在 refine 的 catch 之前，
    // 且 catch 段里没有把 ingest 的失败冒泡出去。
    const iRefine = code.indexOf('meetingsApi.refine(')
    const iIngest = code.indexOf('ingestMeetingArtifacts(')
    // ⚠⚠ §955：**只改指向，不改语义**。
    //   原锚点是「**全文件第一个** `} catch (e) {`」⇒ 而 §955 在收尾模块的**前面**
    //   （`readNativeCaptureSnapshot`）加了一个 `catch (e)`（取原生采集窗口失败时的兜底）
    //   ⇒ 那个锚点命中的是**新加的**那个 catch ⇒ 判据开始报「ingest 不在 refine 与 catch 之间」
    //   ⇒ 而那不是真的：ingest 仍然夹在 refine 与**它自己的** catch 之间。
    //   ⇒ ⇒ 锚点改成「**从 refine 往后**找那个 catch」⇒ 语义完全不变
    //   （要守的仍然是「ingest 在 refine 的 try 块内、catch 之前」）。
    const iCatch = code.indexOf('} catch (e) {', iRefine)
    assert.ok(iRefine > 0 && iIngest > 0 && iCatch > 0, '三个锚点都要能找到')
    assert.ok(iIngest > iRefine && iIngest < iCatch,
      'ingest 必须夹在 refine 成功与外层 catch 之间 —— 抛到外层会让精校明明成功却被报成失败')
  })

  it('★ 负控：把 ingest 挪到 catch **之后**（= 精校失败会被说成入库失败）必须被同一条判据抓住', () => {
    // ⚠ 这条负控是 §955 改锚点时配套加的：锚点从「全文件第一个 catch」改成
    //   「从 refine 往后找 catch」⇒ 必须证明**新锚点仍然有牙**。
    const sample = `
      const result = await meetingsApi.refine(id, refineSegments)
    } catch (e) {
      notify('warning', 'refine failed')
    }
      await ingestMeetingArtifacts(fresh, result)
    `
    const iRefine = sample.indexOf('meetingsApi.refine(')
    const iIngest = sample.indexOf('ingestMeetingArtifacts(')
    const iCatch = sample.indexOf('} catch (e) {', iRefine)
    const ok = iIngest > iRefine && iIngest < iCatch
    assert.equal(ok, false,
      '负控样本构造错了：ingest 落在 catch 之后时，同一条判据必须判红（否则新锚点没牙）')
  })
})

describe('B：refine 必须带 meta.participants（否则服务端兜底是空转）', () => {
  it('★ refine 调用必须传第 4 个参数 meta，且含 participants', () => {
    const call = code.slice(code.indexOf('meetingsApi.refine('), code.indexOf('meetingsApi.refine(') + 320)
    assert.ok(/participants\s*:/.test(call),
      'refine 调用没传 participants ⇒ participantLeak 名单恒空 ⇒ '
      + 'guardRefineResult 永不触发 ⇒ refine_rejected / refine-failed 永不出现')
  })

  it('★ 负控：不传 meta 的调用形态必须被判据抓住', () => {
    const noMeta = `const result = await meetingsApi.refine(id, refineSegments)`
    assert.ok(!/participants\s*:/.test(noMeta), '负控样本构造错了')
  })

  it('传的是推导出来的名单，不是又一个恒空的字段', () => {
    const call = code.slice(code.indexOf('meetingsApi.refine('), code.indexOf('meetingsApi.refine(') + 320)
    assert.ok(/participants\s*:\s*roster/.test(call),
      'meta.participants 必须接推导出的 roster，而不是再读一次 participants 字段')
  })
})

/**
 * 取 `meetingsApi.refine(` 第 4 个实参（meta 对象字面量）的**整个原文**。
 *
 * ⚠ 为什么不沿用上面那个「往后截 320 个字符」：
 *   本仓两条既有判据用固定窗口量 `participants`，那是因为它离调用点很近
 *   （偏移 ~70）。而 `topic` 的偏移是 **208**，中间隔着 4 行 §138.3 的注释 ——
 *   而剥注释是**等长空格替换**，行数与字符位置都不变
 *   ⇒ 窗口大小对**注释有多长**敏感、对**代码长多长**不敏感。
 *   今天绿、明天有人把注释从 4 行写成 6 行就红，而代码一个字没动。
 *   ⇒ 改成按大括号配平取整个对象体：长度无关。
 *
 * ⚠⚠ 还有一层：本文件里 `meetingsApi.refine(` 源码中**出现两次**，
 *   第一次在头部注释里（`实测（§48.8/§49）：② 路径**全程没有任何 refine 调用点**`）。
 *   所以这个抽取器**必须**喂 `code`（已剥注释），不能喂 `hook`（原文）。
 */
function refineMetaArg(src: string): string {
  const at = src.indexOf('meetingsApi.refine(')
  assert.ok(at > 0, '解析不到 meetingsApi.refine( —— 判据失效，先修判据')
  const open = src.indexOf('{', at)
  assert.ok(open > 0 && open - at < 200,
    `refine 的第 4 个实参不是紧邻的对象字面量（偏移 ${open - at}）—— 判据的前提变了，先看判据`)
  let depth = 0
  for (let i = open; i < src.length; i++) {
    if (src[i] === '{') depth++
    else if (src[i] === '}') {
      depth--
      if (depth === 0) return src.slice(open + 1, i)
    }
  }
  throw new Error('refine 的 meta 实参括号不配平 —— 判据失效，先修判据')
}

// ★ §143：这条是「§82 接线值没人守」的**第五次**复现，而这次是我自己刚写完
//   §143 的四处改动之后才发现的 —— 我给**两端**（前端 meta 类型 / Go json tag）
//   都配了门，却漏了**唯一那个把主题交出去的调用点**。
//   ⇒ 后果不是「少一个字段」，而是 §143 整条修复在生产里**空转**：
//     字段两头都在、中间没人接线，且没有任何门会响。
//   与 §88/§89/§82 同形，只是这次缺的不是值，是**字段本身**。
describe('B2：refine 必须带 meta.topic（主题是专有名词的唯一来源）', () => {
  it('★ 调用点必须把 meeting.topic 传进 meta', () => {
    assert.match(refineMetaArg(code), /\btopic\s*:\s*meeting\?\.topic/,
      'refine 调用没传 topic ⇒ 主题里的专有名词永远到不了精校 ⇒ §143 整条修复在生产里空转')
  })

  it('★ topic 必须走 `?? undefined`（与相邻三个字段同口径，缺字段时整体不出现在 JSON 里）', () => {
    assert.match(refineMetaArg(code), /\btopic\s*:\s*meeting\?\.topic\s*\?\?\s*undefined/,
      'topic 的取值形态与 title/location/participants 不一致')
  })

  it('★ 负控：摘掉 topic 的形态必须被上面那两条抓住', () => {
    const noTopic = refineMetaArg(code.replace(/topic\s*:\s*meeting\?\.topic[^\n]*/, ''))
    assert.ok(!/\btopic\s*:/.test(noTopic), '负控样本构造失败：topic 没被摘掉')
    assert.doesNotMatch(noTopic, /\btopic\s*:\s*meeting\?\.topic/,
      '判据没咬住：调用点没有 topic 却仍然放行 —— B2 组是恒真的')
  })

  it('量具自证：抽取的是**第 4 个实参**，不是文件里随便一个花括号对象', () => {
    // 正控：样本里 topic 就在第 4 实参内，必须被读到。
    const ok = `const r = await meetingsApi.refine(id, segs, ['en'], { title: t, topic: meeting?.topic }, sig)`
    assert.match(refineMetaArg(ok), /\btopic\b/, '抽取器连正控样本里的第 4 实参都没读到')
    // 负控：topic 挪到**第 6 个实参** ⇒ 抽取器必须看不见它。
    //   这条防的是「用第一个 `{…}` 或最后一对花括号」这类错误实现 ——
    //   那会让判据绿在一个根本没被发出去的字段上。
    const moved = `const r = await meetingsApi.refine(id, segs, ['en'], { title: t }, sig, { topic: meeting?.topic })`
    assert.doesNotMatch(refineMetaArg(moved), /\btopic\b/,
      '抽取器把第 6 个实参当成了 meta ⇒ 本组会绿在错的对象上')
    // 自证前提：本仓真实调用点的偏移在窗口守卫之内。
    assert.ok(refineMetaArg(code).includes('participants'),
      '真实调用点的 meta 里连 participants 都没有 ⇒ 前置判据 B 的前提变了，先看判据')
  })
})

describe('C：名单必须在生产形状下**非空**（这条才是能抓住死分支的门）', () => {
  it('★ 生产形状分段（说话人已起真名）推导出非空名单', () => {
    const roster = deriveRoster({
      participants: [],                                   // 随手记路径实测恒空
      speakerLabels: PROD_SEGS.map((s) => s.speakerLabel),
    })
    assert.deepEqual(roster, ['张伟'],
      `生产形状下名单应含「张伟」，实测 ${JSON.stringify(roster)} —— `
      + '为空则 repairRosterHomophones 分支不可达（同音纠正整块是死代码）')
  })

  it('★ 端到端：名单非空 ⇒ 同音错真的被改掉', () => {
    const roster = deriveRoster({ participants: [], speakerLabels: PROD_SEGS.map((s) => s.speakerLabel) })
    assert.ok(roster.length > 0, '前置：名单为空，后面无从谈起')
    // 只验「张伟」能命中不是重点——重点是同音字确实被替换。
    const r = repairRosterHomophones('我跟林兰还有张伟开会', [...roster, '林岚'], PINYIN_COMMON)
    assert.equal(r.text, '我跟林岚还有张伟开会', `实测未纠正：${r.text}`)
    assert.equal(r.fixes.length, 1, `应恰好 1 条修正，实测 ${r.fixes.length}`)
  })

  it('★ 占位标签必须被剔掉，否则「表不全则整体放弃」每次都触发', () => {
    // 反例：把「说话人 2」原样放进名单 ⇒ pinyinOf 返回 null ⇒ 整体放弃，
    // 于是「张伟」这条真名也一并不生效。
    assert.equal(isPlaceholderSpeaker('说话人 2'), true)
    assert.equal(isPlaceholderSpeaker('说话人'), true)
    assert.equal(isPlaceholderSpeaker('Speaker 3'), true)
    assert.equal(isPlaceholderSpeaker('  '), true)
    assert.equal(isPlaceholderSpeaker('张伟'), false)

    const withPlaceholder = repairRosterHomophones(
      '我跟林兰开会', ['说话人 2', '林岚'], PINYIN_COMMON,
    )
    assert.deepEqual(withPlaceholder.fixes, [],
      '占位名进了名单，修正确实整个被关掉了（这正是必须过滤的原因）')

    const filtered = deriveRoster({ speakerLabels: ['说话人 2', '张伟'] })
    assert.deepEqual(filtered, ['张伟'], '占位标签没被过滤掉')
  })

  it('★ 负控：若不过滤占位符，上面那条「整体放弃」判据就会转红', () => {
    const rosterKept = ['说话人 2', '林岚']
    const r = repairRosterHomophones('我跟林兰开会', rosterKept, PINYIN_COMMON)
    assert.deepEqual(r.fixes, [], '负控样本应表现为「修正被整体放弃」')
  })

  it('名单为空时不做任何修正（安全方向，不得硬猜）', () => {
    assert.deepEqual(deriveRoster({ participants: [], speakerLabels: [] }), [])
    const r = repairRosterHomophones('我跟林兰开会', [], PINYIN_COMMON)
    assert.equal(r.text, '我跟林兰开会')
    assert.deepEqual(r.fixes, [])
  })

  it('两个来源取并集且去重（手填名单 + 说话人标签都要算）', () => {
    const roster = deriveRoster({
      participants: ['王总', '张伟'],
      speakerLabels: ['张伟', '说话人 1', '林岚'],
    })
    assert.deepEqual(roster, ['王总', '张伟', '林岚'], `实测 ${JSON.stringify(roster)}`)
  })

  // ★★★ 这条是 M3 变异逼出来的（见 /tmp/opstt/mutate-46.mjs）：
  //   上面两条 C 组判据都在**纯函数层**验 deriveRoster 的行为，
  //   而 M3 变异改的是 **hook 的接线**（把 deriveRoster 换回只读 participants）
  //   ⇒ 纯函数没变，门全绿。**变异仍绿的根因是「变异够不到门要看的东西」**，
  //   不是判据恒真。补一条盯接线的门。
  // ⚠ §52：名单推导已从 finalizeRecording 移进纯决策层 meeting-refine-plan.ts
  //   （拆出来是因为 §45/§48/§49/§50 四轮的 bug 全在「决策」里，而那一层
  //   此前在 node 里 import 都进不去）。判据跟着改指向。
  const planMod = strip(read('features/meetings/meeting-refine-plan.ts'))

  it('★ 决策层的名单必须由 deriveRoster 产出，不能退回只读 participants', () => {
    assert.ok(/const roster = deriveRoster\(/.test(planMod),
      'roster 不是 deriveRoster 产出的 —— §46 的死分支（只读 meeting.participants，随手记路径恒空）')
  })

  it('★ 决策层必须把分段说话人标签喂进 deriveRoster（光传 participants 不够）', () => {
    // ⚠ §428：按括号配平取**完整**实参区，不是「取够 400 个字符」。
    //   上限窗口把覆盖率交给了排版 —— 肯定断言偏小是假红，否定断言偏小是假绿。
    const call = balancedSpan(planMod, 'deriveRoster({')
    assert.ok(call, '找不到 deriveRoster 调用')
    assert.ok(/speakerLabels\s*:/.test(call.span),
      'deriveRoster 没收到 speakerLabels —— 随手记路径的名单恒空，同音纠正不可达')
  })

  it('★★ 名单必须同时看 baseSegments 与原始 segments（§52 行为门逼出来的）', () => {
    // 归因失效时 baseSegments 塌成一条 speakerLabel=null 的分段；
    // 只从它取名单 ⇒ 名单为空 ⇒ 同音纠正静默不跑。
    const call = balancedSpan(planMod, 'deriveRoster({')
    assert.ok(call, '找不到 deriveRoster 调用')
    assert.ok(/speakerLabels\s*:\s*\[/.test(call.span) && /baseSegments\.map/.test(call.span) && /\.\.\.segments\.map/.test(call.span),
      '名单只看 baseSegments —— 归因失效时真名会被一起丢掉（变异 P1 实测）')
  })

  it('★ 负控：M3 形态（退回只读 participants）必须被判据抓住', () => {
    const mutant = `
      const meeting = await getMeeting(id)
      const roster = meeting?.participants ?? []
    `
    assert.ok(!/const roster = deriveRoster\(/.test(mutant),
      '负控样本构造错了：退回形态不该命中 deriveRoster')
  })
})

// ── D：两条录音路径都必须走收尾（§49 新增）────────────────────────────────
//
// ★ 这组是为「§48 的形状」补的牙：上一轮 A/B/C 三条修复全部只落在**随手记**
//   那条路径上，而产品里其实有**两条**录音路径：
//     ① 随手记会话内录音  useSessionLiveRecord.stop()
//     ② 会议详情页录音    MeetingDetailView.onMicToggle()   ← **主入口**
//   ② 是会议列表「开始录音」落地的位置（push `?record=1` → onMounted 自动开录），
//   而它在 §49 之前**全程没有任何 refine 调用点**：录完只有原始分段转写，
//   没有精校、没有行动项、没有日程提醒。
//   ⇒ 「修好了一条、另一条照旧」必须有一道门钉住，否则下一轮还会重演。
describe('D：两条录音路径都必须走同一份收尾编排', () => {
  const CALL_SITES = [
    { rel: 'features/sessions/useSessionLiveRecord.ts', label: '随手记会话内录音' },
    { rel: 'features/meetings/MeetingDetailView.vue', label: '会议详情页录音（主入口）' },
  ]

  it('★ 两个调用点都必须调用 finalizeRecording', () => {
    for (const { rel, label } of CALL_SITES) {
      const c = strip(read(rel))
      assert.ok(/finalizeRecording\s*\(/.test(c),
        `${label}（${rel}）没有调用 finalizeRecording —— `
        + '该路径录完不会精校、不建待办、不进日程')
    }
  })

  it('★ 收尾编排的实现只有一处（防止有人复制一份）', () => {
    const impl = read('features/meetings/meeting-recording-finalize.ts')
    assert.ok(/export async function finalizeRecording/.test(impl), '共享收尾模块不在预期位置')
    assert.ok(/meetingsApi\.refine\(/.test(impl), '共享收尾模块里没有 refine 调用点')
    // 两个调用点都不得自己直接调 refine —— 那是「复制一份」的开始。
    for (const { rel, label } of CALL_SITES) {
      const c = strip(read(rel))
      assert.ok(!/meetingsApi\.refine\(/.test(c),
        `${label} 自己调了 meetingsApi.refine —— 收尾编排出现第二份实现，`
        + '下一次修一条漏一条就是从这里开始的')
    }
  })

  it('★ 两个调用点都必须从**持久层**拿分段（不是录音态读数）', () => {
    // ⚠ 第一版这条判据写成了 `A && B || /await load\(\)/`，
    //   等价于 `(A && B) || C` —— 而两个调用点**必然**含 `await load()`，
    //   ⇒ C 恒真 ⇒ 整条判据恒真，W6 变异（详情页改传 segments.value）跑出来仍绿。
    //   这是我自己新写的判据当场犯的「恒真判据」，记录下来别再犯。
    // ⇒ 现在精确判定「传给 finalizeRecording 的第二参数是不是录音态读数」。
    for (const { rel, label } of CALL_SITES) {
      const c = strip(read(rel))
      // ⚠ §428：按括号配平取**完整**实参区，不是「取够 600 个字符」。
      //   这条是**否定断言** —— 上限窗口一旦把它关心的位置切到窗口外，
      //   它就在自己看不见的范围里宣布通过（假绿）。
      //   实测当天余量只有 236 字符（实参区 332 / 364），
      //   所以那一刻还是绿的；但没有门保证明天也绿 —— 加个选项对象就可能翻面。
      const call = balancedSpan(c, 'finalizeRecording(')
      assert.ok(call, `${label} 找不到 finalizeRecording 调用`)
      assert.ok(
        !/recorder\s*\.\s*segments\s*\.value/.test(call.span)
        && !/(^|[^.\w])segments\s*\.\s*value\s*[,)]/.test(call.span),
        `${label} 传给 finalizeRecording 的分段来自录音态 —— `
        + '§49：stop() 之后 recorder 的 segments 恒为 []，说话人归因会整体失效',
      )
    }
  })

  it('★ 负控：把分段来源换成录音态读数，判据必须抓住', () => {
    const bad = strip(`
      await finalizeRecording(
        meetingId.value,
        segments.value,
        0,
        { notify: () => {} },
      )
    `)
    assert.ok(
      /(^|[^.\w])segments\s*\.\s*value\s*[,)]/.test(bad),
      '负控样本构造错了：录音态读数形态不该逃过判据',
    )
  })

  it('★ 负控：只有一个调用点接线时，这道门必须报红', () => {
    const onlyOne = `
      async function stop() {
        await finalizeRecording(id, segs, 0, { notify: () => {} })
      }
    `
    assert.ok(/finalizeRecording\s*\(/.test(onlyOne), '负控样本构造错了')
    // 反向：把详情页那一处摘掉后，判定逻辑必须不再认它
    const detail = "async function onMicToggle() { await stop(); await load() }"
    assert.ok(!/finalizeRecording\s*\(/.test(detail),
      '负控样本构造错了：未接线的详情页不该命中')
  })
})

// ── E：整段重转必须先转码（§50）──────────────────────────────────────────
//
// ★ 这是本组存在的唯一理由：**没有它，「整段重转」在真机上是死的。**
//
//   真机录音产物是 webm/opus（`VadSegmenter.stop()` 产 `audio/webm`）。
//   两条独立的证据（都不是推断）：
//     ① 真网关实测：POST {gw}/audio/transcriptions
//        webm → HTTP 400 `audio format "webm" is not supported by the
//        chat-audio bridge (supported: mp3, wav)`；同一段音频 wav → HTTP 200。
//     ② 后端生产函数实测：`stt.SplitWAV(webm)` → `splittable=false, 0 段`
//        ⇒ 整段只发一次、只回 1 条回执 ⇒ `attributeFullTranscript` 返回 null
//        ⇒ §40 的说话人归因整块不生效。
//   而 `refetchFullTranscript` 的第 3 个参数（`toWav`）**在生产上没人传**。
//
//   ⚠ 随手记语音那条路径早接了（`recordingRuntime` 用 `audioDecoder.takeFull()`），
//     只有会议录音这条路没接 —— 又是一次「修好一条、另一条照旧」。
describe('E：整段重转必须先把 webm 转成 wav', () => {
  const impl = strip(read('features/meetings/meeting-recording-finalize.ts'))

  it('★ finalizeRecording 必须给 refetchFullTranscript 传 toWav', () => {
    const at = impl.indexOf('refetchFullTranscript(')
    assert.ok(at > 0, '找不到 refetchFullTranscript 调用')
    // ⚠ §428：按圆括号配平取**完整**实参区（同本文件 F 组那条否定断言的理由）。
    const call = balancedSpan(impl, 'refetchFullTranscript(')
    assert.ok(call, '找不到 refetchFullTranscript 调用')
    // ⚠ 这里必须同时接受 `{ toWav: … }` 与 `{ toWav }` 两种写法：
    //   §805 把转码实参提成了 `createMeasuringToWav(...)` 包出来的局部 const，
    //   调用点因此用了花括号简写。两者语义完全相同，而只认 `toWav:`
    //   会把「等价简写」判成「没传」—— 那是**判据贴在语法形状上**，不是行为。
    //   `}` 分支要求 `toWav` 是实参对象的**最后一个键**，简写才合法，因此不会放宽。
    assert.ok(/toWav\s*[:,}]/.test(call.span),
      '整段重转没有传 toWav ⇒ 真机上传的是 webm ⇒ 网关 400 / 后端不可切 ⇒ '
      + '整段重转与说话人归因全部失效（§50 实测）')
  })

  // ★ 转码实现在 §51 被提升成 utils/wav-encode.ts 的正式导出 `toGatewayWavBytes`
  //   （原先是 finalize 模块里的私有函数 ⇒ 只能被源码扫描看见，跑不起来）。
  //   于是「它到底做不做转换」现在有**行为门**在 audio-wire-contract.test.ts 里，
  //   这里只守「收尾模块用的就是它、且它不是恒等函数」。
  const wavEnc = strip(read('utils/wav-encode.ts'))

  it('★ toWav 必须真的做容器转换，不能是恒等函数', () => {
    assert.ok(/export async function toGatewayWavBytes/.test(wavEnc), 'toGatewayWavBytes 不在 wav-encode.ts 里')
    // ⚠ §511：区间切片必须先证「结束锚在起点之后」，否则 slice() 返回空串，
    //   断言会报 actual: ''（本仓已踩过一次，见 recording-audio-transcode.test.mjs）。
    //   这里之所以还安全，是因为 `wavEnc` 是**剥过注释**的 —— 头注释里也提到了这个名字。
    //   ⇒ 这条 guard 把「依赖 strip」这件事变成显式的，而不是靠运气。
    const wStart = wavEnc.indexOf('toGatewayWavBytes')
    const wEnd = wStart + 400
    assert.ok(wStart >= 0 && wEnd > wStart, `toGatewayWavBytes 的区间坏了（start=${wStart}, end=${wEnd}）`)
    const body = wavEnc.slice(wStart, wEnd)
    assert.ok(/needsWavTranscode\s*\(/.test(body), '没有按容器类型判断是否需要转码')
    assert.ok(/encodeWav16kMono\s*\(/.test(body), '没有调用真正的转码实现')
    assert.ok(
      !/return\s+await\s+blob\.arrayBuffer\(\)/.test(body),
      'toGatewayWavBytes 看着像恒等函数 —— 那等于没转码',
    )
  })

  // ★ 这条是 X2 变异逼出来的：把 finalize 模块里的 defaultToWav 换成恒等函数，
  //   上一版 E 组**全绿** —— 因为它只查了 utils/wav-encode.ts 里的
  //   toGatewayWavBytes 做得对不对，**没查收尾模块的 defaultToWav 有没有真的委托它**。
  //   ⇒ 同一个断面上，「实现对不对」与「用没用上」是两件事，必须分别断言。
  it('★ 收尾模块的 defaultToWav 必须真的委托 toGatewayWavBytes', () => {
    const at = impl.indexOf('const defaultToWav')
    assert.ok(at > 0, '收尾模块里找不到 defaultToWav')
    const decl = impl.slice(at, at + 220)
    assert.ok(/toGatewayWavBytes\s*\(/.test(decl),
      'defaultToWav 没有委托 toGatewayWavBytes —— 换成恒等函数也不会有任何门报红（X2 变异实测）')
    assert.ok(!/blob\.arrayBuffer\(\)\s*$/m.test(decl.split('\n')[0]),
      'defaultToWav 看着像恒等函数')
  })

  it('★ 转码实现必须来自仓内既有工具（不自己造第二套）', () => {
    assert.ok(
      /import[^;]*toGatewayWavBytes[^;]*utils\/wav-encode/.test(read('features/meetings/meeting-recording-finalize.ts')),
      '转码没有复用 utils/wav-encode.ts —— 会出现第二套编码实现',
    )
  })

  it('★ 负控：去掉 toWav 的形态必须被判据抓住', () => {
    const noTranscode = strip(`
      const finalText = await refetchFullTranscript(
        meetingId,
        deps.transcribeFull ?? defaultTranscribeFull,
      )
    `)
    assert.ok(!/toWav\s*:/.test(noTranscode), '负控样本构造错了：不传 toWav 不该命中')
  })

  it('★ 负控：恒等 toWav 也必须被抓住（形式在、语义没转）', () => {
    const identity = strip(`
      const defaultToWav = async (blob: Blob) => blob.arrayBuffer()
    `)
    assert.ok(
      !/needsWavTranscode\s*\(/.test(identity) && !/encodeWav16kMono\s*\(/.test(identity),
      '负控样本构造错了：恒等实现不该命中真实转码判据',
    )
  })
})

/**
 * ★§89：`ingestMeetingArtifacts` 的两个实参与四个上报值此前都没人守。
 *
 * 注入实测（§89.1）：把它们逐个换空，**四发 0 报红**：
 *   · 把 `result` 换成 `{ ...result, todos: [], structuredMinutes:{nextMeeting:null} }`
 *     ⇒ refine 返回的行动项与下次会议**静默不到库**
 *   · `fresh` 换成 null
 *   · `outcome.todosCreated` / `outcome.dueUnresolved` 不上报
 *     ⇒ 库里真建了待办，界面却说「0 项已加入」
 * 而已有的门只守着 `eventsCreated` 一格（§56 那道）。
 */
describe('§89 · 接线值：ingest 的两个实参与四个上报值', () => {
  /** 按括号配平切出 `ingestMeetingArtifacts(` 的全部顶层实参。 */
  function callArgs(src: string): string[] | null {
    const call = 'ingestMeetingArtifacts('
    const i = src.indexOf(call)
    if (i < 0) return null
    const from = i + call.length
    const args: string[] = []
    // ⚠ 尾逗号会切出空实参；中间空实参在 JS 里本就是语法错误，丢掉不算掩盖。
    const done = () => args.filter((a) => a.trim() !== '')
    let depth = 0
    let start = from
    for (let j = from; j < src.length; j++) {
      const ch = src[j]
      if (ch === '(' || ch === '[' || ch === '{') depth++
      else if (ch === ')' || ch === ']' || ch === '}') {
        if (depth === 0) { args.push(src.slice(start, j)); return done() }
        depth--
      } else if (ch === ',' && depth === 0) { args.push(src.slice(start, j)); start = j + 1 }
    }
    return done()
  }

  it('★ 两个实参必须是 fresh 与 result（不是被裁剪过的副本）', () => {
    const args = callArgs(code)
    assert.ok(args !== null, '解析不出 ingestMeetingArtifacts 的实参 —— 判据失效，先修判据')
    assert.deepEqual(args.map((a) => a.trim()), ['fresh', 'result'],
      `实参是 ${JSON.stringify(args.map((a) => a.trim()))} ⇒ refine 的 todos/nextMeeting 到不了入库层`)
  })

  it('★ fresh 必须来自 getMeeting（持久层），不是录音态或常量', () => {
    assert.match(code, /const\s+fresh\s*=\s*await\s+getMeeting\(/,
      'ingest 的第一个实参不是从持久层取回的那份会议')
  })

  it('★ 每个上报值都必须全部回写 outcome（否则界面报 0）', () => {
    // ⚠§229.1：键表从 5 个加到 7 个。新增的两个（eventUnresolved/eventFailed）
    //   不在这里点名，就等于「上报了但没人看」——而这正是 §229.1 那个洞的形状：
    //   ingest 老实报了，文案层拿不到，于是用户看到的是「什么也没发生」。
    for (const k of [
      'todosCreated', 'reminderPlanned', 'reminders', 'dueUnresolved',
      'eventsCreated', 'eventUnresolved', 'eventFailed',
    ]) {
      assert.match(code, new RegExp(`outcome\\.${k}\\s*=\\s*ingested\\.${k}`),
        `ingested.${k} 没有回写到 outcome ⇒ 界面会报 0`)
    }
  })

  it('★ 回写还不够：buildIngestNotice 必须真的收到新两格', () => {
    // ★§229.1：上一条量的是「ingest → outcome」，漏了「outcome → 文案」这一跳。
    //   两跳都断了的话，第一条照样全绿 —— 与 `meeting-next-event.test.ts`
    //   记的 N11 是同一个教训（只看形状，量不到值流）。
    for (const k of ['eventUnresolved', 'eventFailed']) {
      assert.match(code, new RegExp(`${k}:\\s*outcome\\.${k}`),
        `buildIngestNotice 没收到 outcome.${k} ⇒ 这一格永远是初值，文案说不了话`)
    }
  })

  it('负控：实参被换掉时上面那条必须报红（防止判据恒真）', () => {
    const mutated = code.replace('ingestMeetingArtifacts(fresh, result)', 'ingestMeetingArtifacts(fresh, {})')
    assert.notEqual(mutated, code, '样本构造失败：锚点没命中')
    const args = callArgs(mutated)
    assert.ok(args !== null)
    assert.notDeepEqual(args.map((a) => a.trim()), ['fresh', 'result'],
      '判据没咬住：实参已换成 {} 却仍判定通过')
  })
})
