// meeting-dedup.test.ts — 门禁：相邻语音段的重叠必须被消解。
//
// ── 这道门守的是什么（2026-10-06 用户实测反馈「录音片段重复」）──
//
// 真实链路：
//   VadSegmenter.finalizeSegment() 按 [speechStartMs-sliceMs, endMs] 取音频
//   → 每段独立 POST /stt/transcribe
//   → ingestSpeechBlob 存成一条 segment
//   → updateTranscript 拼成全文
//
// ★ 整条前端链路此前**没有任何去重**。后端 stt/incremental.go 有
//   mergeIncremental，但它属于 IncrementalTranscriber —— 前端从不调用它
//   （前端走的是逐段 sttApi.transcribe）。所以那次修的是一条没人走的路径，
//   用户看到的重复一点没少。
//
// 重复的产生机制（VadSegmenter 逐行核对）：
//   - 取片窗口带 sliceMs(250ms) 余量以补偿 MediaRecorder 缓冲延迟；
//   - speechStartMs 来自 requestAnimationFrame 能量判定，RAF 在后台被节流
//     会让语音起点判定**回退**；
//   - 相邻两段取片窗口重叠 ⇒ 同一段音频转写两次 ⇒ 「今天今天下午三点」。
//
// 与讯飞听见/Otter/FunASR 两阶段模式一致：切片留重叠余量避免断词是必需的，
// 代价就是必须在文本侧消解。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readdirSync, readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join, relative } from 'node:path'
import { dedupeSegmentText, dedupeSegments, dedupeStep, dedupeTranscriptParagraphs, likelyAudioOverlap, renderTranscript } from './meeting-dedup.ts'

const HERE = dirname(fileURLToPath(import.meta.url))
const read = (f: string) => readFileSync(join(HERE, f), 'utf8')
const seg = (speakerLabel: string, text: string) => ({ speakerLabel, text }) as {
  speakerLabel: string; text: string
}

describe('A · 行为层：重叠必须被消解', () => {
  it('精确重叠（ASR 两次输出逐字一致）', () => {
    assert.equal(
      dedupeSegmentText('今天下午三点开项目评审会', '三点开项目评审会请准备材料'),
      '今天下午三点开项目评审会请准备材料',
    )
  })

  it('★ 模糊重叠（标点随机——ASR 的常态）', () => {
    assert.equal(
      dedupeSegmentText('今天下午三点开项目评审会', '今天下午三点开项目评审，请准备材料'),
      '今天下午三点开项目评审会，请准备材料',
    )
  })

  it('重叠区少一个字', () => {
    assert.equal(
      dedupeSegmentText('麻烦你把预算表今天下班前发给我', '把预算表今天下班前发给我谢谢'),
      '麻烦你把预算表今天下班前发给我谢谢',
    )
  })

  it('连续三段都重叠时逐段只出净增', () => {
    const out = dedupeTranscriptParagraphs([
      '我们今天主要讨论三个问题第一是预算',
      '三个问题第一是预算第二是排期',
      '第二是排期第三是人员安排就这样',
    ])
    assert.equal(out, '我们今天主要讨论三个问题第一是预算第二是排期第三是人员安排就这样')
  })

  it('负控：语义无关的两段不得被当成重叠（否则吃真实内容）', () => {
    const a = '今天下午三点开会'
    const b = '明天上午九点复盘'
    assert.equal(dedupeSegmentText(a, b), a + b, '无关的两段必须原样拼接')
  })

  it('负控：短串不得触发模糊匹配', () => {
    assert.equal(dedupeSegmentText('预算', '算完成'), '预算算完成')
    assert.equal(dedupeSegmentText('我', '好'), '我好')
  })

  it('负控：远隔的相似段落要保留（用户可能真的重复说）', () => {
    const a = '这个预算超了'
    const b = '同步一下进度'
    const c = '这个预算超了'
    const out = dedupeTranscriptParagraphs([a, b, c])
    assert.ok(out.includes(c), '非相邻的重复内容必须保留两份')
  })

  // ★ 下面这组是 ANCHOR_COVERAGE 闸的**专属靶子**，缺了它这道闸就是死配置。
  //
  //   构造要求（两条都必要，踩过才知道）：
  //   1. 精确匹配**不能**先命中 —— 否则第 1 步就把重叠裁掉了，
  //      压根走不到 coverage 闸。所以重叠区里必须有差异字符
  //      （「排期，还有人员」vs「排期还有人员」多一个逗号）。
  //   2. 覆盖率落在 [0.42, 0.6) —— 低于 0.6 才会被闸拦下。
  //      其它负控要么被 MIN_ANCHOR 拦（limit<4）、要么被 L<MIN_ANCHOR 拦
  //      （L=3），都轮不到 coverage 闸。
  //
  //   实测：把 ANCHOR_COVERAGE 从 0.6 调到 0.0，下面这两条**依然全绿**
  //   （2026-10-06）——那说明闸没被任何用例压到，是死配置。
  it('负控·coverage 专属：共享常用词但不是切片重叠，必须原样拼接', () => {
    const cases: Array<[string, string, string]> = [
      // 实测 cov=0.43，精确匹配不命中
      ['我们今天讨论了预算和排期，还有人', '排期还有人员安排要尽快定下来', '排期、还有人员'],
      // 实测 cov=0.57，精确匹配不命中
      ['客户希望下个月交付第一版功能', '下个月交付第一版的时间点要确认好', '下个月交付第一版'],
    ]
    for (const [a, b, shared] of cases) {
      const got = dedupeSegmentText(a, b)
      assert.equal(
        got, a + b,
        `cov 落在 [0.42,0.6) 的负控被误判成重叠（共享「${shared}」），会吃掉真实内容`,
      )
    }
  })

  it('阴性对照：真重叠的覆盖率必须高于闸门（否则闸门调高会误杀真去重）', () => {
    // 实测 cov=0.92 / 0.86 / 0.64
    const a = dedupeSegmentText('今天下午三点开项目评审会', '今天下午三点开项目评审，请准备材料')
    assert.equal(a, '今天下午三点开项目评审会，请准备材料')
    const b = dedupeSegmentText('麻烦你把预算表今天下班前发给我', '把预算表今天下班前发给我谢谢')
    assert.equal(b, '麻烦你把预算表今天下班前发给我谢谢')
  })

  // ★ MIN_ANCHOR 的专属靶子。coverage 闸也能拦这几条（L=2/3 远低于 0.6），
  //   所以要验证 MIN_ANCHOR 独立有牙，必须**同时**放宽两道闸才该转红。
  //   这组的存在意义是：让「两道闸都被拆掉」这个复合变异无法蒙混过关。
  it('负控·MIN_ANCHOR 专属：LCS 只对上 2-3 个字时不得判为重叠', () => {
    const cases: Array<[string, string]> = [
      ['这个功能已经上线了', '上线之后用户反馈不错'],       // L=2, cov=0.22
      ['产品路线图已经定下来了', '定下来之后我们开始执行'], // L=3, cov=0.27
      ['客户希望尽快拿到方案', '尽快安排一次评审'],         // L=2, cov=0.25
    ]
    for (const [a, b] of cases) {
      assert.equal(
        dedupeSegmentText(a, b), a + b,
        `LCS 只匹配 2-3 个字不应判为重叠（会吃掉真实内容）：${a} + ${b}`,
      )
    }
  })
})

describe('B · 渲染层：说话人归属必须正确', () => {
  it('重叠时净增仍留在原说话人名下', () => {
    const out = renderTranscript([
      seg('张三', '今天下午三点开项目评审会'),
      seg('张三', '三点开项目评审会请准备材料'),
      seg('李四', '好的我来准备'),
    ])
    const lines = out.split('\n')
    assert.equal(lines[0], '[张三] 今天下午三点开项目评审会')
    assert.equal(lines[1], '[张三] 请准备材料', '去重后的净增必须留在原说话人名下')
    assert.equal(lines[2], '[李四] 好的我来准备')
  })

  it('全文不得出现「今天今天」这类紧邻重复', () => {
    const out = renderTranscript([
      seg('A', '今天下午三点开项目评审会'),
      seg('A', '今天下午三点开项目评审，请准备材料'),
    ])
    assert.ok(!/今天今天/.test(out), `出现重复：${out}`)
    assert.equal(out, '[A] 今天下午三点开项目评审会\n[A] ，请准备材料')
  })

  it('负控：说话人切换时不得把内容并进上一位的话里', () => {
    const out = renderTranscript([
      seg('张三', '这个方案我觉得可行'),
      seg('李四', '可行'),
    ])
    // 内容短于锚点阈值，应保留原文而不是被吃掉。
    assert.equal(out, '[张三] 这个方案我觉得可行\n[李四] 可行')
  })
})

describe('C · 接线层：ingestSpeechBlob 必须真的调用去重（防回退）', () => {
  it('ingest-speech.ts 必须走 renderTranscript', () => {
    const s = read('ingest-speech.ts')
    assert.match(s, /renderTranscript\(/, 'ingest-speech 未使用去重渲染 —— 重复会原样显示给用户')
    assert.ok(
      !/map\(\(s\) => `\[\$\{s\.speakerLabel\}\] \$\{s\.text\}`\)/.test(s),
      '仍是「原样拼接每段」的旧写法（无去重）',
    )
  })

  it('负控：退回原样拼接的样本必须被上面那条判红', () => {
    const legacy = 'opts.segments.map((s) => `[${s.speakerLabel}] ${s.text}`).join(\'\\n\')'
    const re = /map\(\(s\) => `\[\$\{s\.speakerLabel\}\] \$\{s\.text\}`\)/
    assert.ok(re.test(legacy), '负控：旧写法样本应被正则捕获')
  })
})

describe('D · 接线层：所有出口都必须去重（防回退）', () => {
  it('ingest-speech.ts 走 renderTranscript', () => {
    assert.match(read('ingest-speech.ts'), /renderTranscript\(/)
  })

  // ⚠⚠ 这道门**当前没有牙**（2026-10-11 §438 实测，别把它当成保护）。
  //
  // 它断言的是「appendText 调了 renderTranscript」——这个断言成立。
  // 但被它保护的那批段**压根不参与去重**：
  //   实时字幕（Web Speech `isFinal`）→ recordingRuntime.ts:459 → appendText
  //   → buildUtteranceSegment（meeting-utterance.ts:41）→ origin: 'manual'
  //   → dedupeStep 第 222 行 `if (manual) return …` **整段提前返回**
  // ⇒ renderTranscript 在这条路上执行的是原样拼接，去重是**恒等操作**。
  //
  // 实测（§438）：相邻两段有真实重叠时 origin:'asr' 渲染出「预算超了」，
  // 而 origin:'manual'（= 这条路的真实产物）渲染出整段重复；
  // 且累积体被污染后会**逐段放大**。
  //
  // 「字幕段该是 asr 还是 manual」是产品口径，已登记 **#20**，不由本门替人决定。
  // 本门要真正有牙，得先有那个答案。
  it('★ recordingRuntime.appendText 也要去重（本地 sherpa 实时字幕是另一条录音入口）', () => {
    const s = read('../../native/recordingRuntime.ts')
    assert.match(
      s, /renderTranscript\(this\.segments\.value\)/,
      'appendText 仍在原样拼接 —— 本地实时字幕路径会显示重复',
    )
    assert.ok(
      !/updateTranscript\(meetingId, this\.segments\.value\.map/.test(s),
      '仍是「原样 map 拼接」的旧写法',
    )
  })

  it('★ useLiveSummary 喂给 LLM 的 segments 必须去重', () => {
    const s = read('../../composables/useLiveSummary.ts')
    assert.match(s, /dedupeSegments\(segments\.value\)/, '摘要/推荐拿到的仍是含重复的 segments')
    assert.match(s, /summarize\(\s*\n\s*id,\s*\n\s*cleanSegments/, 'summarize 未使用去重后的 segments')
    assert.match(s, /recommend\(id, cleanSegments,/, 'recommend 未使用去重后的 segments')
  })

  it('★ api/meetings.ts 的三条降级路径都要去重（离线时用户正走这些）', () => {
    const s = read('../../api/meetings.ts')
    const n = (s.match(/renderTranscript\(segments\)/g) || []).length
    assert.ok(
      n >= 3,
      `normalizeRefine/fallbackSummarize/fallbackRefine 三条降级路径应各自去重，实际 ${n} 处`,
    )
    assert.ok(
      !/segments\.map\(\(s\) =>\s*\n?\s*`\[\$\{s\.speakerLabel/.test(s),
      '仍有原样拼接的旧写法',
    )
  })

  it('★ MeetingDetailView 在源头去重（整页消费者共用）', () => {
    const s = read('MeetingDetailView.vue')
    assert.match(s, /displaySegments = computed\(\(\) => dedupeSegments\(/, '详情页未在源头去重')
  })
})

describe('E · dedupeSegments 的行为', () => {
  it('相邻重叠段被裁掉，startMs 保持不变', () => {
    const out = dedupeSegments([
      { text: '今天下午三点开项目评审会', startMs: 0 },
      { text: '今天下午三点开项目评审，请准备材料', startMs: 8000 },
    ])
    assert.equal(out[0].text, '今天下午三点开项目评审会')
    assert.equal(out[1].text, '，请准备材料')
    assert.equal(out[1].startMs, 8000, '时间戳是真实采集的，不得被去重篡改')
  })

  it('不修改入参数组（segments 是响应式的）', () => {
    const input = [{ text: '今天下午三点开会' }, { text: '今天下午三点开会讨论排期' }]
    const snapshot = JSON.stringify(input)
    dedupeSegments(input)
    assert.equal(JSON.stringify(input), snapshot, '去重必须返回新数组，不能原地改响应式数据')
  })

  it('无重叠时原样返回等价内容', () => {
    const input = [{ text: '今天下午三点开会' }, { text: '明天上午九点复盘' }]
    assert.deepEqual(dedupeSegments(input).map(s => s.text), input.map(s => s.text))
  })
})

describe('G · 时间轴判据：区分「切片重叠」与「用户真的重复说」', () => {
  // ★ 这组解决一个原先记为「不修」的固有局限：
  //   dedupe('今天我们讨论了预算和排期', '预算和排期都要再确认一次')
  //   纯文本形态上和切片重叠**完全一样**（新段以旧段结尾开头），无从区分。
  //   时间戳能区分：正常切分两段之间有 silenceMs(1500ms) 静音间隙，
  //   而 RAF 节流导致 speechStartMs 回退时，下一段起点会落到上一段结尾**之前**。

  it('likelyAudioOverlap：起点落在上一段结尾之前 ⇒ 重叠', () => {
    assert.equal(likelyAudioOverlap({ prevEndMs: 8000, nextStartMs: 7800 }), true)
    assert.equal(likelyAudioOverlap({ prevEndMs: 8000, nextStartMs: 3000 }), true)
  })

  it('likelyAudioOverlap：有正常静音间隙 ⇒ 不是重叠', () => {
    assert.equal(likelyAudioOverlap({ prevEndMs: 8000, nextStartMs: 9500 }), false)
  })

  // §613 新增：**边界那一档**。上面两条测的是「明显在前」与「明显在后」，
  // 唯独漏了「正好等于」——
  // §613 变异实测：把实现里的 `<` 改成 `<=`，整份 meeting-dedup.test.ts
  // （连同 asr / two-stage 两份）**一条不红** ⇒ 该边界零判据。
  //
  // 语义取自实现自己的注释：「下一段起点落在上一段结尾**之前**」为重叠。
  // 「正好等于」= 两段首尾相接、没有交叠 ⇒ 必须判**不重叠**；
  // 判成重叠会让「用户真的连着说了两段」被当成切片重叠而裁掉。
  it('likelyAudioOverlap：起点正好等于上一段结尾 ⇒ 首尾相接，不算重叠', () => {
    assert.equal(likelyAudioOverlap({ prevEndMs: 8000, nextStartMs: 8000 }), false,
      '首尾相接（既不在前也不在后）必须判不重叠；判成重叠会把用户连着说的两段当成切片重叠')
    // 相邻的两档一并钉住，避免这条被改成 `>=` 之类仍能通过
    assert.equal(likelyAudioOverlap({ prevEndMs: 8000, nextStartMs: 7999 }), true)
    assert.equal(likelyAudioOverlap({ prevEndMs: 8000, nextStartMs: 8001 }), false)
  })

  it('likelyAudioOverlap：时间戳非法 ⇒ 不下结论（回退纯文本判据）', () => {
    assert.equal(likelyAudioOverlap({ prevEndMs: NaN, nextStartMs: 100 }), false)
    assert.equal(likelyAudioOverlap({ prevEndMs: 100, nextStartMs: Number.NaN }), false)
  })

  it('★ 时间轴重叠（gap 0ms）⇒ 必须去重', () => {
    const out = renderTranscript([
      seg('A', '今天下午三点开项目评审会'),
      { ...seg('A', '今天下午三点开项目评审，请准备材料'), startMs: 7800, endMs: 16000 },
    ] as never)
    assert.ok(!/今天今天/.test(out), `重叠未消解：${out}`)
    assert.ok(/，请准备材料/.test(out), `净增应保留：${out}`)
  })

  it('★ 有正常间隙 ⇒ 用户真重复说，两段都要保留', () => {
    // 这是本组的核心：纯文本形态与切片重叠一模一样，只有时间轴能区分。
    const out = renderTranscript([
      { speakerLabel: 'A', text: '今天我们讨论了预算和排期', startMs: 0, endMs: 8000 },
      { speakerLabel: 'A', text: '预算和排期都要再确认一次谢谢', startMs: 9500, endMs: 17000 },
    ])
    assert.ok(
      out.includes('今天我们讨论了预算和排期') && out.includes('预算和排期都要再确认一次谢谢'),
      `用户真的重复说的话被吃掉了：${out}`,
    )
    assert.equal(out.split('\n').length, 2, '两段都应保留')
  })

  // ★ 这条是时间轴判据的**专属靶子**，缺了它 N2/N3 变异能蒙混过关
  //   （实测：把 overlapping 恒设为 false，用例仍全绿 —— 因为那批用例的
  //   LCS 覆盖率都在 0.9 以上，soft 路径自己也能命中，时间轴判据没被区分开）。
  //
  // 构造要求：**精确匹配能命中、但 LCS 覆盖率 < 0.6**。
  // 这样只有「走精确匹配路径」才能消解重叠；恒 false 时会落到 soft 路径
  // 被覆盖率闸拦下 ⇒ 重复留在界面上 ⇒ 判红。
  it('★ 专属靶子：cov<0.6 但精确匹配命中时，仍须靠时间轴判据决定去不去重', () => {
    // 实测 cov=0.57 / exact=4（'这个需求的优先级我建议调整到' / '下个迭代，优先级建议调整一下'）
    const overlapping = renderTranscript([
      { speakerLabel: 'A', text: '这个需求的优先级我建议调整到下个迭代', startMs: 0, endMs: 9000 },
      { speakerLabel: 'A', text: '下个迭代，优先级建议调整一下', startMs: 8900, endMs: 17000 },
    ])
    const gapOk = renderTranscript([
      { speakerLabel: 'A', text: '这个需求的优先级我建议调整到下个迭代', startMs: 0, endMs: 9000 },
      { speakerLabel: 'A', text: '下个迭代，优先级建议调整一下', startMs: 10500, endMs: 19000 },
    ])
    // 有间隙 ⇒ 保留两段（cov<0.6 时 soft 闸本来也拦得住，但精确匹配会吃）
    assert.ok(
      gapOk.includes('下个迭代，优先级建议调整一下'),
      `有间隙时不该动用户的话：${gapOk}`,
    )
    // 无间隙 ⇒ 走精确匹配消解掉「下个迭代」这段重叠。
    //
    // ★ 必须断言**第二段的确切内容**，不能断言「拼接后不含某个串」——
    //   renderTranscript 每段是独立一行（`[A] ...`），跨行的重复串
    //   永远匹配不上，断言会恒真（2026-10-06 实测踩到）。
    const lines = overlapping.split('\n')
    assert.equal(lines.length, 2)
    assert.equal(lines[0], '[A] 这个需求的优先级我建议调整到下个迭代', '首段必须原样')
    assert.equal(
      lines[1], '[A] ，优先级建议调整一下',
      `时间轴重叠时第二段应只留净增，实际 ${JSON.stringify(lines[1])}`,
    )
  })

  // ★ N3 变异的专属靶子：无时间戳的那条分支（: true）必须钉住。
  //   它对应 appendText 造出的草稿段（startMs 取上一段 endMs）与任何
  //   时间戳缺失的形态 —— 重复最严重的那条路径。把它改成 false 的话，
  //   这些段会全走 soft，被覆盖率闸拦下 ⇒ 重复原样显示。
  it('★ 专属靶子：无时间戳时仍须走精确匹配（拿不到判据不等于可以不去重）', () => {
    // 必须用 cov<0.6 的样本：cov 高的输入 soft 路径自己也能命中，
    // 那时「: true 改 : false」是等价变异 ⇒ 用例恒绿（2026-10-06 实测）。
    // 实测 cov=0.571 / exact=4。
    const out = renderTranscript([
      seg('A', '这个需求的优先级我建议调整到下个迭代'),
      seg('A', '下个迭代，优先级建议调整一下'),
    ] as never)
    const lines = out.split('\n')
    assert.equal(lines.length, 2)
    assert.equal(lines[0], '[A] 这个需求的优先级我建议调整到下个迭代', '首段必须原样')
    assert.equal(
      lines[1], '[A] ，优先级建议调整一下',
      `无时间戳时也该走精确匹配去重，实际 ${JSON.stringify(lines[1])}`,
    )
  })

  it('负控：去掉时间轴判据（永远按「有重叠」处理）必须让上一条判红', () => {
    // 不看时间轴时，纯文本会把「预算和排期」当重叠吃掉 —— 这正是修复前的行为
    const noTiming = renderTranscript([
      { speakerLabel: 'A', text: '今天我们讨论了预算和排期' },
      { speakerLabel: 'A', text: '预算和排期都要再确认一次谢谢' },
    ] as never)
    assert.ok(
      !/今天我们讨论了预算和排期.*预算和排期/.test(noTiming.split('\n')[1] ?? ''),
      '无时间戳时也应保守（走 LCS 闸）而不是吃精确匹配',
    )
  })
})

/**
 * H · ★ MIN_ANCHOR 那道闸**不是冗余的**（2026-10-11 实测证伪）
 *
 * 本块守的是一个**曾经被删掉过**的闸。删它的理由写在旧注释里：
 * 「变异测试证明它冗余 —— 只把 MIN_ANCHOR 放宽到 1 而 coverage 保持 0.6 时，
 * 15 条用例全绿」。**那个结论是错的。**
 *
 * 证伪方法：按**参数结构**构造语料（匹配字取自集合 M、未匹配字取互斥集合 U，
 * 于是 LCS 长度精确等于 m），枚举 limit∈[2,12] × m∈[0,limit] × 三种摆放，
 * 178 条同时喂给**真函数**——后端 `mergeIncremental` 与本实现。分叉 5 条，
 * 全部落在 limit∈{3,4} 且 lcsLen∈{2,3} 这一格：coverage（lcsLen/limit≥0.6）
 * 放行，而 MIN_ANCHOR（lcsLen≥4）会拦。**两道闸只在这一格不等价。**
 *
 * 后果不是多几个重复字，而是**删掉用户没说过的话**。
 */
describe('H · MIN_ANCHOR 闸：短 LCS 必须判成「不重叠」（防删真实内容）', () => {
  // 下面 5 条即修复前分叉的 5 条。断言取自**真 Go mergeIncremental** 的实测输出，
  // 不是我手算的 —— 手算过的东西不该当基准。
  const DIVERGENT = [
    { committed: '甲云山', next: '云甲山后续内容', want: '甲云山云甲山后续内容', shape: 'B散落' },
    { committed: '甲乙云', next: '云甲乙后续内容', want: '甲乙云云甲乙后续内容', shape: 'C末尾' },
    { committed: '甲云山石', next: '云甲山石后续内容', want: '甲云山石云甲山石后续内容', shape: 'B散落' },
    { committed: '甲乙云山', next: '云甲乙山后续内容', want: '甲乙云山云甲乙山后续内容', shape: 'B散落' },
    { committed: '甲乙丙云', next: '云甲乙丙后续内容', want: '甲乙丙云云甲乙丙后续内容', shape: 'C末尾' },
  ]

  for (const c of DIVERGENT) {
    it(`短锚点（limit≤4）⇒ 判不重叠，一个字都不删（${c.shape}）`, () => {
      assert.equal(
        dedupeSegmentText(c.committed, c.next),
        c.want,
        `短锚点被当成了真重叠 ⇒ 重叠区里没出现过的字被删掉了。` +
          `后端基准保留全部内容，界面却比后端少字。`,
      )
    })
  }

  it('正控：锚点够长（lcsLen≥4）时去重**仍然必须发生**', () => {
    // MIN_ANCHOR=4 的正侧：lcsLen=4 时两道闸都放行 ⇒ 必须照常去重。
    // 少了这一条，上面那组可能靠「一律不去重」而全绿。
    assert.equal(dedupeSegmentText('今天讨论预算排期', '预算排期要再确认'), '今天讨论预算排期要再确认')
  })

  it('★ 真负控：修复前的实测输出与修复后**逐条不同**（证明判据有牙）', () => {
    // 修复前 TS 的 5 条真实输出（本次修复前的实测值，非手算）：
    const before = [
      '甲云山云后续内容',
      '甲乙云云后续内容',
      '甲云山石云后续内容',
      '甲乙云山云后续内容',
      '甲乙丙云云后续内容',
    ]
    const after = DIVERGENT.map((c) => dedupeSegmentText(c.committed, c.next))
    for (let i = 0; i < DIVERGENT.length; i++) {
      assert.notEqual(
        after[i],
        before[i],
        `第 ${i + 1} 条：修复前后输出相同 ⇒ 这条判据对该闸没有区分力（恒真）`,
      )
      assert.equal(after[i], DIVERGENT[i].want)
    }
  })
})

/**
 * I · ★ 端到端：**送进 LLM 的那份文字**里，一个字都不能少
 *
 * 前面两组门各覆盖一半，但中间缺一环：
 *   · C/D 两组断言的是**源码里有没有调 renderTranscript**（正则，防回退）
 *   · H 组断言的是 `dedupeSegmentText` **函数级**的行为
 *   · 本组才是**行为级**的：拿生产形状的段（带 speakerLabel / startMs / endMs /
 *     origin:'asr'）喂真实的 `renderTranscript`，断言它的输出。
 *
 * 为什么必须是这一层：§427 记着 `renderTranscript` 有 5 个出口，其中
 * ③ `api/meetings.ts` 的 fallbackSummarize / fallbackRecommend 与
 * ④⑤ fallbackRefine 提示词（§54 说它是**高频**路径）——
 * ⇒ 用户看到的那份文字，和喂给模型的那份文字，**是同一个函数的同一个返回值**。
 * §437 端到端实测过「李娜说『没问题』→ 界面渲染成『没』且送进 LLM」，
 * 但那是一次**手工探针**，没有变成常驻门。
 *
 * ★ 断言值绑定**后端基准**而不是手算：
 *   本组的期望值就是 §455 那轮用真 `mergeIncremental` 量出来的输出。
 *   前端生产渲染与后端基准逐字相等 —— 这比「两边的正则看起来像」强得多。
 */
describe('I · 端到端：renderTranscript 的输出与后端基准逐字相等', () => {
  /** 造一个生产形状的 ASR 段（与 processSegment 的产物同形）。 */
  function asrSeg(text: string, startMs: number, endMs: number) {
    return { speakerLabel: '李娜', lang: 'zh', confidence: 0.86, startMs, endMs, text, origin: 'asr' as const }
  }

  /** 去掉 `[说话人] ` 前缀，只留正文字 —— 即送进 LLM 的那串字符。 */
  function bodyOf(rendered: string): string {
    return rendered.split('\n').map((l) => l.replace(/^\[[^\]]*\]\s*/, '')).join('')
  }

  // 两条即 §455 那 178 条语料里分叉的样本，取自真 mergeIncremental 的实测输出。
  const CASES = [
    {
      name: 'limit=3 散落重叠',
      segs: [asrSeg('甲云山', 0, 3000), asrSeg('云甲山后续内容', 2500, 6000)],
      // Go 基准：重叠区不足 4 字 ⇒ 判不重叠 ⇒ 全部保留
      goBaseline: '甲云山云甲山后续内容',
      // 修复前 TS 的真实输出（删掉了用户没说过、又没重复出现的「甲」「山」）
      beforeFix: '甲云山云后续内容',
    },
    {
      name: 'limit=4 末尾重叠',
      segs: [asrSeg('甲乙丙云', 0, 4000), asrSeg('云甲乙丙后续内容', 3000, 7000)],
      goBaseline: '甲乙丙云云甲乙丙后续内容',
      beforeFix: '甲乙丙云云后续内容',
    },
  ]

  for (const c of CASES) {
    it(`不丢字：${c.name}`, () => {
      const rendered = renderTranscript(c.segs)
      assert.equal(
        bodyOf(rendered),
        c.goBaseline,
        `端到端产出与后端基准不等 ⇒ 生产链上仍在丢字。用户看到与喂给 LLM 的都是这一份。`,
      )
      assert.notEqual(
        bodyOf(rendered),
        c.beforeFix,
        '输出与修复前相同 ⇒ 这条判据对 MIN_ANCHOR 那道闸没有区分力（恒真）',
      )
    })
  }

  it('★ 真负控：锚点够长时**仍然必须真的去重**（否则上面两条可能被「一律不去重」骗过）', () => {
    const rendered = renderTranscript([
      asrSeg('今天我们讨论了预算和排期', 0, 5000),
      asrSeg('预算和排期都要再确认一次', 4000, 9000),
    ])
    assert.equal(
      bodyOf(rendered),
      '今天我们讨论了预算和排期都要再确认一次',
      '长锚点的真实重叠必须被裁掉 —— 去重整体失效比丢字更难发现',
    )
  })

  it('无时间戳时同样保守（拿不到判据 ≠ 可以乱裁）', () => {
    const rendered = renderTranscript([
      { ...asrSeg('甲云山', 0, 3000), startMs: undefined, endMs: undefined },
      { ...asrSeg('云甲山后续内容', 2500, 6000), startMs: undefined, endMs: undefined },
    ])
    assert.equal(bodyOf(rendered), '甲云山云甲山后续内容', '无时间戳也应判不重叠，而不是靠猜')
  })
})

/**
 * J · ★ **`updateTranscript` 的每一个写入点都必须走去重**（动态枚举，不靠手写点名）
 *
 * ── 这道门补的是什么洞 ──
 *
 * C/D/E/H/I 各组加起来已经覆盖了：接线（C/D 的源码断言）、函数行为（A/H）、
 * 端到端产出（I）。⚠ 但 D 组是**逐个文件写死的正则**——
 * 它检查 `ingest-speech.ts`、`recordingRuntime.ts`、`api/meetings.ts`、
 * `MeetingDetailView.vue` 这几个**已知的**文件。
 *
 * ⇒ 新增第 6 个 `updateTranscript(...)` 写入点时，**没有任何门会响**，
 * 而那一处若直接传原始拼接文本，重复就**永久落库 + 默认展示 + 流进 LLM**。
 * ⇒ 这与 §439 记下的「手写点名的判据覆盖面没有门在守」同型：
 *   **点名的清单只覆盖名字已经写下来的人。**
 *
 * 本组改为**扫全 `src/`**：枚举每一处调用，逐处断言第二个实参是 `renderTranscript(...)`。
 * ⇒ 新增写入点自动进入覆盖范围，**不需要改这道门**。
 */
describe('J · updateTranscript 的每个写入点都必须走去重', () => {
  const SRC_ROOT = join(HERE, '..', '..')

  /** 递归扫 .ts/.vue（跳过测试与 node_modules）。 */
  function walk(dir: string, out: string[] = []): string[] {
    for (const e of readdirSync(dir, { withFileTypes: true })) {
      if (e.name === 'node_modules' || e.name === '.git') continue
      const p = join(dir, e.name)
      if (e.isDirectory()) walk(p, out)
      else if (/\.(ts|vue)$/.test(e.name) && !/\.test\.ts$/.test(e.name)) out.push(p)
    }
    return out
  }

  /** 找出所有 `updateTranscript(` 调用点，跳过函数**定义**那一处。 */
  function callSites(): { file: string; line: number; text: string }[] {
    const out: { file: string; line: number; text: string }[] = []
    for (const f of walk(SRC_ROOT)) {
      readFileSync(f, 'utf8')
        .split('\n')
        .forEach((line, i) => {
          if (!line.includes('updateTranscript(')) return
          // 定义行：`export async function updateTranscript(id: string, ...`
          if (/function\s+updateTranscript\s*\(/.test(line)) return
          out.push({ file: relative(SRC_ROOT, f), line: i + 1, text: line.trim() })
        })
    }
    return out
  }

  it('抽取器有效（量具先自证，防止「扫到 0 个 ⇒ 恒绿」）', () => {
    const sites = callSites()
    assert.ok(
      sites.length >= 5,
      `只扫到 ${sites.length} 处 updateTranscript 调用（预期 ≥5）—— ` +
        `抽取器可能坏了：路径、扩展名白名单或「跳过定义行」的规则有一处不对。`,
    )
    // 独立锚点：这 5 处是 §442 登记过的写入点，逐个点名对表。
    const files = new Set(sites.map((s) => s.file))
    for (const expect of [
      'features/meetings/ingest-speech.ts',
      'native/recordingRuntime.ts',
      'features/meetings/MeetingDetailView.vue',
    ]) {
      assert.ok(files.has(expect), `抽取器没抓到 ${expect} —— 它在这条链上是写入点`)
    }
  })

  it('★ 每一处调用都必须把 renderTranscript 的结果写进去', () => {
    const bad = callSites().filter((s) => !/updateTranscript\([^,]*,\s*renderTranscript\(/.test(s.text))
    assert.deepEqual(
      bad.map((b) => `${b.file}:${b.line}  ${b.text}`),
      [],
      '这些写入点没有走去重渲染 ⇒ 重复会原样落库、默认展示、并流进 LLM 提示词。\n' +
        '修法：第二个实参传 renderTranscript(...) 的返回值。',
    )
  })

  it('负控：直接传原始拼接的写入点必须被上面那条判红', () => {
    // 真实存在过的坏写法（§438 之前的 appendText 形态）。
    const legacy = 'await updateTranscript(meetingId, this.segments.value.map((s) => `[${s.speakerLabel}] ${s.text}`).join("\\n"))'
    assert.ok(
      !/updateTranscript\([^,]*,\s*renderTranscript\(/.test(legacy),
      '负控样本必须通不过上面那条判据，否则那道判据恒绿',
    )
  })
})

/**
 * K · ★ 连**底层写入原语**也不许绕过 `updateTranscript`
 *
 * ── 为什么 J 组还不够 ──
 *
 * `updateTranscript`（`meetings-store.ts:238`）本身只是
 * `updateMeeting(id, { transcript })` 的一层包装。
 * ⇒ **J 组枚举的是包装层**：谁直接调 `updateTranscript(` 都被它看见；
 *   但谁直接调 `updateMeeting(id, { transcript: … })` 就**完全绕过** J 组，
 *   而那正是重复文本落库的那一列。
 *
 * ── 实测结论（2026-10-11）──
 *
 * 全 `src/` 18 个 `updateMeeting` 调用点（其中 6 个跨行），
 * patch 里带**裸** `transcript` 键的**只有 1 个**：`meetings-store.ts:239`
 * —— 正是 `updateTranscript` 自己的函数体。
 * ⇒ **今天没有人绕过它。** 本组把这句「今天」钉住。
 *
 * ── ⚠⚠ 本组的第一条判据是**抽取器自证**，而它有来由 ──
 *
 * 第一版抽取器写成「先剥掉含 transcript 的长键，再找裸键」，
 * 但剥的���则要求键后面跟 `:` —— 而 `{ transcript }` 是**简写形式、没有冒号**，
 * 于是它没被剥掉、判据又找不到它 ⇒ **报出「0 处违规」**。
 * ⛔ 那是个**假阴性**：读数「0」看起来是最干净的结论，实际是量具坏了。
 * ⇒ 所以现在把**已知真相**（唯一那处就是 `updateTranscript` 本体）写死成断言：
 * 抽取器一坏（扫不到 / 扫出多余的），本条立刻红，而不会静默说「都挺好」。
 */
describe('K · updateMeeting 这一层也不许直接写 transcript', () => {
  const SRC_ROOT = join(HERE, '..', '..')

  function walk(dir: string, out: string[] = []): string[] {
    for (const e of readdirSync(dir, { withFileTypes: true })) {
      if (e.name === 'node_modules' || e.name === '.git') continue
      const p = join(dir, e.name)
      if (e.isDirectory()) walk(p, out)
      else if (/\.(ts|vue)$/.test(e.name) && !/\.test\.ts$/.test(e.name)) out.push(p)
    }
    return out
  }

  /**
   * 枚举所有 `updateMeeting(...)` 调用，**括号配平**取完整表达式（跨行安全）。
   * 返回其中 patch 带**裸** `transcript` 键的位置。
   *
   * ⚠ 两个必须同时处理的形态（少一个都会漏）：
   *   ① `transcript: x`（带冒号）
   *   ② `{ transcript }`（**简写、没有冒号**）—— 第一版就是漏在这里
   * 另外要先删掉 refinedTranscript / liveTranscript 这类**长键**，
   * 否则它们会把裸键的匹配带偏。
   */
  function bareTranscriptWriters(): string[] {
    const out: string[] = []
    for (const f of walk(SRC_ROOT)) {
      const src = readFileSync(f, 'utf8')
      for (let i = 0; i < src.length; i++) {
        const at = src.indexOf('updateMeeting(', i)
        if (at < 0) break
        i = at
        if (/function\s+updateMeeting\s*$/.test(src.slice(Math.max(0, at - 60), at))) continue
        let depth = 0
        let j = at + 'updateMeeting('.length - 1
        for (; j < src.length; j++) {
          if (src[j] === '(') depth++
          else if (src[j] === ')') { depth--; if (depth === 0) break }
        }
        const expr = src.slice(at, j + 1)
        const stripped = expr.replace(
          /\b(refinedTranscript|liveTranscript|transcriptFallback|summaryFallback)\b/g, '',
        )
        // 简写与带冒号两种形态都要认
        if (/(^|[^\w])transcript(\s*:|(?=\s*[,}\s]))/.test(stripped)) {
          out.push(`${relative(SRC_ROOT, f)}:${src.slice(0, at).split('\n').length}`)
        }
        i = j
      }
    }
    return out
  }

  it('抽取器自证：必须恰好找到 updateTranscript 本体那一处', () => {
    const hits = bareTranscriptWriters()
    // ⚠⚠ §913.3：这里**只比文件，不比行号**。
    //
    //   原本钉的是 `'features/meetings/meetings-store.ts:239'`（**绝对行号**），
    //   而 §913 往 meetings-store.ts 的 `RecommendItem.score` 上加了一段 18 行注释
    //   ⇒ 同一处写入点被推到 **:257**，这道门立刻报假红（实测 gates 第 3 项 EXIT=1）。
    //
    //   ⚠ 行号**从来不是这道门要验的东西**：它要验的是
    //   「抽取器恰好找到**一处**，且那一处在 meetings-store.ts」
    //   —— 下面那条 `filter(!startsWith(...))` 才是它真正的防御面。
    //   ⇒ 行号只会带来「上游任何一次无关编辑都让门变红」的噪声。
    //
    //   ⚠ 削弱了吗？没有：抽取器**仍然**返回 `file:line`（信息没丢，
    //   失败信息里照样能看到具体行），只是比较时取 `file` 部分。
    //   空清单 ⇒ 仍然红；多出一处 ⇒ 仍然红；只有「行号漂移」不再红。
    //   已按 §911.4 的要求**重跑变异确认门没被放松**（见设计文档 §913.7）。
    assert.deepEqual(
      hits.map((h) => h.slice(0, h.lastIndexOf(':'))),
      ['features/meetings/meetings-store.ts'],
      `带裸 transcript 键的写入点清单变了：\n${hits.join('\n') || '（空）'}\n` +
        '· 空 ⇒ 抽取器坏了（第一版就因为漏了 `{ transcript }` 简写而报假阴性）\n' +
        '· 多出来 ⇒ 有人绕过 updateTranscript 直接写 transcript 了',
    )
  })

  it('★ 除 updateTranscript 本体外，任何地方都不得直接写 transcript 列', () => {
    const bad = bareTranscriptWriters().filter((h) => !h.startsWith('features/meetings/meetings-store.ts:'))
    assert.deepEqual(bad, [], `这些调用点直接把 transcript 写进 local_meetings，绕过了去重渲染。\n${bad.join('\n')}`)
  })

  it('负控：简写形态 `{ transcript }` 也必须被判出来（第一版就漏在这里）', () => {
    const shorthand = 'updateMeeting(id, { transcript })'
    const stripped = shorthand.replace(
      /\b(refinedTranscript|liveTranscript|transcriptFallback|summaryFallback)\b/g, '',
    )
    assert.ok(
      /(^|[^\w])transcript(\s*:|(?=\s*[,}\s]))/.test(stripped),
      '抽取器认不出**没有冒号**的简写键 —— 它会静默报「0 处违规」',
    )
    // 反向：长键不得被误判成裸键
    const longKey = 'updateMeeting(id, { refinedTranscript: x })'
    assert.ok(
      !/(^|[^\w])transcript(\s*:|(?=\s*[,}\s]))/.test(
        longKey.replace(/\b(refinedTranscript|liveTranscript|transcriptFallback|summaryFallback)\b/g, ''),
      ),
      'refinedTranscript 被误判成裸 transcript —— 那是另一列，不该被这道门拦',
    )
  })
})

/**
 * §608 新增：**已登记的 Go↔TS 行为差异**，不是缺陷修复，是把差异钉成事实。
 *
 * ── 差异是什么 ──
 *
 * 当相邻两段**完全相同**且长度**超过 MAX_OVERLAP(40)** 时：
 *
 *   Go  stt/incremental.go mergeIncremental :288 有专门一条
 *       「整段重复 ⇒ 判为真实重复说话，**保留两份**」，并由
 *       TestMergeIncrementalDoesNotDropRealContent 显式钉住（60 rune → 120）。
 *   TS  dedupeSegmentText                   : **没有**这一条 ⇒ 走 LCS 那条路，
 *       把其中 40 字当成切片重叠裁掉。
 *
 * ── 真函数双跑的实测读数（2026-10-14，两侧各跑自己那一份实现）──
 *
 *   语料（committed 与 next **完全相同**）      Go 输出   TS 输出
 *     「甲」× 60                                120        80      ⛔ 分叉
 *     45 字真实句子重复一遍                       90        80      ⛔ 分叉
 *     「甲」× 40（正好等于阈值）                  40        40      ✅ 一致
 *     35 字真实句子重复一遍                       35        35      ✅ 一致
 *
 * ⚠ TS 那一侧的后果是**删掉用户真的说了两遍的内容** ——
 * 而本文件头部与 Go 侧注释都写着「误去重的代价是丢掉真实内容，用户永远发现不了」。
 *
 * ── 为什么这里只钉现状、不改成一致 ──
 *
 * 后端同步门（dedup_threshold_sync_test.go 文件头）明写它的原则：
 * 「把一个行为差异偷偷改成一致，比留着它更危险」「差异一旦变化就报红，
 * 逼人来更新这里的登记，而不是让变化悄悄发生」。
 *
 * ⇒ 两侧到底**该**保留一份还是两份，是**产品口径**（用户在界面上看到一次还是两次），
 *   本轮**不代拍**，已登记为决策简报 #58。
 * ⇒ 本条的作用是：① 钉住 TS 现状，让它出现在回归里；② 谁改动它时会被迫看到这段说明。
 *
 * ── §609 补：这条差异是不是唯一的？──
 *
 * 造 **485 条**语料做全量差分（A 类 264 条参数化合成：limit∈[2,12] × 匹配字个数 × 3 种摆放，
 * 匹配字与干扰字取自互斥字符集；B 类 221 条真实中文句变体 + 整段重复）：
 *
 *   分叉 **11 条（2.3%）**，且 **100%** 满足「committed === next 且长度 > 40」、
 *   **100%** 是 Go 输出更长；**A 类 264 条零分叉**
 *   ⇒ 两份实现的精确重叠与 LCS 路径在结构化语料上**逐字节一致**，
 *     本条是该语料内**唯一**的行为差异。
 */
describe('§608 · 已登记的 Go↔TS 行为差异：整段重复超阈值时 TS 会裁掉 40 字', () => {
  const MAX_OVERLAP = 40
  it('现状读数：60 rune 完全重复 ⇒ TS 只剩 80 字（Go 侧是 120）', () => {
    const s = '甲'.repeat(60)
    const out = dedupeSegmentText(s, s)
    assert.equal(Array.from(out).length, 80,
      `TS 现状读数已变（得到 ${Array.from(out).length}）—— ` +
      '若这是修好了/改坏了，请先读本段说明与决策简报 #58，再决定是否要同步改 Go 侧')
  })
  it('现状读数：正好等于 MAX_OVERLAP(40) 时两侧一致，都裁成一份', () => {
    const s = '甲'.repeat(MAX_OVERLAP)
    assert.equal(dedupeSegmentText(s, s), s,
      '等于阈值时应当与 Go 侧一致（都视为切片重叠）；这里变了说明阈值语义被改动')
  })
  it('现状读数：短于阈值时两侧一致，都裁成一份', () => {
    const s = '甲'.repeat(MAX_OVERLAP - 5)
    assert.equal(dedupeSegmentText(s, s), s)
  })
  it('★ 正向要求：本段说明不得被静默删掉', () => {
    const src = read('meeting-dedup.test.ts')
    assert.ok(src.includes('#58'),
      '本文件的差异说明引用了决策简报 #58；若要删这段说明，请先改简报条目')
  })
})

/**
 * §914 / D82：长会议的相邻去重**曾经是二次方的** ——
 * 打开一场 8 小时的会议，光去重就要 6.6 秒；12 小时以上直接把页面打死。
 *
 * ── 缺陷形态（**只有一层**是二次方；另一层是实测证伪的）──
 *
 * ① **窗口那一层（真病根）**：`dedupeSegmentText` / `dedupeSegmentTextSoft`
 *    里做的是 `Array.from(committed)` —— 把**整段累积正文**转成码点数组，
 *    而且**每段做一次**。⇒ 单段 O(累积正文长度)，总量 O(n²)。
 *    ⚠ 第一版修复只把 `Array.from(committed)` 换成「取末尾窗口」，
 *      但窗口是用 `charCodeAt` 逐字读的 —— 而 `committed` 是 ConsString 链，
 *      **读它就得展平** ⇒ 12 小时会议**仍然 OOM**（栈 `String::SlowFlatten`）。
 *    ⇒ 真正的修法是：**窗口由调用方增量维护，函数内一次都不碰 `committed`**。
 *
 * ② **展平那一层（⚠ 实测证伪，不是病根）**：调用方原本靠
 *    `body.slice(prev.length)` 取净增，看着像会强制展平整条链。
 *    ⚠⚠ **实测否定了这个推断**：把净增改回 `body.slice(prev.length)` 后，
 *    1200 段仍然只要 **36ms**、且 **ms/段 持平**（0.024 → 0.035，线性）
 *    ⇒ V8 对 ConsString **尾部**的 slice 不必展平整条链。
 *    ⇒ 「delta 由函数直接算好返回」是**顺手的清理**（少一次拷贝、
 *      且 `rollTail` 需要它），**但它不是 O(n²) 的来源**。
 *    ⚠ 所以下面规模判据**不**该靠它变红：把它改回 slice 是**语义等价**的
 *      （判据②正是用来证明这一点的）⇒ 那种变异**理应**不红。
 *
 * ── 修法 ──
 * 比较窗口由调用方**增量维护**（`rollTail`），函数内**一次都不碰**那条链；
 * 净增由函数直接算好返回（`DedupeStep.delta`）。
 *
 * ── 这道门守什么 ──
 * ① **差分**：新实现必须与「修复前的逐字副本」在随机语料上**完全一致**
 *    （这是「性能优化不改变行为」的判别器）；
 * ② **delta 语义**：`delta` 必须恒等于修复前的取法 `body.slice(prev.length)`；
 * ③ **规模**：2000 段（≈8 小时会议）必须在预算内完成 ⇒ 钉住「线性」。
 */
describe('§914 · 长会议去重必须是线性的（修掉的是 O(n²)）', () => {
  const LEGACY_MAX_OVERLAP = 40
  const LEGACY_MIN_ANCHOR = 4
  const LEGACY_COVERAGE = 0.6

  /**
   * §914 **冻结参照实现** —— 修复**之前**的 `lcsNetNew`，逐字副本。
   *
   * ⚠ 为什么要在判据里放一份**抄写**：它是「输出应当逐字不变」的判别器。
   *   抄写件与生产件一旦分叉 ⇒ 这条差分会红 ⇒ 那**是真实信号**
   *   （两版算法开始分叉了），此时应当去读 §914 的文档并同步复核 Go 侧差分。
   */
  function legacyLcsNetNew(cs: string[], ns: string[], minCoverage = LEGACY_COVERAGE): string | null {
    const limit = Math.min(LEGACY_MAX_OVERLAP, cs.length, ns.length)
    if (limit < 2) return null
    const tail = cs.slice(cs.length - limit)
    const head = ns.slice(0, limit)
    const n = limit
    const dp: number[][] = Array.from({ length: n + 1 }, () => new Array<number>(n + 1).fill(0))
    for (let i = 1; i <= n; i++) {
      for (let j = 1; j <= n; j++) {
        if (tail[i - 1] === head[j - 1]) dp[i][j] = dp[i - 1][j - 1] + 1
        else dp[i][j] = Math.max(dp[i - 1][j], dp[i][j - 1])
      }
    }
    const lcsLen = dp[n][n]
    if (lcsLen / n < minCoverage) return null
    if (lcsLen < LEGACY_MIN_ANCHOR) return null
    const matched = new Array<boolean>(n + 1).fill(false)
    let i = n
    let j = n
    while (i > 0 && j > 0) {
      if (tail[i - 1] === head[j - 1]) { matched[j] = true; i--; j-- }
      else if (dp[i - 1][j] >= dp[i][j - 1]) i--
      else j--
    }
    const out: string[] = []
    for (let k = 1; k <= n; k++) if (!matched[k]) out.push(head[k - 1])
    if (ns.length > limit) out.push(...ns.slice(limit))
    return out.join('')
  }

  /**
   * §914 **冻结参照实现** —— 修复**之前**的 `dedupeSegmentText`，逐字副本。
   * ⚠ 注意 `Array.from(committed)` 与 `cs.join('')` 这两处**正是**被 §914 改掉的
   *   「窗口那一层」；它们必须原样保留，否则这份参照就不再是「修复前」。
   */
  function legacyDedupeSegmentText(committed: string, next: string, minCoverage = LEGACY_COVERAGE): string {
    const n = next.trim()
    if (!committed) return n
    if (!n) return committed
    const cs = Array.from(committed)
    const ns = Array.from(n)
    const limit = Math.min(LEGACY_MAX_OVERLAP, cs.length, ns.length)
    for (let k = limit; k >= 2; k--) {
      if (cs.slice(cs.length - k).join('') === ns.slice(0, k).join('')) {
        return cs.join('') + ns.slice(k).join('')
      }
    }
    const netNew = legacyLcsNetNew(cs, ns, minCoverage)
    if (netNew !== null) return cs.join('') + netNew
    return committed + n
  }

  /** 确定性伪随机语料（不用 Math.random ⇒ 判据可复现）。 */
  function makeCorpus(seed: number): Array<[string, string]> {
    let s = (seed >>> 0) || 1
    const rnd = () => { s = (s * 1664525 + 1013904223) >>> 0; return s / 0x100000000 }
    const pool = ['季度预算', '排期', '张伟', '王芳', '确认一下', '今天下午三点', '交付', '里程碑', 'a', 'bc']
    const nonBmp = ['\u{20000}', '\u{20001}', '\u{1F600}']
    const out: Array<[string, string]> = []
    for (let i = 0; i < 300; i++) {
      const len = 1 + Math.floor(rnd() * 90)
      let c = ''
      for (let k = 0; k < len; k++) {
        c += rnd() < 0.12 ? nonBmp[Math.floor(rnd() * nonBmp.length)] : pool[Math.floor(rnd() * pool.length)]
      }
      // committed 有时是长正文（> 40 码点，正是 §914 动到的那一格），
      // 有时是 next 的一半（真实切片重叠形态），有时与 next 全文相同（#58 那一格）。
      const roll = rnd()
      const committed = roll < 0.4 ? c.repeat(1 + Math.floor(rnd() * 3))
        : roll < 0.75 ? c.slice(0, Math.floor(c.length / 2))
        : c
      out.push([committed, c])
    }
    return out
  }

  it('★ 差分：新实现与「修复前」的逐字副本在 300×5 条随机语料上完全一致', () => {
    let checked = 0
    for (const seed of [1, 7, 42, 20261019, 99991]) {
      for (const [committed, next] of makeCorpus(seed)) {
        const want = legacyDedupeSegmentText(committed, next)
        const got = dedupeSegmentText(committed, next)
        assert.equal(got, want,
          `§914 的性能改动**改变了输出**（seed=${seed}）：\n` +
          `  committed=${JSON.stringify(committed)}\n  next=${JSON.stringify(next)}\n` +
          `  修复前=${JSON.stringify(want)}\n  修复后=${JSON.stringify(got)}`)
        checked++
      }
    }
    assert.equal(checked, 1500, '语料条数不对 ⇒ 差分覆盖面被改小了')
  })

  it('★ delta 的语义恒等于「修复前」的取净增方式 body.slice(prev.length)', () => {
    for (const [committed, next] of makeCorpus(2026)) {
      const step = dedupeStep({ prev: committed, raw: next, prevEndMs: 3000, startMs: 4500 })
      const legacyDelta = step.body.length > committed.length ? step.body.slice(committed.length) : ''
      assert.equal(step.delta, legacyDelta,
        `delta 与修复前的取法不一致：\n  body=${JSON.stringify(step.body)}\n  delta=${JSON.stringify(step.delta)}`)
    }
  })

  /**
   * §914 **冻结参照实现** —— 修复**之前**的 `dedupeSegmentTextSoft`（活路径）。
   * ⚠ 它的 `Array.from(committed)` 正是「窗口那一层」的病根。
   */
  function legacyDedupeSegmentTextSoft(committed: string, next: string, minCoverage = LEGACY_COVERAGE): string {
    const n = next.trim()
    if (!committed) return n
    if (!n) return committed
    const ns = Array.from(n)
    if (committed === n && ns.length > LEGACY_MAX_OVERLAP) return committed + n
    const netNew = legacyLcsNetNew(Array.from(committed), ns, minCoverage)
    if (netNew !== null) return committed + netNew
    return committed + n
  }

  /**
   * §914 **冻结参照实现** —— 修复**之前**的热循环：
   * 「累积正文 + 每段一次 `Array.from(committed)` + 每段一次 `body.slice(prev.length)`」。
   * ⚠ 它存在的唯一目的是**当成本尺子**：判据拿它量新实现的快慢。
   */
  function legacyDedupeLoop(segs: Array<{ text: string }>): void {
    let body = ''
    for (const s of segs) {
      const n = (s.text ?? '').trim()
      if (!body) { body = n; continue }
      const prev = body
      body = legacyDedupeSegmentTextSoft(body, n)
      // ⚠ 修复前就是靠这一句取净增 —— 而它会**强制展平整条 ConsString 链**。
      void body.slice(prev.length)
    }
  }

  /** 真实 ASR 行长中位 66 字（§788 实测）；判据不能依赖 .artifacts/（已 gitignore）。 */
  const ASR_LINE = '第三季度的续费方案基本上已经定了，没签的那两个客户我们走线下合同，'
    + '另外交付里程碑要挪到下个迭代，排期表我今天下午发群里，大家确认一下各自负责的部分。'

  it('★ 规模：新实现必须比「修复前」快一个数量级 —— 钉住「线性」', () => {
    const N = 1200
    const segs = Array.from({ length: N }, (_, i) => ({
      text: `${i} ${ASR_LINE}`,
      speakerLabel: '张明', startMs: i * 14_600, endMs: (i + 1) * 14_600,
    }))

    // ⚠ 相对量而不是绝对量：**比值对机器速度免疫**（CI 慢就两边一起慢）。
    //   取 min 而不是平均：min 是「最快能多快」的稳健估计，不受调度抖动影响。
    const timeMin = (fn: () => void, runs: number): number => {
      let best = Infinity
      for (let i = 0; i < runs; i++) {
        const t0 = process.hrtime.bigint()
        fn()
        const ms = Number(process.hrtime.bigint() - t0) / 1e6
        if (ms < best) best = ms
      }
      return best
    }

    const tNew = timeMin(() => { dedupeSegments(segs) }, 5)
    const tOld = timeMin(() => { legacyDedupeLoop(segs) }, 1)

    // ⚠ 阈值的来历（真实 ASR 行长、实测读数）：
    //   修复后 1200 段 ≈ 27ms；修复前 ≈ 788ms ⇒ 比值 **29×**。
    //   ⇒ 门限取 8× ⇒ 余量 3.6×。
    //   ⚠ 而「把净增改回 `body.slice(prev.length)`」这一变异**不会**让本判据变红
    //   —— 那是**语义等价**的改写（实测 36ms、仍线性），**不是回归**。
    //   真正会让本判据变红的是「窗口层退回 Array.from(committed)」（实测比值掉到 1× 附近）。
    assert.ok(tNew < 500, `新实现 1200 段用了 ${tNew.toFixed(0)}ms（预算 500ms）`)
    assert.ok(tOld > tNew * 8,
      `新实现没有比修复前快一个数量级：新 ${tNew.toFixed(0)}ms vs 修复前 ${tOld.toFixed(0)}ms` +
      `（比值 ${(tOld / tNew).toFixed(1)}×）—— 二次方可能回来了`)
  })
})
