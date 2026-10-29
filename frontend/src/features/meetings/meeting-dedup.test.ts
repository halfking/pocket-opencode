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
import { readdirSync, readFileSync, writeFileSync, mkdtempSync, rmSync } from 'node:fs'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { dirname, join, relative } from 'node:path'
import { tmpdir } from 'node:os'
import { build } from 'esbuild'
import { dedupeSegmentText, dedupeSegments, dedupeStep, dedupeTranscriptParagraphs, likelyAudioOverlap, renderTranscript, createTranscriptRenderer } from './meeting-dedup.ts'
import {
  shouldPersistTranscript,
  meetingListSql,
  TRANSCRIPT_PERSIST_MIN_GROWTH_BYTES,
  TRANSCRIPT_LIST_PREVIEW_CHARS,
} from './meeting-transcript-cache.ts'


// ★ 写入口的路径归一（2026-10-22 Windows 实测修掉）
//
//   事故形态：把 Windows 绝对路径**裸插**进单引号字符串字面量 ⇒
//   反斜杠开头的 w / p / f / n 全被 JS 解析成转义序列
//   ⇒ 路径被吃成 "C:workspacepocket-opencode" + 换行 + "ote-todo-persist.ts"
//   ⇒ esbuild 解析不到 ⇒ before 钩子抛错 ⇒ 整组子用例 cancelledByParent
//   ⇒ Linux 上路径是 "/workspace/..."，没有反斜杠 ⇒ **Linux 一直绿**
//   ⇒ 这是 Windows 专属红：不阻塞 CI，但会让 Windows 上的本地读数完全失真。
//
//   修法两步（缺一不可）：
//   ① 正斜杠化：Windows 的 fs 与 esbuild 都认 "C:/a/b" 这种形态
//   ② JSON.stringify：做 JS 字符串字面量转义 ⇒ 路径里再有引号/反斜杠也不会破
//
//   ⚠ 归一在**写进入口**（不是逐个路径改对）—— 与 D6–D10、§88 同族纪律。
//   ⚠ 本 helper **不带 TypeScript 注解**：本仓有用例是 .mjs（纯 JS），
//     写上参数类型注解会直接 SyntaxError（本轮实测踩到）。
//   ⚠ 注释里**绝不能出现反引号**：Node 的 TS stripper 会把 // 注释里的
//     反引号当成模板串开头 ⇒ 后面整行被当代码解析（本轮实测踩到）。
function asModulePath(p) {
  return JSON.stringify(String(p).replace(/\\/g, '/'))
}

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
    // ⚠⚠ §932：这条断言的**指向**变了，**语义没变**。
    //
    // 旧形态：`dedupeSegments(segments.value)`
    // 新形态（§932）：优先用**提供方**给的去重后段，拿不到才回退
    //   `const rawSegments = segments.value` → `provided ?? dedupeSegments(rawSegments)`
    // ⇒ 这里认**两种**形态：语义仍是「喂给 LLM 的段来自**去重后**的数组」
    //   （提供方那份与整场去重**逐字相同**，由 N 组的行为门钉住）。
    assert.match(
      s, /dedupeSegments\(\s*(?:segments\.value|rawSegments)\s*\)/,
      '摘要/推荐拿到的仍是含重复的 segments',
    )
    // ⚠⚠ §924：这条断言的**指向**变了，**语义没变**。
    //
    // 旧形态：`summarize(id, cleanSegments, …)` / `recommend(id, cleanSegments, …)`
    //   —— 载荷是「去重后的**全量**」。
    // 新形态（§924）：服务端契约是「已有摘要 + **新增转写**」⇒ 载荷变成
    //   「从去重后的全量里**选出来**的增量 / 有界尾部」
    //   ⇒ 字面量 `cleanSegments` 不再直接出现在实参位上。
    //
    // ⇒ 但这道门要钉的**仍然是同一件事**：**喂给 LLM 的段必须来自去重后的数组**。
    // ⇒ 所以这里钉的是**来源链**（三段），而不是某个字面量：
    //   cleanSegments = dedupeSegments(segments.value)   ← 去重后的数组
    //   selection     = selectSummarySegments(cleanSegments, …)  ← 从它里面选
    //   payload       = selection.segments               ← 送出去的就是选出来的
    assert.match(
      s, /const cleanSegments = (?:provided \?\? )?dedupeSegments\(\s*(?:segments\.value|rawSegments)\s*\)/,
      '去重后的数组不再是 cleanSegments',
    )
    assert.match(
      s, /const selection = selectSummarySegments\(cleanSegments, sentSegmentIds\)/,
      '摘要载荷不再是从 cleanSegments 里选出来的',
    )
    assert.match(
      s, /const payloadSegments = selection\.segments/,
      '送进 summarize 的不再是选出来的那份',
    )
    assert.match(
      s, /summarize\(\s*\n\s*id,\s*\n\s*payloadSegments/,
      'summarize 未使用选出来的（去重后的）增量段',
    )
    assert.match(
      s, /recommend\(id, selectRecommendSegments\(cleanSegments\)/,
      'recommend 未使用去重后的 segments',
    )
  })

  it('★★ 负控：把载荷改回「未经去重的 segments」必须判红', () => {
    // ⛔ 只看「改完是绿的」不能证明这道门有牙 —— 必须让它红一次。
    const s = read('../../composables/useLiveSummary.ts')
    // 同形变异：只把载荷的**来源**换回未去重的那份（其余一律不动）。
    const mutated = s.replace(
      'const payloadSegments = selection.segments',
      'const payloadSegments = segments.value',
    )
    assert.notEqual(mutated, s, '变异没落盘 ⇒ 这条负控不算数')
    assert.ok(
      !/const payloadSegments = selection\.segments/.test(mutated),
      '负控前提失效：变异没有真的改掉来源',
    )
    assert.match(
      s, /const payloadSegments = selection\.segments/,
      '判据前提失效：真实源码不满足「载荷来自选出来的那份」',
    )
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
    // ⚠⚠ §932：这条断言的**指向**变了，**语义没变**。
    //
    // 旧形态：`displaySegments = computed(() => dedupeSegments(…)`
    // 新形态（§932）：computed 体内「先问提供方、拿不到才回退整场去重」
    // ⇒ 详情页仍然「在源头去重」，只是那份去重结果来自 runtime 的增量渲染器
    //   （与整场去重**逐字相同**，由 N 组的行为门钉住）。
    //
    // ⇒ 这里认**两种**形态 —— 语义（详情页在源头去重）在两种形态下都成立。
    const oldForm = /displaySegments = computed\(\(\) => dedupeSegments\(/.test(s)
    const newForm = /dedupeSegments\s*\(/.test(computedBody(stripComments(s), 'displaySegments'))
    assert.ok(oldForm || newForm, '详情页未在源头去重')
  })

  it('★★ 负控：把「源头去重」摘掉必须判红（D3 + D6 改指向后配套）', () => {
    // ⛔ §932 改了 D3 / D6 的**指向** ⇒ 必须让它们**各红一次**，
    //   否则「认两种形态」可能变成「什么都认」⇒ 门就没牙了。
    const live = read('../../composables/useLiveSummary.ts')
    const detail = read('MeetingDetailView.vue')

    // ① D3：把去重整个摘掉（`cleanSegments` 直接等于原始数组）
    const liveMutated = live.replace(
      'const cleanSegments = provided ?? dedupeSegments(rawSegments)',
      'const cleanSegments = rawSegments',
    )
    assert.notEqual(liveMutated, live, '变异没落盘 ⇒ 这条负控不算数')
    assert.ok(
      !/const cleanSegments = (?:provided \?\? )?dedupeSegments\(\s*(?:segments\.value|rawSegments)\s*\)/.test(liveMutated),
      '负控前提失效：变异没有真的把去重摘掉（D3 仍然绿 ⇒ 指向改宽了）',
    )
    assert.match(
      live,
      /const cleanSegments = (?:provided \?\? )?dedupeSegments\(\s*(?:segments\.value|rawSegments)\s*\)/,
      '判据前提失效：真实源码不满足「cleanSegments 来自去重后的数组」',
    )

    // ② D6：把界面那列的去重摘掉（直接返回原始段）
    const detailMutated = detail.replace(
      'return dedupeSegments(live.length ? live : storedSegments.value)',
      'return live.length ? live : storedSegments.value',
    )
    assert.notEqual(detailMutated, detail, '变异没落盘 ⇒ 这条负控不算数')
    const oldFormMutated = /displaySegments = computed\(\(\) => dedupeSegments\(/.test(detailMutated)
    const newFormMutated = /dedupeSegments\s*\(/.test(computedBody(stripComments(detailMutated), 'displaySegments'))
    assert.ok(
      !(oldFormMutated || newFormMutated),
      '负控前提失效：变异没有真的把界面那列的去重摘掉（D6 仍然绿 ⇒ 指向改宽了）',
    )
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
          out.push({ file: relative(SRC_ROOT, f).replace(/\\/g, '/'), line: i + 1, text: line.trim() })
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

  it('★ 每一处调用都必须把**去重渲染**的结果写进去', () => {
    // ⚠ §931：写法有两种 —— 直接 `renderTranscript(...)`，
    //   或 §931 的**惰性源** `xxx.source()`（正文只在真要落库时才渲染）。
    //   两者都是「去重后的渲染」⇒ **语义不变，只是指向变了**。
    const DEDUPED = /updateTranscript\([^,]*,\s*(renderTranscript\(|[^,]*\.source\(\))/
    const bad = callSites().filter((s) => !DEDUPED.test(s.text))
    assert.deepEqual(
      bad.map((b) => `${b.file}:${b.line}  ${b.text}`),
      [],
      '这些写入点没有走去重渲染 ⇒ 重复会原样落库、默认展示、并流进 LLM 提示词。\n' +
        '修法：第二个实参传 renderTranscript(...) 的返回值，或 §931 的惰性源 xxx.source()。',
    )
  })

  it('负控：直接传原始拼接的写入点必须被上面那条判红', () => {
    // 真实存在过的坏写法（§438 之前的 appendText 形态）。
    const legacy = 'await updateTranscript(meetingId, this.segments.value.map((s) => `[${s.speakerLabel}] ${s.text}`).join("\\n"))'
    const DEDUPED = /updateTranscript\([^,]*,\s*(renderTranscript\(|[^,]*\.source\(\))/
    assert.ok(!DEDUPED.test(legacy),
      '负控样本必须通不过上面那条判据，否则那道判据恒绿')
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
          out.push(`${relative(SRC_ROOT, f).replace(/\\/g, '/')}:${src.slice(0, at).split('\n').length}`)
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

/**
 * L · §925 `local_meetings.transcript` 这一列是**派生缓存**，不是事实源
 *
 * ── 为什么放在这个文件 ──
 *
 * J/K 两组已经在守「谁在写 `updateTranscript`、写的是不是去重后的渲染」，
 * ⇒ 这一列的**写入纪律**归本组；本组只加**新的那一维**：它怎么被维护、怎么被读。
 *
 * ── 缺陷本体（实测）──
 *
 * `local_meeting_segments` 是事实源（每段一行）；
 * `local_meetings.transcript` 是它的派生缓存（整场转写拼成一段文本）。
 * ⚠ 而它此前是**当事实源在维护**的：
 *   · 写入：每新增一段就把「到目前为止的整场转写」**整段重写** ⇒ 写入量 O(时长²)
 *     （真 SQLite + 真实 ASR 语料实测：1952 段 = 8 小时 ⇒ 落库 **376.3 MB**、
 *      中位纯写 **17.3 s**；节流后 **2.4 MB** / **1.44 s**）。
 *   · 读取：`listMeetings` 是 `SELECT *` ⇒ 每次列表都把每场会的**整场转写 + 整场精校稿**读出来
 *     （30 场 × (25 KB + 25 KB) = **1.45 MB**，而界面只要 **≈10.5 KB**：30 × 120 字预览；
 *      有界片段后 **85.7 KB**）。
 *
 * ⚠ 修法把「风险落在慢的那一侧」：
 *   节流是**显式 opt-in**（`{ throttled: true }`）⇒ 新增写入点忘了声明 ⇒ 只是多写几次，
 *   **不会丢内容**（默认永远落库）。
 */
describe('L · §925 转写列是派生缓存：写入节流 + 列表只取有界片段', () => {
  const STORE_SRC = read('meetings-store.ts')

  // ── 行为门：拿**真**的 `updateTranscript` / `listMeetings` 跑（把 localDB 换成内存假件）──
  //
  // ★ 为什么这两条**不能**只做源码扫描：
  //   §925 的第一版接线判据是「函数名出现过」⇒ 变异 **M8**
  //   （`if (false && !shouldPersist(...))` ⇒ 永远落库）**漏网**
  //   ⇒ 「按写法找」= 假阴性 ⇒ 必须行为化。
  //   （做法与 `__tests__/ingest-behavior.test.mjs` 同款：esbuild 打包期替掉 I/O。）
  //
  // ⚠ §931：加载器已**提到模块级**（`loadStoreModule`）—— M 组要用同一份
  //   （§3 的教训：同一段机制抄两份 ⇒ 两份必然漂）。

  it('★★ 行为：真的 updateTranscript 在**节流**下不写库、在**默认**下写库', async () => {
    const { updateTranscript } = await loadStoreModule()
    S925_WRITES.length = 0
    // ① 默认（不声明 throttled）⇒ **必须**写（用户的内容不能丢）
    await updateTranscript('m-default', '短短的一段')
    assert.equal(S925_WRITES.length, 1, '默认路径没有写库 ⇒ 用户的内容会丢')
    assert.ok(
      String(S925_WRITES[0].sql).includes('UPDATE local_meetings SET transcript = ?'),
      `写库用的不是那列：${S925_WRITES[0].sql}`,
    )
    // ② 节流：第一次**必须**写（否则短会议那一列永远是空的）
    S925_WRITES.length = 0
    await updateTranscript('m-thr', 'x'.repeat(1000), { throttled: true })
    assert.equal(S925_WRITES.length, 1, '节流路径的第一次没落库 ⇒ 短会议的预览会整行空白')
    // ③ 节流：增长不足阈值 ⇒ **必须不写**（这一条是 M8 的死因）
    S925_WRITES.length = 0
    await updateTranscript('m-thr', `${'x'.repeat(1000)}y`, { throttled: true })
    assert.equal(S925_WRITES.length, 0,
      '增长不到阈值却写库了 ⇒ 节流没生效 ⇒ 写入量回到 O(时长²)')
    // ④ 节流：增长超过阈值 ⇒ 必须写
    S925_WRITES.length = 0
    await updateTranscript('m-thr', `${'x'.repeat(1000)}${'z'.repeat(TRANSCRIPT_PERSIST_MIN_GROWTH_BYTES)}`, { throttled: true })
    assert.equal(S925_WRITES.length, 1, '超过阈值却没落库 ⇒ 转写会一直落后')
  })

  it('★★ 行为：真的 listMeetings 发出去的 SQL 必须**只取有界片段**', async () => {
    const { listMeetings } = await loadStoreModule()
    S925_QUERIES.length = 0
    await listMeetings(30)
    assert.equal(S925_QUERIES.length, 1, 'listMeetings 没发查询（桩的形状变了？）')
    const sql = S925_QUERIES[0].sql
    assert.ok(!/SELECT\s+\*/i.test(sql), `列表 SQL 又是 SELECT *：${sql}`)
    assert.ok(
      sql.includes(`substr(transcript, 1, ${TRANSCRIPT_LIST_PREVIEW_CHARS}) AS transcript`),
      `列表 SQL 没有取有界的转写片段：${sql}`,
    )
    assert.ok(
      sql.includes(`substr(refined_transcript, 1, ${TRANSCRIPT_LIST_PREVIEW_CHARS}) AS refined_transcript`),
      `列表 SQL 里精校稿还是整列读：${sql}`,
    )
  })

  it('★★ 量具自证：扫描用的两个助手必须**真的有牙**（否则上面那些扫描判据是假绿）', () => {
    // ① `stripComments` 必须能剥掉注释里的 SELECT *（本节踩过：注释里写着「此前是 SELECT *」）。
    assert.ok(
      !/SELECT\s+\*/i.test(stripComments('const q = 1 // 此前是 SELECT *\nconst r = 2')),
      'stripComments 剥不掉行注释 ⇒ 扫描会把说明文字当成真 SQL（假红）',
    )
    assert.ok(
      !/SELECT\s+\*/i.test(stripComments('/* 此前是 SELECT * */ const q = 1')),
      'stripComments 剥不掉块注释 ⇒ 同上',
    )
    // ② 但它**不许**把字符串字面量里的内容也剥掉
    //    （否则「SQL 文本里真的写了 SELECT *」这种真缺陷会被吞掉 ⇒ 假绿）。
    assert.ok(
      /SELECT\s+\*/i.test(stripComments("const sql = 'SELECT * FROM t'")),
      'stripComments 把字符串字面量也剥了 ⇒ 真缺陷会被吞掉（假绿）',
    )
    // ③ `functionBody` 必须绕开**参数里的类型字面量**
    //    （`opts?: { archived?: boolean }` 的 `{` 不是函数体起点 —— 本节踩过）。
    const sample = 'function f(a: number, opts?: { x?: boolean }): void { return 1 }'
    const body = functionBody(sample, 'f')
    assert.ok(body.includes('return 1'), `functionBody 切错了函数体：${JSON.stringify(body)}`)
    assert.ok(!body.includes('archived'), `functionBody 把类型字面量当成了函数体：${JSON.stringify(body)}`)
    // ④ **正则字面量**里可以有反引号（`/[#*_>`~\[\]()]/g`）——
    //    不认正则的扫描器会把那个反引号当成「字符串开始」⇒ 后面一大段被吞掉
    //    ⇒ 括号配平失败 ⇒ 判据**假红**（本节踩过：preview 就在 `NotesHubView.vue` 里）。
    const reSample = 'function g(): string { const r = /[#*_>`~]/g; return String(r) }'
    const reBody = functionBody(reSample, 'g')
    assert.ok(reBody.includes('return String(r)'), `functionBody 把正则当成了字符串：${JSON.stringify(reBody)}`)
    //    ⇒ 模板串同理（`${…}` 里有花括号，不能参与配平）。
    const tplSample = 'function h(): string { return `${a({ b: 1 })}` }'
    assert.ok(functionBody(tplSample, 'h').includes('return'), 'functionBody 切错了模板串函数体')
  })

  it('★★ 安全侧：默认（不声明 throttled）必须**立刻落库**', () => {
    // ⭐ 这是本组最重要的一条：节流是 opt-in ⇒ 默认的那条路不能被节流。
    assert.equal(
      shouldPersistTranscript({ lastPersistedBytes: 10_000, nextBytes: 10_001 }),
      true,
      '默认路径被节流了 ⇒ 将来新增一个写入点忘了声明，用户的内容可能不落库',
    )
    assert.equal(
      shouldPersistTranscript({ lastPersistedBytes: 10_000, nextBytes: 10_001, throttled: false }),
      true,
      '显式 throttled:false 也必须立刻落库',
    )
  })

  it('★ 节流路径：第一次一定要落库（短会议不能整列都是空的）', () => {
    assert.equal(
      shouldPersistTranscript({ lastPersistedBytes: 0, nextBytes: 200, throttled: true }),
      true,
      '第一次没落库 ⇒ 一场短会议（转写不到阈值）的这一列永远是空的 ⇒ 预览整行空白',
    )
  })

  it('★★ 节流路径：增长不到阈值不落库，超过阈值才落库', () => {
    const T = TRANSCRIPT_PERSIST_MIN_GROWTH_BYTES
    assert.equal(
      shouldPersistTranscript({ lastPersistedBytes: 100_000, nextBytes: 100_000 + T - 1, throttled: true }),
      false,
      '差一个字节就落库 ⇒ 节流形同虚设',
    )
    assert.equal(
      shouldPersistTranscript({ lastPersistedBytes: 100_000, nextBytes: 100_000 + T, throttled: true }),
      true,
      '正好到阈值却没落库 ⇒ 阈值判错了',
    )
  })

  it('★ 节流路径：转写被**整份替换**（变短）时也要落库', () => {
    // 整场重转的结果可能比原来的分段转写短 ⇒ 差值是负数 ⇒ 用绝对值。
    assert.equal(
      shouldPersistTranscript({
        lastPersistedBytes: 200_000,
        nextBytes: 200_000 - TRANSCRIPT_PERSIST_MIN_GROWTH_BYTES,
        throttled: true,
      }),
      true,
      '变短了却没落库 ⇒ 这一列会一直留着旧版本',
    )
  })

  it('★★ 性质：列表只取片段后，用户看到的预览**逐字不变**', () => {
    // ⚠⚠ 这是「省体积」类改动的**必要**证据（§924 立的纪律）：
    //   少读字节必须证明**用户看到的内容一个字符都不变**。
    //
    // ① 漂移守卫：预览的**真实**实现住在 `NotesHubView.vue` 里（不是共享模块）
    //   ⇒ 这里把它的**真实源码**抠出来核对 ⇒ 它一改，这条门立刻响（不会默默漂移）。
    const NOTES_SRC = read('../notes/NotesHubView.vue')
    const previewBody = functionBody(NOTES_SRC, 'preview')
    for (const needle of ['<[^>]*>', '[#*_>`~\\[\\]()]', '\\s+', 'slice(0, max)']) {
      assert.ok(previewBody.includes(needle), `preview 的实现变了（缺 ${needle}）—— 这条性质判据的前提没了，先修判据`)
    }
    assert.ok(/max\s*=\s*120/.test(NOTES_SRC), 'preview 的默认长度不是 120 —— 前提变了，先修判据')

    // ② 与 ① 核对过 ⇒ 本地这份 strip 与真实实现**等价**（只用于算「显示文本」）。
    const display = (s: string): string =>
      s.replace(/<[^>]*>/g, ' ').replace(/[#*_>`~\[\]()]/g, ' ').replace(/\s+/g, ' ').trim()

    // ③ 用**生产渲染器** `renderTranscript` 造真实形状的转写（含 markdown 噪声）。
    //    ⚠ 语料必须**超过** 512 字符，否则这条判据量不到任何东西（自己踩过）。
    const speeches = [
      '这周先把**登录**流程做完，<b>优先级</b>很高。',
      '好，我这边先出接口文档，#待办 记得同步给王强。',
      '收到，周三之前给结论，`日志` 采集也要一起看。',
      '那我们先看 error 再看 warn，别一上来就上全量。',
      '压测环境昨晚重启过，机器上只有 4 核，跑不动全量。',
      '那就在 2 核上跑 smoke，完整回归放到周五夜里。',
    ]
    const full = renderTranscript(
      Array.from({ length: 20 }, (_, i) => ({
        speakerLabel: ['张伟', '李娜', '王强'][i % 3],
        text: `${speeches[i % speeches.length]}（第 ${i + 1} 轮）`,
      })),
    )
    assert.ok(full.length > TRANSCRIPT_LIST_PREVIEW_CHARS, '样本太短，这条性质判据没意义')

    // ④ 关键性质：截到 512 字符之后，用户看到的**那一行**必须逐字不变。
    const shownFull = display(full).slice(0, 120)
    const shownTrunc = display(full.slice(0, TRANSCRIPT_LIST_PREVIEW_CHARS)).slice(0, 120)
    assert.equal(shownTrunc, shownFull, '截断后预览变了 ⇒ 用户会看到不一样的一行')
    //   ⇒ 而且不能**变短**（短了就是少给了内容）。
    assert.ok(
      display(full.slice(0, TRANSCRIPT_LIST_PREVIEW_CHARS)).length >= 120,
      '截断后连 120 字都不到 ⇒ 预览会短一截',
    )
    // ⚠ 口径：SQLite 的 `substr` 按**字符**（码点）切，JS 的 `slice` 按 **UTF-16 码元**切
    //   ⇒ 含 emoji 时两者差 1 个字符。本条只比较 120 字的前缀 ⇒ 不受影响。
  })

  it('★★ 列表查询不许再用 `SELECT *`（那会把整场转写读出来）', () => {
    for (const archived of [false, true]) {
      const sql = meetingListSql(archived)
      assert.ok(!/SELECT\s+\*/i.test(sql), `列表 SQL 又是 SELECT *：${sql}`)
      assert.ok(
        sql.includes(`substr(transcript, 1, ${TRANSCRIPT_LIST_PREVIEW_CHARS}) AS transcript`),
        `列表 SQL 没有取有界的转写片段：${sql}`,
      )
    }
  })

  it('★★ 列表查询里的**精校稿**也必须只取有界片段（它是同一类派生缓存）', () => {
    // ⚠ `refined_transcript` 与 `transcript` 是**同一类东西**（分段转写的派生），
    //   列表里的消费者**也只有**那一个 120 字预览 ⇒ 只修一半 ⇒ 读侧只降一半。
    const sql = meetingListSql(false)
    assert.ok(
      sql.includes(`substr(refined_transcript, 1, ${TRANSCRIPT_LIST_PREVIEW_CHARS}) AS refined_transcript`),
      `列表 SQL 里精校稿还是整列读：${sql}`,
    )
    // ⇒ 反向核对：SELECT 列表里**不许**出现「裸的」两列（那说明有人把它改回去了）。
    //   ⚠ 必须在**项**这一层核对（不是整条 SQL 上正则）——
    //     `AS refined_transcript` 里的那个 refined_transcript 是**别名**，不是裸列。
    const items = selectItems(sql)
    for (const bare of ['transcript', 'refined_transcript']) {
      assert.ok(!items.includes(bare), `列表 SQL 里还有裸的 ${bare} 列：${sql}`)
    }
  })

  it('★★ 列表查询的列必须**恰好**是 rowToMeeting 读的那些（防漏列）', () => {
    // ⭐ 这一条是「抄两份」的老毛病（§80）的解法：
    //   列清单在 SQL 里、字段名在 rowToMeeting 里 ⇒ 两份 ⇒ 必然漂移。
    // ⇒ 这里**互相核对**：少一列 ⇒ 列表里那个字段恒为 undefined（用户看到空标题之类）。
    const body = functionBody(STORE_SRC, 'rowToMeeting')
    const wanted = [...new Set([...body.matchAll(/\br\.(\w+)/g)].map((m) => m[1]))].sort()
    assert.ok(wanted.length >= 20, `只从 rowToMeeting 里认出 ${wanted.length} 个字段 —— 抽取器坏了`)
    const sql = meetingListSql(false)
    const got = selectColumns(sql).sort()
    assert.deepEqual(got, wanted,
      '列表 SQL 的列与 rowToMeeting 读的字段对不上 ⇒ 少列 = 列表里那个字段恒为 undefined')
  })

  it('★★ 接线：store 必须真的用上这两个决策', () => {
    const upd = functionBody(STORE_SRC, 'updateTranscript')
    assert.ok(upd.includes('shouldPersistTranscript('), 'updateTranscript 没有走节流决策')
    assert.ok(upd.includes('updateMeeting(id, { transcript }, db)'), 'updateTranscript 不再沿用发起租约写 transcript 列')
    const list = functionBody(STORE_SRC, 'listMeetings')
    assert.ok(list.includes('meetingListSql('), 'listMeetings 没有用会议列表 SQL')
    // ⚠⚠ 扫描前必须**剥注释**（本节自己踩过）：`listMeetings` 的注释里写着
    //   「此前是 `SELECT *`」—— 不剥就会把**说明文字**当成真的 SQL ⇒ 假红。
    assert.ok(!/SELECT\s+\*/i.test(stripComments(list)), 'listMeetings 里还有 SELECT *')
  })

  it('★★ 节流只能出现在「逐段追加」那两条路径上', () => {
    // ⚠ 这条是**枚举型**判据（只覆盖名字已经写下来的调用点）⇒ 它的缺口要说清：
    //   但**方向是安全的** —— 漏声明只会多写几次，**不会丢内容**。
    const sites: string[] = []
    for (const f of walkSrc()) {
      const src = readFileSync(f, 'utf8')
      if (src.includes('throttled: true')) sites.push(relative(SRC_ROOT, f).replace(/\\/g, '/'))
    }
    assert.deepEqual(sites.sort(), [
      'features/meetings/ingest-speech.ts',
      'native/recordingRuntime.ts',
    ], `节流出现在这些文件里：${sites.join(', ')} —— 只有「逐段追加」那两条路径可以节流`)
  })

  it('★★ 阈值必须同时满足两条**派生性质**（钉住的是性质，不是那个字面量）', () => {
    // ⚠⚠ 为什么要**派生性质**而不是「阈值必须等于 32768」：
    //   第一版判据全部用 `TRANSCRIPT_PERSIST_MIN_GROWTH_BYTES` 自己算期望值
    //   ⇒ **判据与实现同源** ⇒ 变异 M13「阈值调大 1000 倍」**照样全绿**（漏网）。
    //   ⇒ 这里改成两条**与实现无关**的量化性质（用 §925.2 的实测语料折算）。
    //
    // ① 够大：8 小时会议（转写 394.4 KB）的落库次数 ≤ 20 次
    //    ⇒ 阈值 ≥ 394.4 KB / 20 ≈ 19.7 KB
    const EIGHT_HOUR_TRANSCRIPT_BYTES = 394.4 * 1024
    assert.ok(
      TRANSCRIPT_PERSIST_MIN_GROWTH_BYTES >= 20 * 1024,
      `阈值 ${TRANSCRIPT_PERSIST_MIN_GROWTH_BYTES} B ⇒ 8 小时会议要落库 `
      + `${(EIGHT_HOUR_TRANSCRIPT_BYTES / TRANSCRIPT_PERSIST_MIN_GROWTH_BYTES).toFixed(0)} 次（>20 次）`
      + ' ⇒ 写入量回不到 O(时长) 的量级',
    )
    // ② 够小：录音期间的滞后 ≤ 1 小时语音
    //    ⇒ 实测语料 792.5 B/分 ⇒ 阈值 ≤ 40 KB ⇒ 滞后 ≤ 51.6 分钟
    const BYTES_PER_MINUTE = 792.5
    assert.ok(
      TRANSCRIPT_PERSIST_MIN_GROWTH_BYTES <= 40 * 1024,
      `阈值 ${TRANSCRIPT_PERSIST_MIN_GROWTH_BYTES} B ⇒ 录音期间滞后 `
      + `${(TRANSCRIPT_PERSIST_MIN_GROWTH_BYTES / BYTES_PER_MINUTE).toFixed(0)} 分钟（>1 小时）`
      + ' ⇒ 这一列在会议结束前就长时间不是「当前转写」',
    )
  })

  it('★★ 收尾必须**整篇**落库（否则这一列在会议结束时仍落后四十来分钟）', () => {
    // ⚠ 为什么必须有这一步：录音期间那一列是**节流**的 ⇒ 会议刚结束时它可能落后
    //   **几十 KB / 四十来分钟**的语音 ⇒ 「会议结束」就必须是一个明确的补齐点。
    // ⚠ 这条是**源码形状**判据（`stop()` 是类方法，node 里跑不到）⇒ 但它钉的是
    //   「**这个函数体里有一个不声明 throttled 的整篇写**」这个**具体形态**，
    //   而不只是「函数名出现过」（§925.5 的 M8 就是栽在后者上）。
    const RT = read('../../native/recordingRuntime.ts')
    const stop = methodBody(RT, 'stop')
    // ⚠ 前提登记：`recordingRuntime.ts` 里有**两个** `stop()`（会议那条 / 随手记那条）
    //   ⇒ 必须证明切到的是**会议**那条（它才有在途分段的等待）。
    assert.ok(stop.includes('inFlightSegments'),
      '切到的不是会议那条 stop()（随手记那条没有在途分段）—— 判据失效，先修判据')
    const writes = [...stop.matchAll(/updateTranscript\(([^)]*)\)/g)].map((m) => m[1])
    assert.ok(writes.length > 0, 'stop() 里没有 updateTranscript 调用 —— 收尾不补齐这一列')
    const forced = writes.filter((w) => !w.includes('throttled'))
    assert.ok(forced.length > 0,
      `stop() 里的转写写入都声明了 throttled（${writes.length} 处）⇒ 收尾不补齐这一列`)
    // ⇒ 且必须**有分段才写**（没分段时写空串会把这一列清空 ⇒ 误报「可恢复」）。
    assert.ok(
      /if\s*\(\s*this\.segments\.value\.length\s*\)/.test(stop),
      '收尾的整篇写入没有「有分段才写」的守卫 ⇒ 没有分段时会把这一列清空',
    )
  })

  it('★★ 详情页必须拿到**全文**（列表那一份是截断的，不许被当成正文）', () => {
    // ⚠ 列表 SQL 只取 512 字符 ⇒ 那个值**不能**当正文展示。
    //   详情页走 `getMeetingWithSegments` → `getMeeting`（**`SELECT *`**）⇒ 全文。
    // ⇒ 这里把这条**数据来源**钉住：将来有人为了让列表更快而把详情页也改成读列表那份
    //   ⇒ 用户会看到一份**被砍半**的精校正文（正是 `refine-retention-guard` 那道门防的事）。
    const VIEW = read('MeetingDetailView.vue')
    assert.ok(
      /getMeetingWithSegments\s*\(/.test(VIEW),
      '详情页不再走 getMeetingWithSegments ⇒ 它可能改读列表那份（截断的）',
    )
    const getMeetingBody = functionBody(STORE_SRC, 'getMeeting')
    assert.ok(
      /SELECT\s+\*/.test(getMeetingBody),
      'getMeeting 不再是 `SELECT *` ⇒ 详情页拿到的不是全文',
    )
  })

  it('★★ 负控：默认变成节流时上面那条必须判红', () => {
    // 与真实实现**同形**的样本：只是把默认值反过来。
    const inverted = (i: { lastPersistedBytes: number; nextBytes: number; throttled?: boolean }): boolean => {
      if (i.throttled === false) return true
      if (i.lastPersistedBytes <= 0) return true
      return Math.abs(i.nextBytes - i.lastPersistedBytes) >= TRANSCRIPT_PERSIST_MIN_GROWTH_BYTES
    }
    assert.equal(inverted({ lastPersistedBytes: 10_000, nextBytes: 10_001 }), false,
      '负控前提失效：样本竟然没有反转默认行为')
    assert.equal(shouldPersistTranscript({ lastPersistedBytes: 10_000, nextBytes: 10_001 }), true,
      '真实现与负控同形 ⇒ 量具坏了，不是判据坏了')
  })

  it('★★ 负控：列表 SQL 里的转写片段被去掉时必须判红', () => {
    const broken = meetingListSql(false).replace(/substr\(transcript, 1, \d+\) AS transcript/, 'transcript')
    assert.notEqual(broken, meetingListSql(false), '负控样本没改成功')
    assert.ok(!/substr\(transcript, 1, \d+\) AS transcript/.test(broken),
      '负控失败：去掉片段后仍然判绿 ⇒ 上面那条判据没有牙')
  })
})

/**
 * ── 一个够用的 JS 记号扫描器（门禁用）──
 *
 * ⚠⚠ 为什么不能只认「字符串」：**正则字面量**里可以有引号和反引号。
 *   本节踩过：`/[#*_>`~\[\]()]/g` 里那个**反引号**被当成「字符串开始」⇒
 *   后面一大段被吞掉 ⇒ 括号配平失败 ⇒ 判据假红。
 * ⇒ 所以下面这套同时认：**行注释 / 块注释 / 字符串 / 模板串 / 正则字面量**。
 *
 * ⚠ 记号扫描的返回值约定：一律返回**记号最后一个字符的下标**（含），
 *   「此处不是记号」时返回**原下标**（调用方据此判断「没跳过任何东西」）。
 */

/** 正则可以跟在这些**关键字**后面（`return /re/` 这种）。 */
const REGEX_AFTER_WORDS = new Set([
  'return', 'typeof', 'case', 'in', 'of', 'new', 'delete', 'void',
  'instanceof', 'do', 'else', 'yield', 'await', 'throw',
])

/** `src[i]` 处的 `/` 是正则字面量的开头吗（还是除号）。 */
function isRegexStart(src: string, i: number): boolean {
  let k = i - 1
  while (k >= 0 && /\s/.test(src[k])) k--
  if (k < 0) return true
  const c = src[k]
  if (c === ')' || c === ']') return false
  if (/[A-Za-z0-9_$]/.test(c)) {
    let w = k
    while (w >= 0 && /[A-Za-z0-9_$]/.test(src[w])) w--
    return REGEX_AFTER_WORDS.has(src.slice(w + 1, k + 1))
  }
  return true
}

/** 跳过引号字符串（返回**结束引号**的下标）。 */
function skipQuoted(src: string, i: number): number {
  const q = src[i]
  let k = i + 1
  while (k < src.length) {
    if (src[k] === '\\') { k += 2; continue }
    if (src[k] === q) return k
    if (src[k] === '\n') return k // 没闭合 ⇒ 止于行尾
    k++
  }
  return src.length - 1
}

/** 跳过模板串（含 `${…}` 里的嵌套记号与嵌套花括号）。 */
function skipTemplate(src: string, i: number): number {
  let k = i + 1
  while (k < src.length) {
    const c = src[k]
    if (c === '\\') { k += 2; continue }
    if (c === '`') return k
    if (c === '$' && src[k + 1] === '{') {
      let depth = 1
      k += 2
      while (k < src.length && depth > 0) {
        const t = skipToken(src, k)
        if (t !== k) { k = t + 1; continue }
        if (src[k] === '{') depth++
        else if (src[k] === '}') depth--
        k++
      }
      continue
    }
    k++
  }
  return src.length - 1
}

/** 跳过正则字面量（`[...]` 里的 `/` 不结束正则）。 */
function skipRegex(src: string, i: number): number {
  let k = i + 1
  let inClass = false
  while (k < src.length) {
    const c = src[k]
    if (c === '\\') { k += 2; continue }
    if (c === '\n') return k - 1 // 没闭合 ⇒ 多半不是正则
    if (inClass) { if (c === ']') inClass = false; k++; continue }
    if (c === '[') { inClass = true; k++; continue }
    if (c === '/') {
      k++
      while (k < src.length && /[a-z]/i.test(src[k])) k++
      return k - 1
    }
    k++
  }
  return src.length - 1
}

/** 从 `i` 开始跳过一个记号；`i` 处不是记号则**原样返回** `i`。 */
function skipToken(src: string, i: number): number {
  const c2 = src.slice(i, i + 2)
  if (c2 === '//') { const nl = src.indexOf('\n', i); return nl < 0 ? src.length - 1 : nl }
  if (c2 === '/*') { const e = src.indexOf('*/', i + 2); return e < 0 ? src.length - 1 : e + 1 }
  const c = src[i]
  if (c === '"' || c === "'") return skipQuoted(src, i)
  if (c === '`') return skipTemplate(src, i)
  if (c === '/' && isRegexStart(src, i)) return skipRegex(src, i)
  return i
}

/**
 * 取一次**调用**的实参文本（`useLiveSummary(...)` 的括号里那段）。
 *
 * ⚠ 不能用 `bodyFrom`（那是取**函数体**的）⇒ 调用点后面没有 `{` ⇒ 它会失败。
 */
function callArgs(src: string, callName: string): string {
  const at = src.indexOf(`${callName}(`)
  assert.ok(at >= 0, `解析不到 ${callName}( —— 判据失效，先修判据`)
  let depth = 0
  let close = -1
  for (let i = at + callName.length; i < src.length; i++) {
    const t = skipToken(src, i)
    if (t !== i) { i = t; continue }
    const c = src[i]
    if (c === '(') depth++
    else if (c === ')') { depth--; if (depth === 0) { close = i; break } }
  }
  assert.ok(close > 0, `${callName}( 的参数列表不配平 —— 判据失效，先修判据`)
  return src.slice(at + callName.length + 1, close)
}

/** 取 `const X = computed(() => { … })` 的函数体。 */
function computedBody(src: string, name: string): string {
  const at = src.indexOf(`const ${name} = computed(`)
  assert.ok(at >= 0, `解析不到 ${name} 这个 computed —— 判据失效，先修判据`)
  const open = src.indexOf('{', at)
  assert.ok(open > 0, `找不到 ${name} 的函数体起点 —— 判据失效，先修判据`)
  let depth = 0
  for (let k = open; k < src.length; k++) {
    const t = skipToken(src, k)
    if (t !== k) { k = t; continue }
    const c = src[k]
    if (c === '{') depth++
    else if (c === '}') { depth--; if (depth === 0) return src.slice(open + 1, k) }
  }
  throw new Error(`${name} 的括号不配平`)
}

/**
 * 取某个函数体（**括号配平**，不能用正则猜边界 —— §439 记过这个坑）。
 *
 * ⚠⚠ 参数里的**类型字面量**会骗过「第一个 `{` 就是函数体」这种写法：
 *   `listMeetings(limit = 30, opts?: { archived?: boolean })` 的第一个 `{` 是**类型**的，
 *   按它切出来的「函数体」是 ` archived?: boolean; offset?: number ` ⇒ 判据全部读到垃圾。
 * ⇒ 这里先扫到**参数列表的右括号**（按配平、跳过记号），再找它后面的第一个 `{`。
 *
 * ⚠ 已知边界：返回类型里带花括号的函数（如 `(): { a: number } {`）会切错。
 *   本组扫的三个函数（`rowToMeeting` / `updateTranscript` / `listMeetings`）与
 *   `preview` 的返回类型都是简单类型 ⇒ 不触发。
 */
function functionBody(src: string, name: string): string {
  // ⚠ 锚点带**左括号**：`function getMeeting(` 不会误命中 `function getMeetingWithSegments(`。
  const at = src.indexOf(`function ${name}(`)
  // ⚠ 判据用 `at >= 0`（不是 `> 0`）：函数在**下标 0** 处也是找得到
  //   （自证样本 `function f(...)` 就是）⇒ 用 `> 0` 会把「在开头」误判成「找不到」。
  assert.ok(at >= 0, `解析不到 ${name} —— 判据失效，先修判据`)
  return bodyFrom(src, at, name)
}

/**
 * 取**类方法**的体（`async stop(): … {` 这种**没有** `function` 关键字的）。
 *
 * ⚠ 为什么单列一个：类方法与函数声明的**声明形态**不同
 *   （`function f(...)` vs `  async stop(...)`）⇒ 用 `indexOf('function stop')` 找不到。
 * ⚠ 同一个类里可能有**同名方法**（`recordingRuntime.ts` 里 `stop()` 有两处：
 *   会议那条与随手记那条）⇒ 取**第 occurrence 个**，并在调用处做**前提登记**。
 */
function methodBody(src: string, name: string, occurrence = 0): string {
  const re = new RegExp(`^[ \\t]*(?:async[ \\t]+)?${name}[ \\t]*\\(`, 'gm')
  const hits: number[] = []
  for (let m = re.exec(src); m; m = re.exec(src)) hits.push(m.index)
  assert.ok(hits.length > occurrence, `解析不到第 ${occurrence} 个 ${name} 方法 —— 判据失效，先修判据`)
  return bodyFrom(src, hits[occurrence], `${name}（第 ${occurrence} 个）`)
}

/**
 * 取**带修饰符**的方法体（`private async xxx(` / `public xxx(`）。
 *
 * ⚠ 为什么单列一个：`methodBody` 的正则是 `^[ \t]*(?:async[ \t]+)?NAME\s*\(`，
 *   它**不认** `private` / `public` / `protected` / `static` 这些修饰符
 *   ⇒ `private async processSegment(` 那种声明会**找不到**。
 * ⇒ 本函数把修饰符列进可选前缀。
 */
function methodBodyWithModifiers(src: string, name: string): string {
  const re = new RegExp(
    `^[ \\t]*(?:(?:private|public|protected|static|readonly)\\s+)*(?:async\\s+)?${name}\\s*\\(`,
    'm',
  )
  const m = re.exec(src)
  assert.ok(m, `解析不到 ${name} 方法（带修饰符的形态）—— 判据失效，先修判据`)
  return bodyFrom(src, m.index, name)
}

/** 从声明处开始取函数体（**括号配平**，跳过记号）。 */
function bodyFrom(src: string, declIndex: number, label: string): string {  // ① 参数列表的右括号
  let i = src.indexOf('(', declIndex)
  assert.ok(i > 0, `${label} 没有参数列表 —— 判据失效，先修判据`)
  let depth = 0
  let close = -1
  for (; i < src.length; i++) {
    const t = skipToken(src, i)
    if (t !== i) { i = t; continue }
    const c = src[i]
    if (c === '(') depth++
    else if (c === ')') {
      depth--
      if (depth === 0) { close = i; break }
    }
  }
  assert.ok(close > 0, `${label} 的参数列表不配平 —— 判据失效，先修判据`)
  // ② 跳过返回类型，找到**顶层**的 `{`（函数体起点）
  const open = findBodyBrace(src, close)
  assert.ok(open > 0, `找不到 ${label} 的函数体起点 —— 判据失效，先修判据`)
  depth = 0
  for (let k = open; k < src.length; k++) {
    const t = skipToken(src, k)
    if (t !== k) { k = t; continue }
    const c = src[k]
    if (c === '{') depth++
    else if (c === '}') {
      depth--
      if (depth === 0) return src.slice(open + 1, k)
    }
  }
  throw new Error(`${label} 的括号不配平`)
}

/**
 * 从 `from` 开始找**顶层**的 `{`（函数体起点）。
 *
 * ⚠⚠ **不能**用「`)` 之后的第一个 `{`」：返回类型里可能有花括号 ——
 *   `async stop(): Promise<{ audioPath: string; … } | null> {` 的第一个 `{`
 *   是**返回类型**的 ⇒ 按它切出来的「函数体」是 ` audioPath: string; … ` ⇒ 判据读到垃圾
 *   （本节实测踩到：`stop()` 有两个同名方法，靠 `inFlightSegments` 前提登记才发现切错了）。
 * ⇒ 这里按**括号配平**跳过嵌套组（`{}` / `<>` / `()` / `[]`），取**顶层**那个 `{`。
 */
function findBodyBrace(src: string, from: number): number {
  const stack: string[] = []
  let i = from
  while (i < src.length) {
    const t = skipToken(src, i)
    if (t !== i) { i = t + 1; continue }
    const c = src[i]
    // `=>` 里的 `>` 不是泛型的收尾 ⇒ 必须当成两个字符跳过。
    if (c === '=' && src[i + 1] === '>') { i += 2; continue }
    if (c === '{' || c === '<' || c === '(' || c === '[') {
      if (c === '{' && stack.length === 0) return i // 顶层花括号 = 函数体
      stack.push(c)
      i++
      continue
    }
    if (c === '}' || c === '>' || c === ')' || c === ']') {
      stack.pop()
      i++
      continue
    }
    i++
  }
  return -1
}

/**
 * 剥掉注释（行注释 + 块注释），**保留字符串/模板串/正则字面量**。
 *
 * ⚠⚠ 本节自己踩过这个坑：`listMeetings` 的**注释**里写着「此前是 `SELECT *`」，
 *   不剥注释的扫描会把**说明文字**当成真的 SQL ⇒ 假红。
 *
 * ⚠ 反过来，字面量必须**原样保留**（不能被当成注释剥掉）——
 *   否则「SQL 文本里真的写了 SELECT *」这种真缺陷会被吞掉 ⇒ 假绿。
 */
function stripComments(src: string): string {
  let out = ''
  let i = 0
  while (i < src.length) {
    const c2 = src.slice(i, i + 2)
    if (c2 === '//') {
      while (i < src.length && src[i] !== '\n') i++
      continue
    }
    if (c2 === '/*') {
      const end = src.indexOf('*/', i + 2)
      i = end < 0 ? src.length : end + 2
      out += ' ' // 用空格占位，免得把注释两边的代码粘成一个 token
      continue
    }
    const c = src[i]
    if (c === '"' || c === "'" || c === '`') {
      const end = c === '`' ? skipTemplate(src, i) : skipQuoted(src, i)
      out += src.slice(i, end + 1)
      i = end + 1
      continue
    }
    if (c === '/' && isRegexStart(src, i)) {
      const end = skipRegex(src, i)
      out += src.slice(i, end + 1)
      i = end + 1
      continue
    }
    out += c
    i++
  }
  return out
}

/**
 * 遍历 `src/` 下的源文件（.ts / .vue，跳过测试文件）。
 *
 * ⚠ J/K 两组各自有一份同款遍历器（它们在各自的 describe 作用域里）。
 *   本组**再抄第三份**是明知的重复 ⇒ 但**不重构** J/K（那是另一节的判据，
 *   动它会扩大本节的改动面）⇒ 这里如实记一笔：**三份遍历器**，
 *   若将来要收编，请连 J/K 一起收（三处一起改才不会漂）。
 */
const SRC_ROOT = join(HERE, '..', '..')
function walkSrc(dir: string = SRC_ROOT, out: string[] = []): string[] {
  for (const e of readdirSync(dir, { withFileTypes: true })) {
    if (e.name === 'node_modules' || e.name === '.git') continue
    const p = join(dir, e.name)
    if (e.isDirectory()) walkSrc(p, out)
    else if (/\.(ts|vue)$/.test(e.name) && !/\.test\.ts$/.test(e.name)) out.push(p)
  }
  return out
}

/** 取 SELECT 列表的**原始项**（括号里的逗号不算分隔符）。 */
function selectItems(sql: string): string[] {
  const list = sql.slice(sql.indexOf('SELECT ') + 'SELECT '.length, sql.indexOf(' FROM '))
  const parts: string[] = []
  let depth = 0
  let cur = ''
  for (const c of list) {
    if (c === '(') depth++
    if (c === ')') depth--
    if (c === ',' && depth === 0) { parts.push(cur.trim()); cur = ''; continue }
    cur += c
  }
  if (cur.trim()) parts.push(cur.trim())
  return parts
}

/** 取 SELECT 列表的**输出列名**（括号里的逗号不算分隔符）。 */
function selectColumns(sql: string): string[] {
  return selectItems(sql).map((p) => {
    const as = p.toUpperCase().lastIndexOf(' AS ')
    return as >= 0 ? p.slice(as + 4).trim() : p
  })
}

// ═══════════════════════════════════════════════════════════════════════
// §931 提上来的模块级加载器（原来在 L 组的作用域里，M 组要用同一份）
// ═══════════════════════════════════════════════════════════════════════

const S925_WRITES: { sql: string; params: unknown }[] = []
const S925_QUERIES: { sql: string; params: unknown }[] = []
;(globalThis as unknown as Record<string, unknown>).__s925Writes = S925_WRITES
;(globalThis as unknown as Record<string, unknown>).__s925Queries = S925_QUERIES

let storeBundle: Promise<{ updateTranscript: Function; listMeetings: Function }> | null = null
function loadStoreModule() {
  if (storeBundle) return storeBundle
  storeBundle = (async () => {
    const work = mkdtempSync(join(tmpdir(), 'pocket-s925-'))
    const stub = join(work, 'local-db-stub.mjs')
    const recordingStub = join(work, 'recording-operation-stub.mjs')
    const syncStub = join(work, 'meeting-sync-stub.mjs')
    writeFileSync(stub, `
      export const localDB = {
        owner: {userId:'fixture',workspaceId:'fixture'}, assertCurrent() {},
        captureOperation() { return {owner:{principal:this.owner}} }, bindOperation() { return this },
        async run(sql, params) { globalThis.__s925Writes.push({ sql: String(sql), params }) },
        async query(sql, params) { globalThis.__s925Queries.push({ sql: String(sql), params }); return [] },
        async queryOne(sql, params) { globalThis.__s925Queries.push({ sql: String(sql), params }); return null },
      }`, 'utf8')
    writeFileSync(recordingStub, `import {localDB} from ${JSON.stringify(stub)};export const captureRecordingDb=()=>localDB`, 'utf8')
    writeFileSync(syncStub, `export const meetingPayload=()=>({});export const meetingPortalStore=()=>({commit(){throw Error('portable sync is outside this transcript-only seam')}})`, 'utf8')
    const entry = join(work, 'entry.ts')
    writeFileSync(entry, `export { updateTranscript, listMeetings } from ${asModulePath(join(HERE, 'meetings-store.ts'))}\n`, 'utf8')
    const outfile = join(work, 'out.mjs')
    await build({
      entryPoints: [entry],
      bundle: true,
      format: 'esm',
      platform: 'neutral',
      outfile,
      logLevel: 'error',
      plugins: [{
        name: 'stub-io',
        setup(b) {
          b.onResolve({ filter: /^\.\.\/\.\.\/native\/local-db$/ }, () => ({ path: stub }))
          b.onResolve({ filter: /^\.\/recording-operation$/ }, () => ({ path: recordingStub }))
          b.onResolve({ filter: /^\.\/meeting-sync-adapter\.ts$/ }, () => ({ path: syncStub }))
        },
      }],
    })
    const mod = await import(pathToFileURL(outfile).href)
    process.on('exit', () => { try { rmSync(work, { recursive: true, force: true }) } catch { /* 临时目录清理失败不影响判据结论 */ } })
    return mod as { updateTranscript: Function; listMeetings: Function }
  })()
  return storeBundle
}

/**
 * M · §931 转写列的**渲染**也必须有界：逐段追加那条路径不得每段整场重渲染
 *
 * ── 缺陷本体（实测，§931.1）──
 *
 * §925 把「每段整篇重写」改成了「按增长量节流」⇒ **写**的量从 O(时长²) 降到 O(时长)。
 * 但**渲染**没被节流：`recordingRuntime.appendText`（本地实时字幕）与
 * `ingest-speech.ts`（ASR 段）两条**逐段追加**的路径，每落一段就把**整场**
 * `renderTranscript(全部段)` 重新渲染一遍 —— 而那次渲染的结果在节流判定里
 * **绝大多数被直接丢弃**（8 小时会议真正落库 13 次）。
 *
 * ⚠⚠ 实测（**生产真函数** + 真实 ASR 语料，1952 段 = 8 小时）：
 *   · 单次全量渲染 = **O(n)**，每步 ~16–40 µs，**不随 n 增长**
 *     （链 vs 同长扁平串 1.0× / 4.2× / 1.0× ⇒ 不是 ConsString 展平）
 *   · 现状「每段渲染一次」= **O(n²)**：100 段 61×、200 段 122×、400 段 405×、
 *     800 段 174×（倍数 ≈ n/2）⇒ 8 小时外推 **~100 s 纯 CPU**（主线程）
 *
 * ⇒ 与 §925 同一族：**写**那一半修好了，**算**那一半没修。
 *
 * ── 本组钉的是什么 ──
 *
 * 钉「**渲染次数**」而不是「渲染有多快」—— 前者可测，后者只能靠基准。
 * ⇒ 核心那一条是**落库层**的行为：节流说不写时，那次 O(n) 的渲染**根本没发生**。
 */
/**
 * 造一段**有真实重叠**的语料（相邻段共享尾巴 ⇒ 去重真的会裁字）。
 *
 * ⚠⚠ 为什么语料是**逐段生成**的（而不是先造好一整段再循环）：
 *   §931 的快路径要求「数组**逐段增长**」这个形态（生产上就是如此：
 *   `appendText` 往尾巴上 push、`ingestSpeechBlob` 按 startMs 有序插）。
 *   若把**整段数组**反复传进 `appendAt`，那每次都是「非尾部追加」
 *   ⇒ 渲染器会整场重算 ⇒ **快路径根本没被走到**
 *   ⇒ 那时的「逐字相同」是**重建路径**的等价性，证明不了快路径。
 *   ⇒ 本节的等价性用例因此**逐段 push**（生产形态）。
 *
 * ⚠ §932：提到**模块级**（M 组与 N 组共用一份）——
 *   判据里的语料抄两份必然漂（§3 的教训），而 §932 恰恰要证明
 *   「两条路给出**逐字相同**的结果」⇒ 语料必须**同一份**。
 */
function mkSeg(i: number, tail = 6) {
  const text = `${i > 0 ? `第${i - 1}段新增的内容`.slice(-tail) : ''}第${i}段新增的内容`
  return { speakerLabel: i % 2 ? '乙' : '甲', text, startMs: i * 1000, endMs: i * 1000 + 800, origin: 'asr' as const }
}

describe('M · §931 逐段追加那条路径不得每段整场重渲染', () => {
  it('★ 前置自证：这份语料**真的踩到去重**（否则下面那些「逐字相同」恒成立）', () => {
    const segs = []
    for (let i = 0; i < 20; i++) segs.push(mkSeg(i))
    const raw = segs.map((s) => s.text).join('')
    const rendered = renderTranscript(segs)
    assert.ok(rendered.length < raw.length, '前置失效：语料没有重叠 ⇒ 去重没被踩到 ⇒ 下面的等价性断言说明不了问题')
    assert.ok(rendered.includes('第0段新增的内容'), '前置失效：第一段都不在渲染结果里')
  })

  it('★★★ 行为：增量渲染器与整场渲染**逐字相同**（尾插 / 中间插 / 改写后追加 / 数组替换）', () => {
    // ① 尾插（原生链形态：appendText 往尾巴上 push）
    const a: ReturnType<typeof mkSeg>[] = []
    const r1 = createTranscriptRenderer()
    for (let i = 0; i < 40; i++) { a.push(mkSeg(i)); r1.appendAt(a, a.length - 1) }
    assert.equal(r1.source().text(), renderTranscript(a), '尾插逐段追加：增量结果与整场渲染不一致')

    // ② 中间插（Web 链形态：按 startMs 有序插入 ⇒ 插到中间）
    const b: ReturnType<typeof mkSeg>[] = []
    const r2 = createTranscriptRenderer()
    for (let i = 0; i < 100; i++) { b.push(mkSeg(i)); r2.appendAt(b, b.length - 1) }
    b.splice(50, 0, { ...mkSeg(999), startMs: b[50].startMs + 1, endMs: b[50].endMs + 1 })
    r2.appendAt(b, 50)
    assert.equal(r2.source().text(), renderTranscript(b), '中间插入：增量结果与整场渲染不一致')

    // ③ 段被就地改写（用户改字）后再追加
    const c: ReturnType<typeof mkSeg>[] = []
    const r3 = createTranscriptRenderer()
    for (let i = 0; i < 100; i++) { c.push(mkSeg(i)); r3.appendAt(c, c.length - 1) }
    c[10] = { ...c[10], text: '用户手工改过的正文' }
    r3.invalidate()
    c.push(mkSeg(100))
    r3.appendAt(c, c.length - 1)
    assert.equal(r3.source().text(), renderTranscript(c), '改写后追加：增量结果与整场渲染不一致')

    // ④ 数组被**整份替换**（seedSegments / freshSession 会换掉数组对象）
    const d: ReturnType<typeof mkSeg>[] = []
    const r4 = createTranscriptRenderer()
    for (let i = 0; i < 80; i++) { d.push(mkSeg(i)); r4.appendAt(d, d.length - 1) }
    const e = []
    for (let i = 0; i < 120; i++) e.push(mkSeg(i))
    r4.appendAt(e, 0)
    assert.equal(r4.source().text(), renderTranscript(e), '数组整份替换：增量结果与整场渲染不一致')

    // ④b ⭐ 数组被**整份替换**、且**新数组的长度恰好 = 已渲染段数 + 1**
    //    （生产形态：seedSegments 灌回库里那一批，再来一段 ⇒ 先 push 再 appendAt）
    //    ⇒ 这是**「数组身份检查」唯一能救**的形态：`index === consumed`
    //    ⇒ 「index !== consumed ⇒ 整场重算」那条**兜不住**它（两者都成立）。
    //    ⚠ 第一版只写了 ④ ⇒ 变异 MU7（删掉身份检查）**没转红**
    //    ⇒ 量具错（用例没隔离那条分支），**不是实现错** ⇒ 这里补上隔离形态。
    const f: ReturnType<typeof mkSeg>[] = []
    const r5 = createTranscriptRenderer()
    for (let i = 0; i < 40; i++) { f.push(mkSeg(i)); r5.appendAt(f, f.length - 1) }
    const g: ReturnType<typeof mkSeg>[] = []
    for (let i = 0; i < 40; i++) g.push(mkSeg(i + 5000))   // **内容完全不同**的同样长度
    g.push(mkSeg(6000))                                      // 再来一段（生产形态）
    r5.appendAt(g, g.length - 1)                             // index = 40 === consumed ⇒ 快路径会被触发
    assert.equal(r5.source().text(), renderTranscript(g),
      '数组被整份替换（长度 = 已渲染段数 + 1）后：增量结果与整场渲染不一致 ⇒ 身份检查没起作用')
  })

  it('★★ 字节账：source().byteLength 必须等于正文的 UTF-8 字节数（节流靠它判定）', () => {
    // ⚠ 量具纪律：用 **Node 自己的** Buffer.byteLength，**不**用生产代码里的
    //   utf8ByteLength（那是 TextEncoder）⇒ 两份实现互为对照，口径漂了判据会红。
    const segs: ReturnType<typeof mkSeg>[] = []
    const r = createTranscriptRenderer()
    for (let i = 0; i < 30; i++) { segs.push(mkSeg(i)); r.appendAt(segs, segs.length - 1) }
    const body = r.source().text()
    assert.equal(r.source().byteLength, Buffer.byteLength(body, 'utf8'),
      '字节账与正文对不上 ⇒ 节流判的「增长了多少字节」是错的')
    // ⚠ 必须含**多字节**字符，否则 ASCII 下字节数 == 码元数 ⇒ 单位陷阱测不出来。
    assert.ok(Buffer.byteLength(body, 'utf8') > body.length, '前置失效：语料是纯 ASCII ⇒ 字节账这条量不到单位差异')
  })

  it('★★★ 行为：节流说不写时，惰性源的正文**绝不能**被渲染出来', async () => {
    const { updateTranscript } = await loadStoreModule()
    S925_WRITES.length = 0
    let renders = 0
    const body = 'x'.repeat(1000)
    const src = { byteLength: Buffer.byteLength(body, 'utf8'), text: () => { renders++; return body } }
    // ① 第一次 ⇒ **必写**（否则短会议那一列永远是空的）
    await updateTranscript('m-931', src, { throttled: true })
    assert.equal(S925_WRITES.length, 1, '节流路径的第一次没落库 ⇒ 短会议的预览会整行空白')
    assert.equal(renders, 1, '要写的时候反而没渲染正文')
    // ② 增长不足阈值 ⇒ **不写** ⇒ 正文**绝不能**被渲染
    // ⚠⚠ 必须**重置写入计数**：上面那一次已经写了 1 条 ⇒ 这里判的是「这一轮有没有写」。
    S925_WRITES.length = 0
    renders = 0
    await updateTranscript('m-931', src, { throttled: true })
    assert.equal(S925_WRITES.length, 0, '增长不到阈值却写库了 ⇒ 节流没生效')
    assert.equal(renders, 0,
      '★ 节流说不写，却仍然渲染了整场正文 ⇒ O(n) 的渲染白烧（§931 修的就是这个）')
  })

  it('★★ 行为：要写时，写进库的必须**逐字**是惰性源给的正文', async () => {
    const { updateTranscript } = await loadStoreModule()
    S925_WRITES.length = 0
    const body = '第一行\n第二行'
    await updateTranscript('m-931b', { byteLength: Buffer.byteLength(body, 'utf8'), text: () => body }, { throttled: true })
    assert.equal(S925_WRITES.length, 1, '没落库')
    assert.equal(S925_WRITES[0].params[0], body, '写进库的不是惰性源给的正文')
  })

  /** 两条逐段追加热路径的「是否仍当场整场渲染」违规清单（空 = 合规）。 */
  function hotPathViolations(runtimeSrc: string, ingestSrc: string): string[] {
    const bad: string[] = []
    // ⚠⚠ 必须**先剥注释**再扫：本节第一版的判据扫**原始源码**，
    //   而 appendText 的**注释里**正好写着 `renderTranscript(this.segments.value)`
    //   （说明「以前是什么样」）⇒ 不剥注释就会把注释当成代码 ⇒ **假红**。
    //   （与 §930 在 Go 侧踩的「手写剥注释器」是同一族坑。）
    const runtime = stripComments(runtimeSrc)
    const ingest = stripComments(ingestSrc)
    const append = methodBodyWithModifiers(runtime, 'appendText')
    if (!/transcriptRenderer\s*\.\s*source\(\)/.test(append)) {
      bad.push('recordingRuntime.appendText 没有走增量渲染器')
    }
    if (/renderTranscript\(\s*this\.segments\.value\s*\)/.test(append)) {
      bad.push('recordingRuntime.appendText 仍在当场整场渲染')
    }
    const proc = methodBodyWithModifiers(runtime, 'processSegment')
    if (!/transcriptRenderer\s*:\s*this\.transcriptRenderer/.test(proc)) {
      bad.push('recordingRuntime.processSegment 没把增量渲染器交给 ingestSpeechBlob')
    }
    if (!/renderer\s*\?\s*renderer\.source\(\)/.test(ingest)) {
      bad.push('ingest-speech.ts 没走惰性正文源')
    }
    return bad
  }

  it('★★ 接线：两条逐段追加的热路径必须走**增量渲染器**（不得当场整场渲染）', () => {
    const runtime = read('../../native/recordingRuntime.ts')
    const ingest = read('ingest-speech.ts')
    assert.deepEqual(hotPathViolations(runtime, ingest), [],
      '逐段追加那条路径仍在每段整场渲染 ⇒ 算力白烧（§931 的根）')
  })

  it('★★ 量具自证：接线判据的抽取器**真的有牙**（否则上面那条恒绿）', () => {
    const runtime = read('../../native/recordingRuntime.ts')
    const append = methodBodyWithModifiers(stripComments(runtime), 'appendText')
    assert.ok(append.includes('transcriptRenderer.source()'),
      '抽取器没在 appendText 体内找到增量渲染 ⇒ 接线判据量具坏了')
    assert.ok(append.includes('updateTranscript('),
      '抽取器没在 appendText 体内找到落库调用 ⇒ 接线判据量具坏了')
    // ⚠ 还要证「剥注释」这一步是**必要**的：注释里就写着旧写法 ⇒ 不剥必然假红。
    const raw = methodBodyWithModifiers(runtime, 'appendText')
    assert.ok(/renderTranscript\(\s*this\.segments\.value\s*\)/.test(raw),
      '前置失效：appendText 的注释里已经没有那个旧写法了 ⇒ 「必须剥注释」这条前提不成立')
  })

  it('★★ 负控：把热路径改回「当场整场渲染」必须被上面那条判红', () => {
    const runtime = read('../../native/recordingRuntime.ts')
    const ingest = read('ingest-speech.ts')
    // 同形变异 ①：只把**增量渲染器**换回整场渲染，其余一律不动。
    const mutatedRuntime = runtime.replace(
      'await updateTranscript(meetingId, this.transcriptRenderer.source(), { throttled: true, db: this.operation?.localDb })',
      'await updateTranscript(meetingId, renderTranscript(this.segments.value), { throttled: true, db: this.operation?.localDb })',
    )
    assert.notEqual(mutatedRuntime, runtime, '变异没落盘 ⇒ 这条负控不算数')
    assert.ok(
      hotPathViolations(mutatedRuntime, ingest).length > 0,
      '负控前提失效：变异没有真的把增量渲染换回整场渲染',
    )
    // 同形变异 ②：把渲染器从 ingestSpeechBlob 的实参里去掉
    const mutatedRuntime2 = runtime.replace('transcriptRenderer: this.transcriptRenderer,', '')
    assert.notEqual(mutatedRuntime2, runtime, '变异没落盘 ⇒ 这条负控不算数')
    assert.ok(
      hotPathViolations(mutatedRuntime2, ingest).length > 0,
      '负控前提失效：变异没有真的把渲染器传下去',
    )
    // 同形变异 ③：把 ingest-speech 的惰性源换回当场整场渲染
    const mutatedIngest = ingest.replace(
      'renderer ? renderer.source() : renderTranscript(opts.segments)',
      'renderTranscript(opts.segments)',
    )
    assert.notEqual(mutatedIngest, ingest, '变异没落盘 ⇒ 这条负控不算数')
    assert.ok(
      hotPathViolations(runtime, mutatedIngest).length > 0,
      '负控前提失效：变异没有真的把惰性源换回当场渲染',
    )
  })

  it('★★ 形状：正文必须在**节流判定之后**才渲染（渲染早于判定 ⇒ 浪费照旧）', () => {
    const store = read('meetings-store.ts')
    const fn = functionBody(store, 'updateTranscript')
    const gateAt = fn.indexOf('shouldPersistTranscript')
    const renderAt = fn.indexOf('.text()')
    assert.ok(gateAt >= 0, '判据失效：updateTranscript 里找不到节流判定')
    assert.ok(renderAt >= 0, '判据失效：updateTranscript 里没有惰性渲染（§931 的修复不在？）')
    assert.ok(renderAt > gateAt,
      '正文在节流判定**之前**就渲染了 ⇒ 节流说不写时那次 O(n) 渲染照样发生')
  })

  it('★★ 负控：把渲染挪到节流判定**之前**必须被上面那条判红', () => {
    const store = read('meetings-store.ts')
    const fn = functionBody(store, 'updateTranscript')
    const gateAt = fn.indexOf('shouldPersistTranscript')
    const renderAt = fn.indexOf('.text()')
    assert.ok(gateAt >= 0 && renderAt > gateAt, '前置失效：真实源码不满足「渲染在判定之后」')
    // 同形变异：把渲染提到判定之前
    const mutated = fn.replace(
      /const bytes = [^\n]*\n/,
      'const bytes = 0\n  const eager = typeof source === \'string\' ? source : source.text()\n',
    )
    assert.notEqual(mutated, fn, '变异没落盘 ⇒ 这条负控不算数')
    const mGate = mutated.indexOf('shouldPersistTranscript')
    const mRender = mutated.indexOf('.text()')
    assert.ok(mRender < mGate, '负控前提失效：变异没有把渲染提到判定之前')
  })

  /**
   * ★★ 相对成本：快路径必须**真的**比「每段整场渲染」便宜一个量级以上。
   *
   * ⚠ 为什么要有这条（前面那些都证明不了「快路径被走到了」）：
   *   等价性用例证明「结果一样」，**证明不了**「走的哪条路」。
   *   而快路径一旦被绕开（每次都整场重算），结果**照样逐字相同** ⇒ 假绿。
   *   ⇒ 这里用**相对量**（项目纪律：性能判据优先用相对量）把「走的哪条路」钉住。
   *
   * ⚠ 阈值怎么来的：比值随 n **线性**增长（实测 100/200/400/800 段 ⇒ 61×/122×/405×/174×，
   *   ≈ 0.6n；n=1952 实测 **2949×**）⇒ n=300 时理论 ≈180× ⇒ 取 **20×** 作闸
   *   ⇒ 留 **9×** 误伤余量。
   *   （绝对时间不设闸：机器负载波动会误伤，相对比值稳定得多。）
   *
   * ⚠⚠ 为什么 N 只取 300：**判据自己不能变成负载**。
   *   第一版取 2000 ⇒ 这条用例单跑 **60.4 s**（全量门禁里不可接受）。
   *   ⇒ 降到 300 ⇒ 约 4 s，而比值（≈180×）离闸（20×）仍有 9× 余量。
   */
  it('★★ 相对成本：增量快路径必须比「每段整场渲染」快 ≥20 倍', () => {
    const N = 300
    const all = []
    for (let i = 0; i < N; i++) all.push(mkSeg(i))

    // 快路径（生产形态：数组逐段增长）—— 便宜 ⇒ 取 3 次**最小值**（压掉 GC 停顿）
    const runFast = () => {
      const live: ReturnType<typeof mkSeg>[] = []
      const fast = createTranscriptRenderer()
      const t = performance.now()
      for (let i = 0; i < N; i++) { live.push(all[i]); fast.appendAt(live, live.length - 1) }
      return performance.now() - t
    }
    let fastMs = Infinity
    for (let i = 0; i < 3; i++) fastMs = Math.min(fastMs, runFast())

    // 现状：每段整场渲染（贵 ⇒ 只跑一次）
    const slowMs = (() => {
      const t = performance.now()
      for (let i = 0; i < N; i++) renderTranscript(all.slice(0, i + 1))
      return performance.now() - t
    })()

    assert.ok(slowMs / fastMs >= 20,
      `增量快路径没有明显更快（慢 ${(slowMs / fastMs).toFixed(1)}×，闸 20×）⇒ ` +
      '快路径多半没被走到 ⇒ §931 的优化没生效')

    // ★ 负控：同一份语料走「每次都整场重算」的路（index 故意不等于已渲染段数）必须**慢**。
    const alwaysMs = (() => {
      const live2: ReturnType<typeof mkSeg>[] = []
      const always = createTranscriptRenderer()
      const t = performance.now()
      for (let i = 0; i < N; i++) { live2.push(all[i]); always.appendAt(live2, 0) }
      return performance.now() - t
    })()
    assert.ok(alwaysMs / fastMs >= 20,
      `负控失效：整场重算那条路没有明显更慢（${(alwaysMs / fastMs).toFixed(1)}×）⇒ ` +
      '这条相对成本判据量具坏了（不是产品慢了）')
  })
})

describe('N · §932 摘要链与界面分段列表必须共用同一份去重结果', () => {
  /**
   * §932 修的是 §931 登记的**同族**路径：`useLiveSummary` 每轮刷新（30 秒一轮）
   * 都 `dedupeSegments(segments.value)` ⇒ **O(n)** ⇒ 8 小时会议（1952 段 / 691 轮）
   * 实测累计 **20.6–23.4 s 纯 CPU**（§932 实测，两批读数）⇒ 与 §931 那条（108.9 s）同阶。
   *
   * ★ 这一组守的是**正确性**，不是性能：
   *   §932 把「整场去重」换成了「增量渲染器顺手攒的那份」⇒
   *   ⇒ **喂给 LLM 的提示词必须逐字不变**（差一个字就是行为变更），
   *   ⇒ **拿不到就必须回退**（宁可慢，不可给错）。
   */

  const eqJson = (a: unknown, b: unknown): boolean => JSON.stringify(a) === JSON.stringify(b)

  it('★ 前置自证：这份语料**真的踩到去重**（否则下面那些「逐字相同」恒成立）', () => {
    const segs = []
    for (let i = 0; i < 20; i++) segs.push(mkSeg(i))
    const raw = segs.map((s) => s.text).join('')
    assert.ok(renderTranscript(segs).length < raw.length,
      '前置失效：语料没有重叠 ⇒ 去重没被踩到 ⇒ 下面的「逐字相同」断言说明不了问题')
  })

  it('★★★ 行为：增量攒出来的「去重后段」必须与 `dedupeSegments` **逐字相同**（八种形态）', () => {
    // ① 尾插逐段追加（原生链形态：appendText 往尾巴上 push）
    const a: ReturnType<typeof mkSeg>[] = []
    const r1 = createTranscriptRenderer()
    for (let i = 0; i < 40; i++) { a.push(mkSeg(i)); r1.appendAt(a, a.length - 1) }
    assert.ok(eqJson(r1.dedupedSegmentsFor(a), dedupeSegments(a)),
      '① 尾插逐段追加：与整场去重不一致 ⇒ 送给 LLM 的提示词变了')

    // ② 中间插入（Web 链形态：按 startMs 有序插入）
    const b: ReturnType<typeof mkSeg>[] = []
    const r2 = createTranscriptRenderer()
    for (let i = 0; i < 60; i++) { b.push(mkSeg(i)); r2.appendAt(b, b.length - 1) }
    b.splice(30, 0, { ...mkSeg(999), startMs: b[30].startMs + 1, endMs: b[30].endMs + 1 })
    r2.appendAt(b, 30)
    assert.ok(eqJson(r2.dedupedSegmentsFor(b), dedupeSegments(b)),
      '② 中间插入：与整场去重不一致')

    // ③ 段被就地改写后 refresh（用户改字 / 改说话人 —— §932 把两处都换成了 refresh）
    const c: ReturnType<typeof mkSeg>[] = []
    const r3 = createTranscriptRenderer()
    for (let i = 0; i < 40; i++) { c.push(mkSeg(i)); r3.appendAt(c, c.length - 1) }
    c[10] = { ...c[10], text: '用户手工改过的正文' }
    r3.refresh(c)
    assert.ok(eqJson(r3.dedupedSegmentsFor(c), dedupeSegments(c)),
      '③ 就地改写后 refresh：与整场去重不一致')

    // ④ 数组被整份替换（seedSegments / freshSession 会换掉数组对象）
    const d: ReturnType<typeof mkSeg>[] = []
    const r4 = createTranscriptRenderer()
    for (let i = 0; i < 40; i++) { d.push(mkSeg(i)); r4.appendAt(d, d.length - 1) }
    const e: ReturnType<typeof mkSeg>[] = []
    for (let i = 0; i < 70; i++) e.push(mkSeg(i + 5000))
    r4.appendAt(e, e.length - 1)
    assert.ok(eqJson(r4.dedupedSegmentsFor(e), dedupeSegments(e)),
      '④ 数组被整份替换：与整场去重不一致')

    // ⚠⚠⑤ 单段（**前后有空白**）—— §932 实测抓到的分歧形态
    //   `dedupeSegments` 对 ≤1 段走**短路**（`segments.slice()`：原样返回、**不 trim**），
    //   而增量渲染器是**无条件折叠**（会 trim）⇒ 不修就会**分叉**。
    //   ⇒ 单段形态在生产上**极常见**（会议第一分钟只有一段）⇒ 必须一致。
    const f = [{ ...mkSeg(0), text: '  单段正文  ' }]
    const r5 = createTranscriptRenderer()
    r5.appendAt(f, 0)
    assert.ok(eqJson(r5.dedupedSegmentsFor(f), dedupeSegments(f)),
      '⑤ 单段（前后有空白）：与整场去重不一致 ⇒ 会议第一分钟送出的提示词就变了')

    // ⑥ 单段（**纯空白**）—— 短路会**留着**它，折叠会**丢掉**它
    const g = [{ ...mkSeg(0), text: '   ' }]
    const r6 = createTranscriptRenderer()
    r6.appendAt(g, 0)
    assert.ok(eqJson(r6.dedupedSegmentsFor(g), dedupeSegments(g)),
      '⑥ 单段（纯空白）：与整场去重不一致')

    // ⑦ 单段（manual —— 人工段享受豁免）
    const h = [{ ...mkSeg(0), text: ' 手工输入 ', origin: 'manual' as const }]
    const r7 = createTranscriptRenderer()
    r7.appendAt(h, 0)
    assert.ok(eqJson(r7.dedupedSegmentsFor(h), dedupeSegments(h)),
      '⑦ 单段（manual）：与整场去重不一致')

    // ⑧ 两段且第一段纯空白（长度 ≥2 ⇒ 两边都走折叠 ⇒ 必须一致）
    const i2 = [{ ...mkSeg(0), text: '  ' }, { ...mkSeg(1), startMs: 1000, endMs: 1800 }]
    const r8 = createTranscriptRenderer()
    r8.appendAt(i2, 0)
    r8.appendAt(i2, 1)
    assert.ok(eqJson(r8.dedupedSegmentsFor(i2), dedupeSegments(i2)),
      '⑧ 两段且首段纯空白：与整场去重不一致')
  })

  it('★★★ 守卫：三个条件任一不成立就必须给 null（拿不到 ⇒ 调用方回退整场去重）', () => {
    const arr: ReturnType<typeof mkSeg>[] = []
    const r = createTranscriptRenderer()
    for (let i = 0; i < 20; i++) { arr.push(mkSeg(i)); r.appendAt(arr, arr.length - 1) }

    // ⚠ 负控前提：对得上的形态**必须**给出
    //   （否则「永远返回 null」也能让下面三条全绿 ⇒ 假绿）
    assert.ok(r.dedupedSegmentsFor(arr) !== null,
      '负控前提失效：对得上的形态竟然给不出结果 ⇒ 这组守卫判据量具坏了')

    // ① 数组被**整份替换**（身份不同）
    const stranger: ReturnType<typeof mkSeg>[] = []
    for (let i = 0; i < 20; i++) stranger.push(mkSeg(i))
    assert.equal(r.dedupedSegmentsFor(stranger), null,
      '数组被整份替换却给出了结果 ⇒ 会把过期正文当整场结果送出去')

    // ② 段数对不上（有段不是经由本渲染器进来的 ⇒ 最后一段会被漏掉）
    const arr2: ReturnType<typeof mkSeg>[] = []
    const r2 = createTranscriptRenderer()
    for (let i = 0; i < 20; i++) { arr2.push(mkSeg(i)); r2.appendAt(arr2, arr2.length - 1) }
    arr2.push(mkSeg(999))
    assert.equal(r2.dedupedSegmentsFor(arr2), null,
      '段数对不上却给出了结果 ⇒ 最后一段永远进不了摘要')

    // ③ 状态已过期（`invalidate()` 之后 ⇒ 那份是**改写前**的）
    r.invalidate()
    assert.equal(r.dedupedSegmentsFor(arr), null,
      'invalidate 之后却给出了结果 ⇒ 摘要链会拿到改写前的正文 ⇒ 静默的错误')
  })

  it('★★★ 行为：就地改写后，摘要链拿到的必须是**改写后**的正文', () => {
    const arr: ReturnType<typeof mkSeg>[] = []
    const r = createTranscriptRenderer()
    for (let i = 0; i < 20; i++) { arr.push(mkSeg(i)); r.appendAt(arr, arr.length - 1) }
    arr[10] = { ...arr[10], text: '用户手工改过的正文' }

    // ⚠ 这一步是 §932 之前的形态：只标脏、不重算。
    //   ⇒ 那份「去重后段」仍是**改写前**的 ⇒ 摘要链会拿到旧正文。
    r.invalidate()
    assert.equal(r.dedupedSegmentsFor(arr), null,
      '标脏之后竟然给出了结果 ⇒ 摘要链会拿到改写前的正文 ⇒ 静默的错误')

    // ⇒ §932 把两处就地改写都换成了 `refresh()` ⇒ 必须给出**改写后**的。
    r.refresh(arr)
    const got = r.dedupedSegmentsFor(arr)
    assert.ok(got !== null, 'refresh 之后竟然给不出结果')
    assert.ok(got.some((s) => s.text.includes('用户手工改过的正文')),
      'refresh 之后给出的去重后段里没有改写后的正文 ⇒ 摘要链会拿到旧内容')
    assert.ok(eqJson(got, dedupeSegments(arr)), 'refresh 之后与整场去重不一致')
  })

  it('★★ 相对成本：提供方那条路必须比整场去重快 ≥20 倍（证明「走的哪条路」）', () => {
    const N = 300
    const arr: ReturnType<typeof mkSeg>[] = []
    const r = createTranscriptRenderer()
    for (let i = 0; i < N; i++) { arr.push(mkSeg(i)); r.appendAt(arr, arr.length - 1) }

    // ⚠ 前提：提供方**必须给得出**（给不出 ⇒ 调用方会回退整场去重 ⇒ 比值就没意义了）
    assert.ok(r.dedupedSegmentsFor(arr) !== null, '前提失效：提供方给不出结果 ⇒ 相对成本无从谈起')

    const best = (fn: () => void, reps: number): number => {
      let b = Infinity
      for (let i = 0; i < reps; i++) { const t = performance.now(); fn(); b = Math.min(b, performance.now() - t) }
      return b
    }
    const fastMs = best(() => { r.dedupedSegmentsFor(arr) }, 5)
    const slowMs = best(() => { dedupeSegments(arr) }, 3)

    // ⚠⚠ 量级合理性自检：慢的那条**必须**有可见成本
    //   （否则「比值很大」可能只是因为快的那条被计成了 0 ms ⇒ 判据量具坏了）
    assert.ok(slowMs >= 1, `量级自检失效：整场去重只花了 ${slowMs.toFixed(3)} ms ⇒ 这个比值不可信`)
    assert.ok(slowMs / fastMs >= 20,
      `提供方那条路没有明显更快（慢 ${(slowMs / fastMs).toFixed(1)}×，闸 20×）⇒ §932 的优化没生效`)

    // ★ 负控：守卫不成立时**没有**快路径可走（必须给 null，让调用方回退）
    const stranger: ReturnType<typeof mkSeg>[] = []
    for (let i = 0; i < N; i++) stranger.push(mkSeg(i))
    assert.equal(r.dedupedSegmentsFor(stranger), null,
      '负控前提失效：数组对不上却给出了快路径结果')
  })

  // ── 接线层：判据钉「**真的用上了提供方**」，否则上面那些行为门全是空转 ──

  /** `useLiveSummary` 自己的接线违规清单（空 = 合规）。 */
  function liveSummaryWiringViolations(src: string): string[] {
    const bad: string[] = []
    const body = functionBody(stripComments(src), 'refresh')
    const call = /opts\?\s*\.\s*dedupedSegments\?\s*\.\s*\(\s*([A-Za-z_$][\w$]*)\s*\)/.exec(body)
    if (!call) {
      bad.push('useLiveSummary.refresh 没有调用 opts.dedupedSegments 提供方')
      return bad
    }
    const arg = call[1]
    // ⚠⚠ §932 的语义是「**优先**用提供方」⇒ 光「调用了」不够：
    //   必须钉住「提供方的结果**真的进了 cleanSegments**」
    //   （「调了但不用」= 优化没生效，而 D3 那道门认两种形态 ⇒ 它抓不到）。
    if (!/const\s+cleanSegments\s*=\s*provided\s*\?\?/.test(body)) {
      bad.push('useLiveSummary.refresh 没有优先用提供方的结果（provided 没进 cleanSegments）')
    }
    // ⚠ 回退必须用**同一个**数组（两处看到不同的数组 ⇒ 结果不自洽）
    if (!new RegExp(`\\?\\?\\s*dedupeSegments\\(\\s*${arg}\\s*\\)`).test(body)) {
      bad.push(`useLiveSummary.refresh 的回退整场去重没有用同一个数组（${arg}）`)
    }
    // ⚠ 那个数组必须**取自 segments.value**、且**只取一次**
    const takes = body.match(
      new RegExp(`(?:const|let|var)\\s+${arg}\\s*=\\s*segments\\s*\\.\\s*value`, 'g'),
    ) ?? []
    if (takes.length !== 1) {
      bad.push(`useLiveSummary.refresh 里 ${arg} 的取值次数是 ${takes.length}（应为 1）`)
    }
    return bad
  }

  /** 两个消费方的接线违规清单（空 = 合规）。 */
  function consumerWiringViolations(detailSrc: string, sessionSrc: string): string[] {
    const bad: string[] = []
    const detail = stripComments(detailSrc)
    const session = stripComments(sessionSrc)

    // ① 详情页：摘要链必须收到提供方
    if (!/dedupedSegments\s*:/.test(callArgs(detail, 'useLiveSummary'))) {
      bad.push('MeetingDetailView 没有把提供方传给 useLiveSummary')
    }
    // ② 详情页：界面分段列表必须走提供方（否则界面那列仍是每段整场去重 ⇒ O(时长²)）
    const display = computedBody(detail, 'displaySegments')
    if (!/dedupedSegmentsProvider\s*\(/.test(display)) {
      bad.push('MeetingDetailView.displaySegments 没有用提供方 ⇒ 界面那列仍是每段整场去重')
    }
    // ③ 详情页：必须保留整场去重的兜底（拿不到时给出错的分段比慢更糟）
    if (!/dedupeSegments\s*\(/.test(display)) {
      bad.push('MeetingDetailView.displaySegments 没有保留整场去重的兜底')
    }
    // ④ 随手记那条摘要链
    if (!/dedupedSegments\s*:/.test(callArgs(session, 'useLiveSummary'))) {
      bad.push('useSessionLiveRecord 没有把提供方传给 useLiveSummary')
    }
    return bad
  }

  /** runtime 侧接线的违规清单（空 = 合规）。 */
  function runtimeWiringViolations(src: string): string[] {
    const bad: string[] = []
    const s = stripComments(src)
    const m = methodBodyWithModifiers(s, 'dedupedSegmentsFor')
    // ① 必须按会议隔离（否则会拿到**另一场**的正文 ⇒ 跨会议串台）
    if (!/activeMeetingId\s*\.\s*value\s*!==\s*meetingId/.test(m)) {
      bad.push('recordingRuntime.dedupedSegmentsFor 没有按会议隔离')
    }
    if (!/return\s+null/.test(m)) {
      bad.push('recordingRuntime.dedupedSegmentsFor 没有 null 出口')
    }
    // ② 必须把数组交给渲染器去判（守卫在那儿）
    if (!/transcriptRenderer\s*\.\s*dedupedSegmentsFor\(\s*segments\s*\)/.test(m)) {
      bad.push('recordingRuntime.dedupedSegmentsFor 没有把数组交给渲染器的守卫')
    }
    // ③ 两处「就地改写」必须 refresh（invalidate 只标脏 ⇒ 那份是改写前的）
    for (const name of ['editSegmentInMemory', 'mergeOverSplitSpeakers']) {
      const body = methodBodyWithModifiers(s, name)
      if (!/transcriptRenderer\s*\.\s*refresh\s*\(/.test(body)) {
        bad.push(`recordingRuntime.${name} 没有整场重算（refresh）`)
      }
      if (/transcriptRenderer\s*\.\s*invalidate\s*\(/.test(body)) {
        bad.push(`recordingRuntime.${name} 仍在 invalidate ⇒ 摘要链会拿到改写前的正文`)
      }
    }
    return bad
  }

  it('★★ 接线：摘要链必须**优先用提供方**、拿不到才回退整场去重', () => {
    const src = read('../../composables/useLiveSummary.ts')
    assert.deepEqual(liveSummaryWiringViolations(src), [],
      '摘要链没有用上提供方 ⇒ 每轮仍是整场去重 ⇒ §932 的优化没生效')
  })

  it('★★ 接线：界面分段列表与随手记那条摘要链都必须接上提供方', () => {
    const detail = read('MeetingDetailView.vue')
    const session = read('../sessions/useSessionLiveRecord.ts')
    assert.deepEqual(consumerWiringViolations(detail, session), [],
      '消费方没接上提供方 ⇒ 界面那列 / 随手记摘要仍是每轮整场去重')
  })

  it('★★ 接线：runtime 必须按会议隔离，且两处就地改写必须整场重算', () => {
    const src = read('../../native/recordingRuntime.ts')
    assert.deepEqual(runtimeWiringViolations(src), [],
      'runtime 侧接线不合规 ⇒ 会跨会议串台，或摘要链拿到改写前的正文')
  })

  it('★★ 量具自证：三条接线判据的抽取器**真的有牙**（否则上面那三条恒绿）', () => {
    const live = read('../../composables/useLiveSummary.ts')
    const detail = read('MeetingDetailView.vue')
    const session = read('../sessions/useSessionLiveRecord.ts')
    const runtime = read('../../native/recordingRuntime.ts')

    // ① 抽取器必须真的能在**生产源码**上定位到东西
    assert.equal(liveSummaryWiringViolations(live).length, 0, '生产源码被判成不合规 ⇒ 抽取器有问题')
    assert.equal(consumerWiringViolations(detail, session).length, 0, '生产源码被判成不合规 ⇒ 抽取器有问题')
    assert.equal(runtimeWiringViolations(runtime).length, 0, '生产源码被判成不合规 ⇒ 抽取器有问题')

    // ② 负控：把提供方调用删掉 ⇒ 摘要链那条必须判红
    const liveMutated = live.replace(
      'provided = opts?.dedupedSegments?.(rawSegments) ?? null',
      'provided = null',
    )
    assert.notEqual(liveMutated, live, '变异没落盘 ⇒ 这条负控不算数')
    assert.ok(liveSummaryWiringViolations(liveMutated).length > 0,
      '负控前提失效：变异没有真的摘掉提供方调用')

    // ③ 负控：把界面那列改回「只用整场去重」⇒ 消费方那条必须判红
    const detailMutated = detail.replace(
      'const provided = dedupedSegmentsProvider(live)',
      'const provided = null',
    )
    assert.notEqual(detailMutated, detail, '变异没落盘 ⇒ 这条负控不算数')
    assert.ok(consumerWiringViolations(detailMutated, session).length > 0,
      '负控前提失效：变异没有真的摘掉界面那列的提供方')

    // ④ 负控：把会议隔离删掉 ⇒ runtime 那条必须判红
    const runtimeMutated = runtime.replace(
      'if (this.activeMeetingId.value !== meetingId) return null',
      'if (false) return null',
    )
    assert.notEqual(runtimeMutated, runtime, '变异没落盘 ⇒ 这条负控不算数')
    assert.ok(runtimeWiringViolations(runtimeMutated).length > 0,
      '负控前提失效：变异没有真的摘掉会议隔离')

    // ⑤ 负控：把 refresh 换回 invalidate ⇒ runtime 那条必须判红
    const runtimeMutated2 = runtime.replace(
      'this.transcriptRenderer.refresh(this.segments.value)',
      'this.transcriptRenderer.invalidate()',
    )
    assert.notEqual(runtimeMutated2, runtime, '变异没落盘 ⇒ 这条负控不算数')
    assert.ok(runtimeWiringViolations(runtimeMutated2).length > 0,
      '负控前提失效：变异没有真的把 refresh 换回 invalidate')
  })
})
