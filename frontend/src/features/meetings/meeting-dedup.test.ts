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
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { dedupeSegmentText, dedupeSegments, dedupeTranscriptParagraphs, renderTranscript } from './meeting-dedup.ts'

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
