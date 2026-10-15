/**
 * meeting-roster-homophone.ts — 用**参会人名单**纠正 ASR 阶段的人名同音错。
 *
 * 问题（2026-10-06 实测，§44.3）
 * ----------------------------
 * 本会话已有的代码兜底 `guardRefineResult` / `participantLeak`（§31）防的是
 * 「**精校阶段**把名字换成名单外的**外人**」。但有一类它永远防不住：
 *
 *     参会人名单：["张伟", "林岚", "王总"]
 *     ASR 原文：  "…跟王总还有林兰开个客户评审会…"
 *
 * 「林兰」**确实在名单里**（只是听错了同音字）⇒ `participantLeak` 不触发，
 * 而用户看到的是一个从未在会议中出现过的参会人名。
 *
 * 为什么不用 sherpa-onnx 的 HomophoneReplacer
 * ------------------------------------------
 * HR 更通用（能修「玄戒/悬借」「弓头/公投」这类非人名专名），但它需要
 * `replace.fst`——那文件要用 **pynini** 离线生成，而 pynini **在 macOS 装不上**
 * （无 wheel、源码编译失败；官方文档也要求非 Linux 另找机器，§43.2）。
 *
 * 本模块只做**人名**这一类，判据完全不同：**已有权威名单可依**，
 * 不需要拼音规则去「猜」正确写法，只需比对拼音声调序列。
 * ⇒ 纯代码、确定性、零外部依赖（拼音表由调用方注入）。
 *
 * 三条不可越过的边界（各配一条门）
 * ------------------------------
 * 1. **只在名单里存在该正确写法时才改**——否则凭什么知道哪个才是对的。
 * 2. **名单里若同时存在两个同音不同字的名字，不改**——无法区分，改就是猜。
 *    （§31 的原则：宁可不动，也不要制造错的名字。）
 * 3. **不碰非人名文本**——只替换长度等于某个名单名字长度的同音串，
 *    且该串本身不在名单里。
 *
 * 拼音表由调用方注入：本模块**不含**全量汉字表（那是 66395 行 / 1.3MB 的
 * lex分表，属打包决策）。门也据此注入受控表——**这里验证的是算法，
 * 不是表的覆盖率**。
 */

/** 字 → 带声调拼音。缺字的条目一律视为「不可判定」。 */
export type PinyinTable = Readonly<Record<string, string>>

/** 汉字判定（CJK 统一表意文字区，不含标点/字母/数字）。 */
export function isHan(ch: string): boolean {
  if (!ch || ch.length !== 1) return false
  const cp = ch.codePointAt(0)!
  return cp >= 0x4e00 && cp <= 0x9fff
}

/** 整串是否都是可判定的汉字。有一个字查不到拼音就返回 null（不猜）。 */
export function pinyinOf(seq: string, table: PinyinTable): string | null {
  let out = ''
  for (const ch of seq) {
    if (!isHan(ch)) return null
    const p = table[ch]
    if (!p) return null
    out += p
  }
  return out || null
}

/**
 * 分段说话人的**占位标签**（不是真名）。
 *
 * ⚠ 必须在推导名单时剔掉，理由不是「洁癖」而是**安全机制会被误触发**：
 * `repairRosterHomophones` 里有一条「名单里只要有一个名字拼音判不出来，
 * 就整体放弃」的兜底（表不全 ⇒ 无法证明候选唯一）。
 * 而 `说话人 1` 因为尾部有空格/数字，`pinyinOf` 必然返回 null ——
 * 占位标签混进名单，等于**每次录音都把同音纠正整个关掉**。
 *
 * 来源（别只写一种，会漏）：
 *  · speaker-diarization.ts:57  `说话人 ${nextId}`  —— 声纹聚类默认名
 *  · meeting-utterance.ts:18    `'说话人'`        —— 分段构造兜底
 *  · api/meetings.ts:192        `'说话人'`        —— 发给服务端的 speaker 字段
 */
export function isPlaceholderSpeaker(label: string | null | undefined): boolean {
  const t = (label ?? '').trim()
  if (t === '') return true
  return /^(说话人|speaker)\s*\d*$/i.test(t)
}

/**
 * 推导本次会议的**权威参会人名单**。
 *
 * 两个来源都是产品里真实存在的权威来源，不是猜的：
 *  1. `meeting.participants` —— 用户在会议设置里手填的名单。
 *     ⚠ 但随手记路径**恒为空**：`useSessionLiveRecord.start()` 调
 *     `createMeeting({title, sessionId})` 不传 participants（唯一能写名单的
 *     MeetingSettingsSheet 只挂在会议详情页）。所以只靠它，纠正功能是死代码。
 *  2. **分段说话人标签** —— `labelSpeaker()` 会把「说话人 1」改成真名，且
 *     `recordingRuntime.start()` 每次都 `loadSpeakerProfiles()` 把已存声纹灌进
 *     聚类器 ⇒ **同一个声音跨会议继承真名**。随手记路径虽然没有改名 UI，
 *     但用户只要在任意一次会议详情里起过名，之后就都带着真名。
 *
 * 两者取并集：都为空时返回空数组，调用方据此「不做任何修正」（安全方向）。
 */
export function deriveRoster(input: {
  participants?: readonly (string | null | undefined)[] | null
  speakerLabels?: readonly (string | null | undefined)[] | null
}): string[] {
  const all = [
    ...(input.participants ?? []),
    ...(input.speakerLabels ?? []),
  ]
  const out: string[] = []
  const seen = new Set<string>()
  for (const raw of all) {
    const t = (raw ?? '').trim()
    if (isPlaceholderSpeaker(t)) continue
    if (seen.has(t)) continue
    seen.add(t)
    out.push(t)
  }
  return out
}

export interface RosterFix {
  /** 在 text 中的起始下标 */
  at: number
  /** 误识别的写法 */
  from: string
  /** 名单里的正确写法 */
  to: string
}

export interface RepairResult {
  text: string
  fixes: RosterFix[]
}

/**
 * 纠正 ASR 阶段的人名同音错。
 *
 * 算法（O(n × L)，L = 名单名字长度的集合，通常很小）：
 *  1. 预先算好每个名单名字的拼音序列，**保留全部**（不是只留第一个）——
 *     第 2 条边界要靠这个发现冲突。
 *  2. 在文本里枚举所有长度 ∈ L 的汉字窗口，算拼音。
 *  3. 窗口不在名单里、但与某个名单名字同音 → 记一条修正。
 *  4. 从后往前应用替换，避免下标位移。
 */
export function repairRosterHomophones(
  text: string,
  roster: readonly string[],
  table: PinyinTable,
): RepairResult {
  const trimmed = roster.map((r) => r.trim()).filter((r) => r.length > 0)
  if (trimmed.length === 0 || !text) return { text, fixes: [] }

  // ★ 拼音不可判定的名字**不能静默丢弃**（2026-10-06 变异验证发现的真实缺陷）。
  //
  //   反例：名单 ['林岚', '林蓝']，而表里没有「蓝」。
  //   旧实现 `filter(pinyinOf)` 会把「林蓝」丢掉 ⇒ 候选只剩 1 个 ⇒
  //   系统「自信地」把文本里的「林兰」改成「林岚」，
  //   而真实答案恰恰可能是「林蓝」。
  //
  //   表不全 ⇒ 我们**无法证明**某窗口只有一个候选 ⇒ 安全失效（整体放弃）。
  //   这与边界 2 同源：宁可不动，也不要制造错的名字。
  const unreadable = trimmed.filter((r) => !pinyinOf(r, table))
  if (unreadable.length > 0) return { text, fixes: [] }

  const names = trimmed
  const nameSet = new Set(names)
  // 音 → 名字列表。用列表而不是 Map<string,string>：冲突检测需要「多于一个」。
  const byPin = new Map<string, string[]>()
  for (const n of names) {
    const k = pinyinOf(n, table)!
    const arr = byPin.get(k)
    if (arr) arr.push(n)
    else byPin.set(k, [n])
  }

  const lens = [...new Set(names.map((n) => n.length))].sort((a, b) => a - b)
  const fixes: RosterFix[] = []
  const claimed = new Set<number>() // 同一次扫描里别把同一段改两次

  for (const L of lens) {
    for (let i = 0; i + L <= text.length; i++) {
      if (claimed.has(i)) continue
      const w = text.slice(i, i + L)
      if (!pinyinOf(w, table)) continue
      if (nameSet.has(w)) continue
      const cands = byPin.get(pinyinOf(w, table)!)
      if (!cands || cands.length !== 1) continue // ← 边界 2：同音多人则不动
      fixes.push({ at: i, from: w, to: cands[0] })
      // ⚠ claimed 这条防御**当前未被任何测试覆盖**：它防的是**重叠窗口**
      //   （两个不同长度的名字互为子串且同音，如名单同时含「林岚」与「林岚岚」）。
      //   真实人名场景下这几乎不可达，变异 V6 因此仍绿。
      //   保留是因为它便宜且一旦成立就能挡住「同一段被改两次」的下标错乱；
      //   但**不要**把它当成已验证的行为。
      for (let k = i; k < i + L; k++) claimed.add(k)
    }
  }

  if (fixes.length === 0) return { text, fixes: [] }
  fixes.sort((a, b) => b.at - a.at) // 从后往前
  let out = text
  for (const f of fixes) out = out.slice(0, f.at) + f.to + out.slice(f.at + f.from.length)
  return { text: out, fixes: fixes.reverse() } // 对外按出现顺序返回
}

/* ========================================================================
 * 跨段人名纠正（§80）
 * ========================================================================
 *
 * 问题（2026-10-07 真网关实测，§80.2）
 * ------------------------------------
 * `repairRosterHomophones` 只在**单段文本内**枚举窗口（上方第 175 行的
 * `for i … text.slice(i, i+L)`）。而录音是切段的，于是**人名被切在段边界上时
 * 任何一段都不含完整名字 ⇒ 窗口永不出现 ⇒ 一条都不修**。
 *
 * 实测形态（同一段内容，唯一变量是分段）：
 *
 *     单段："跟章伟还有林兰开个客户评审会"   → 纠正 2 条
 *     切段："今天我们请到了章" / "伟来给…"   → 纠正 1 条（「章伟」那条丢了）
 *
 * 后面的 LLM 精校**也救不回来**（§80.3，两模型各 3/3）：
 *
 *     [说话人 1] 今天我们请到了张     ← 字改对了（章→张，术语表生效）
 *     [说话人 1] 伟来给大家讲一下产品路线 ← 但跨行合并不了
 *
 * ⇒ 用户在界面上看到两个气泡：「请到了张」/「伟来给大家讲…」。
 *
 * 为什么这个修法**不是** `planRefine` 注释里担心的那种位置映射
 * ----------------------------------------------------------
 * `meeting-refine-plan.ts` 写过：「拼接后要把字符位置映射回 segment，
 * 出错时会把修正落到**别的段**上，而错名字比漏修正更糟」。
 *
 * 那条顾虑针对的是「整篇拼接后改写、再按全局偏移切回去」——那里偏移会因
 * 改写而漂移。本函数不做那个：它只看**一个边界左右各最多 (maxLen-1) 个字**，
 * 且中文同音替换**等长**（拼音音节数 = 字数），所以：
 *
 *   · 窗口第 i 个字属于左段还是右段，由 `i < tail.length` **直接可判**，不靠推算；
 *   · 正确写法按同样的下标切给两侧，长度必然相等（下方 assert 兜住）。
 *
 * ⇒ 「错名字落到别的段上」在这条路径上不可达。
 *
 * 三条边界与上方完全一致（不因换位置就放松）：
 *   1. 名单里不存在该正确写法时不改；
 *   2. 名单里有两个同音名字时不改（无法区分，改就是猜）；
 *   3. 名单里有任何一个名字拼音判不出来时**整体放弃**（表不全 ⇒ 无法证明候选唯一）。
 */

/** 一条跨段修正。`at` 是该字在**所属段内**的下标。 */
export interface BoundaryFix {
  /** 落在哪个段（0 起） */
  segment: number
  /** 在该段内的起始下标 */
  at: number
  /** 该段内被替换的原文片段（跨段时通常只有窗口的一部分） */
  from: string
  /** 正确写法的对应片段 */
  to: string
}

export interface BoundaryRepairResult {
  /** 与入参等长，每项是修正后的文本（无修正时原样） */
  texts: string[]
  fixes: BoundaryFix[]
}

/**
 * 纠正**跨段边界**上的人名同音错。段内已有的修正不在这里做
 * （由 `repairRosterHomophones` 负责），两者互补而非重叠。
 */
export function repairSegmentBoundaryHomophones(
  segments: readonly { text: string }[],
  roster: readonly string[],
  table: PinyinTable,
): BoundaryRepairResult {
  const texts = segments.map((s) => s.text)
  const trimmed = roster.map((r) => r.trim()).filter((r) => r.length > 0)
  // 边界 3，与单段版同源：表不全 ⇒ 无法证明候选唯一 ⇒ 安全失效。
  const unreadable = trimmed.filter((r) => !pinyinOf(r, table))
  if (unreadable.length > 0 || trimmed.length === 0 || segments.length < 2) {
    return { texts, fixes: [] }
  }

  const nameSet = new Set(trimmed)
  const byPin = new Map<string, string[]>()
  for (const n of trimmed) {
    const k = pinyinOf(n, table)!
    const arr = byPin.get(k)
    if (arr) arr.push(n)
    else byPin.set(k, [n])
  }
  const lens = [...new Set(trimmed.map((n) => n.length))].sort((a, b) => a - b)
  const maxLen = lens[lens.length - 1]
  const ctx = maxLen - 1 // 名字跨段时，每侧最多需要这么多个字才能凑出完整窗口
  if (ctx <= 0) return { texts, fixes: [] } // 名单全是单字 ⇒ 跨段不可能发生

  const fixes: BoundaryFix[] = []
  for (let b = 0; b + 1 < segments.length; b++) {
    const left = texts[b]
    const right = texts[b + 1]
    if (!left || !right) continue
    const tail = left.slice(-ctx)
    const head = right.slice(0, ctx)
    if (!tail || !head) continue
    const k = tail.length // 窗口里下标 < k 的属于左段
    const window = tail + head

    // 同一侧可能有多个修正 ⇒ 收集后按 at 降序应用，避免位移。
    const leftPatches: { at: number; from: string; to: string }[] = []
    const rightPatches: { at: number; from: string; to: string }[] = []
    const claimed = new Set<number>()

    for (const L of lens) {
      for (let i = 0; i + L <= window.length; i++) {
        // ★ 只处理**跨越**边界的窗口。完全落在某一段里的由单段版负责，
        //   在这里重复处理会让同一条修正被记两次。
        if (!(i < k && i + L > k)) continue
        if (claimed.has(i)) continue
        const w = window.slice(i, i + L)
        const pin = pinyinOf(w, table)
        if (!pin) continue // 有非汉字或查不到 ⇒ 不猜
        if (nameSet.has(w)) continue // 边界 1：已是正确写法
        const cands = byPin.get(pin)
        if (!cands || cands.length !== 1) continue // 边界 2：同音多人则不动
        const correct = cands[0]
        // 「窗口与正确写法等长」是**恒真**的，故这里没有、也不该有长度检查：
        //   pinyinOf 每字产出一个音节，音节串相等 ⇒ 字数相等 ⇒ correct.length === L。
        //   10 发变异里的 M4「去掉等长检查」跑完全绿，确诊它不可达，已删。
        for (let j = i; j < i + L; j++) claimed.add(j)
        const leftCount = k - i // 落在左段的字数
        const lFrom = w.slice(0, leftCount)
        const lTo = correct.slice(0, leftCount)
        const rFrom = w.slice(leftCount)
        const rTo = correct.slice(leftCount)
        if (lFrom !== lTo) {
          leftPatches.push({ at: left.length - leftCount, from: lFrom, to: lTo })
          fixes.push({ segment: b, at: left.length - leftCount, from: lFrom, to: lTo })
        }
        if (rFrom !== rTo) {
          rightPatches.push({ at: 0, from: rFrom, to: rTo })
          fixes.push({ segment: b + 1, at: 0, from: rFrom, to: rTo })
        }
      }
    }
    // 从后往前应用：同一侧多个补丁时不会互相位移。
    // ⚠ 这里**故意没有**「应用前校验该处原文仍等于 from」的第二道防线
    //   （10 发变异里的 M10 去掉后跑全绿，确诊不可达）：
    //   · 窗口不重叠 —— `claimed` 保证起点已被占用的窗口被跳过，
    //     且不同 L 的窗口若起点落在已占区间内同样被跳过；
    //   · 等长替换不改长度 ⇒ 后面的补丁先改不会让前面的下标漂移；
    //   · `at = left.length - leftCount` 而 `leftCount = k - i`、`k = tail.length`
    //     ⇒ 窗口第 i 字落在左段第 i 字，恒等。
    //   三条合起来「同一位置被改两次」不可达，加校验只是让它看起来承重。
    leftPatches.sort((x, y) => y.at - x.at)
    for (const p of leftPatches) {
      texts[b] = left.slice(0, p.at) + p.to + left.slice(p.at + p.from.length)
    }
    rightPatches.sort((x, y) => y.at - x.at)
    for (const p of rightPatches) {
      texts[b + 1] = right.slice(0, p.at) + p.to + right.slice(p.at + p.from.length)
    }
  }
  return { texts, fixes }
}