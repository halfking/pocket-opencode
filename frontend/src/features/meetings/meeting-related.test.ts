import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import {
  mergeRecommendations, parseWikipediaOpenSearch, relatedQueryFromTranscript, wikipediaSearchUrl,
} from './meeting-related.ts'
import { WIKIPEDIA_TIMEOUT_MS, fetchWikipediaItems, searchRelatedWeb, type WikipediaFetch } from './wikipedia-search.ts'
import { RelatedRefreshGate } from './meeting-related-refresh.ts'

describe('meeting-related', () => {
  it('builds a short query from the latest utterances', () => {
    assert.equal(relatedQueryFromTranscript(['', '预算', '发布窗口']), '预算 发布窗口')
    assert.equal(relatedQueryFromTranscript([]), '')
    assert.equal(
      relatedQueryFromTranscript(['今天评审第三季度预算，李四下周提交方案。']),
      '第三季度 预算',
    )
  })

  it('dedupes recommendations by type+id', () => {
    const a = { type: 'note' as const, id: 'n1', title: 'A', snippet: '', score: 1 }
    const b = { type: 'note' as const, id: 'n1', title: 'A2', snippet: '', score: 2 }
    const c = { type: 'email' as const, id: 'n1', title: 'E', snippet: '', score: 1 }
    assert.deepEqual(mergeRecommendations([a], [b, c], 3).map((i) => `${i.type}:${i.id}`), [
      'note:n1', 'email:n1',
    ])
  })

  /**
   * §913/D81：`mergeRecommendations` 是**拼接去重截断**，不是排序函数。
   *
   * ⚠⚠ 为什么这条要钉住：**`RecommendItem.score` 在本仓有四个生产者、三种量纲、
   *   零个消费者**（详见设计文档 §913）。其中笔记那一路来自 `searchHybrid` 的
   *   RRF（实测区间 [0.0152, 0.0323]），会议与维基两路是 `1/(i+1)` 位次
   *   （实测区间 [0.3333, 1.0]）⇒ **相差 10 倍，不可比**。
   *   ⇒ 谁「顺手加一句 `.sort((a,b) => b.score - a.score)`」，
   *     **全部笔记会被沉到会议/维基之后**（笔记最高分只有会议最低分的 0.10×）。
   *   ⇒ 这条判据的作用不是要求排序，而是**要求排序必须先解决量纲**：
   *     真要按相关度排，先统一口径（简报 137 第 ① 问），再改这里。
   */
  it('§913 mergeRecommendations 不按 score 排序 —— 输出顺序恒等于输入顺序', () => {
    // 故意把「高分」的会议放在「低分」笔记**后面**输入。
    const low = { type: 'note' as const, id: 'n1', title: '低分笔记', snippet: '', score: 0.0159 }
    const high = { type: 'knowledge' as const, id: 'm1', title: '高分会议', snippet: '', score: 1.0 }
    assert.deepEqual(
      mergeRecommendations([low], [high], 8).map((i) => `${i.type}:${i.score}`),
      ['note:0.0159', 'knowledge:1'],
      '输出被按 score 重排了 ⇒ 量纲未统一就排序会把笔记全部沉底（简报 137）',
    )
    // 再从反方向确认一次：顺序是**跟随输入**的，不是跟随分数的。
    assert.deepEqual(
      mergeRecommendations([high], [low], 8).map((i) => `${i.type}:${i.score}`),
      ['knowledge:1', 'note:0.0159'],
      '交换输入后输出没跟着变 ⇒ 存在某种与输入顺序无关的重排（排序/权重）',
    )
  })

  /**
   * §913/D81：截断落在**拼接序列的末尾**，与相关性无关。
   *
   * 现状后果（实测）：`searchRelatedContext` 传 `limit = 8`，
   * 而三源满载时是 笔记 3 + 会议 3 + 维基 3 = 9
   * ⇒ 被砍掉的**恒定是维基最后一条**，与它多不相关无关。
   * ⚠ 这是「按拼接顺序截断」的机械后果，不是任何人的取舍 ——
   *   而「参考资料面板最多显示几条、怎么在三源间分配」是产品口径，
   *   已登记为简报 137 第 ③ 问，本节未代拍。
   * ⇒ 本判据只钉**机制**（末尾先死），不钉 `limit` 的具体数值 ——
   *   钉数值就要扫描调用点，而那是 §911 的「人工枚举盲区」。
   */
  it('§913 截断落在拼接序列末尾：末尾那一条先死，且与它的分数无关', () => {
    const mk = (n: number, type: 'note' | 'knowledge' | 'web', score: number) =>
      Array.from({ length: n }, (_, i) => ({ type, id: `${type}${i}`, title: `${type}${i}`, snippet: '', score }))
    const notes = mk(3, 'note', 0.02)
    const extra = [...mk(3, 'knowledge', 1), ...mk(3, 'web', 1)]
    const kept = mergeRecommendations(notes, extra, 8)
    assert.equal(kept.length, 8, 'limit 没生效')
    assert.deepEqual(
      kept.map((i) => i.type),
      ['note', 'note', 'note', 'knowledge', 'knowledge', 'knowledge', 'web', 'web'],
      '截断不再是「末尾先死」⇒ 分配口径变了，必须同步更新设计文档 §913 与简报 137',
    )
  })

  it('builds and parses wikipedia open search', () => {
    assert.equal(wikipediaSearchUrl('  '), '')
    assert.match(wikipediaSearchUrl('Q3 预算'), /search=Q3/)
    assert.deepEqual(
      parseWikipediaOpenSearch(['q', ['预算'], ['释义'], ['https://zh.wikipedia.org/wiki/预算']]).map((i) => i.type),
      ['web'],
    )
    assert.deepEqual(parseWikipediaOpenSearch(null), [])
  })

  /**
   * §913/D81：维基那一路的 `score` 是**位次**，不是相关度。
   *
   * Wikipedia OpenSearch（`action=opensearch`）**不返回任何相关度** ——
   * 它只返回一个按匹配方式排好的标题数组。所以 `1/(i+1)` 量的是
   * 「排第几」，**不是「有多相关」**：第 1 条得 1.0 仅仅因为它排在最前。
   * ⚠ 会议那一路形状相同但成因不同（`meeting-related-search.ts:51`），
   *   它的 `i` 来自 `ORDER BY started_at DESC` ⇒ 那里的「相关度」其实是**新旧**。
   *   两条路都叫 `score`，含义却都只是位次。
   */
  it('§913 维基那一路的 score 是 1/(i+1) 位次，不是相关度', () => {
    const items = parseWikipediaOpenSearch([
      'q', ['甲', '乙', '丙'], ['x', 'y', 'z'], ['u1', 'u2', 'u3'],
    ])
    assert.deepEqual(items.map((i) => i.score), [1, 0.5, 1 / 3],
      '维基分数的形状变了 ⇒ 它不再是「位次」这个事实需要重新核实，§913 的量纲结论可能失效')
  })
})

// =====================================================================
// §926：长会议期间的外部调用与配额账本
//
// ── 这一节钉的是两个性质，不是两个实现 ──
//
// P1（第三方请求有上界）：
//   原来 `fetchWikipedia` 是裸 `fetch` ⇒ 第三方连接卡住 ⇒ promise 永不落地
//   ⇒ `searchRelatedContext` 的 `Promise.all` 等它 ⇒ **整个相关推荐面板的
//   那次刷新被无限期挂住**；而刷新是**每段触发**的 ⇒ 挂住的那一次**不阻止
//   下一次** ⇒ 在途的第三方请求数没有上界。
//   实测（真实会议语料 35 个不同查询串）：中位 406 ms / p90 532 ms / max 6932 ms。
//
// P2（刷新至多一个在途）：
//   段到达得比一次刷新更快时（密集发言段间隔可以到几秒），
//   每一次段到达都会**再叠一个在途刷新** ⇒ 外部请求数没有上界。
//   ⇒ 闸门把「在途期间的请求」合并成**至多一次补跑**，且补跑读的是**最新**分段。
// =====================================================================

const HERE = path.dirname(fileURLToPath(import.meta.url))
const readSrc = (rel: string) => readFileSync(path.resolve(HERE, rel), 'utf8')

/**
 * 显式的手动闸：**不依赖挂钟**。
 *
 * ⚠⚠ 为什么不用 `sleep`：首版这几条判据用 `sleep(5)/sleep(15)/sleep(40)` 编排时序
 *   ⇒ 在**机器有负载**时会抖（实测：变异跑批时出现过一次「还原也红」，
 *   而单独复跑 5 次全绿 ⇒ 判红的是**时序量具**，不是被测代码）。
 *   ⇒ 处置：把时序改成**显式放行**（谁在等、谁放行，写在测试里），
 *   只保留一处 `setTimeout(0)`（让出一次事件循环，用来观察「不该被放行的有没有被放行」）。
 */
function deferred() {
  let resolve!: () => void
  const promise = new Promise<void>((r) => { resolve = r })
  return { promise, resolve }
}

/** 让出一次事件循环（观察「是否已被提前放行」时用）。 */
const turn = () => new Promise<void>((r) => setTimeout(r, 0))

type FakeResponse = { ok: boolean; json: () => Promise<unknown> }

/**
 * 假 fetch：能模拟「正常返回」「抛错」「**挂住**」三种。
 *
 * ⚠ 挂住这一档必须**尊重 abort 事件**（真 fetch 就是这样），
 *   否则测的不是「上界生效」而是「测试自己挂住」。
 */
function makeFetch(behaviour: (url: string) => FakeResponse | 'hang' | 'throw') {
  const seen: { url: string; wasAborted: () => boolean }[] = []
  const impl = async (url: string, init?: { signal?: AbortSignal }) => {
    let aborted = false
    init?.signal?.addEventListener('abort', () => { aborted = true })
    seen.push({ url, wasAborted: () => aborted })
    const b = behaviour(url)
    if (b === 'throw') throw new Error('network down')
    if (b === 'hang') {
      await new Promise<void>((_, reject) => {
        const sig = init?.signal
        if (!sig) return
        if (sig.aborted) { reject(new Error('aborted')); return }
        sig.addEventListener('abort', () => reject(new Error('aborted')))
      })
      throw new Error('unreachable')
    }
    return b
  }
  return { impl: impl as unknown as WikipediaFetch, seen }
}

describe('§926 第三方检索的等待上界', () => {
  it('★ 挂住的第三方请求会在上界内被放弃（返回空数组，不抛、不挂死）', async () => {
    const fake = makeFetch(() => 'hang')
    const t0 = Date.now()
    const items = await fetchWikipediaItems('预算 里程碑', { fetchImpl: fake.impl, timeoutMs: 60 })
    const elapsed = Date.now() - t0
    assert.deepEqual(items, [], '超时后这一路应当「没拿到」⇒ 与网络失败同义')
    assert.ok(elapsed >= 50, `上界没生效：${elapsed} ms 就返回了（应当等满 timeout）`)
    assert.ok(elapsed < 2000, `上界没生效：等了 ${elapsed} ms`)
    assert.equal(fake.seen.length, 1, '应当只发了一次请求')
    assert.equal(fake.seen[0].wasAborted(), true,
      '超时必须真的 abort 掉那个 fetch（不能只是 Promise.race 后把请求晾在外面）')
  })

  it('正常路径逐字不变：照旧解析出条目，URL 形态不变', async () => {
    const fake = makeFetch(() => ({
      ok: true,
      json: async () => ['q', ['预算'], ['释义'], ['https://zh.wikipedia.org/wiki/预算']],
    }))
    const items = await fetchWikipediaItems('预算 里程碑', { fetchImpl: fake.impl })
    assert.deepEqual(items.map((i) => i.title), ['预算'])
    assert.match(fake.seen[0].url, /action=opensearch/)
    assert.match(fake.seen[0].url, /search=/)
  })

  it('网络失败不抛（这一路「没拿到」而不是「整条链崩掉」）', async () => {
    const fake = makeFetch(() => 'throw')
    assert.deepEqual(await fetchWikipediaItems('预算', { fetchImpl: fake.impl }), [])
  })

  it('空查询不发请求（抽取没有引入多余的外呼）', async () => {
    const fake = makeFetch(() => ({ ok: true, json: async () => [] }))
    assert.deepEqual(await fetchWikipediaItems('   ', { fetchImpl: fake.impl }), [])
    assert.equal(fake.seen.length, 0, '空查询也发了一次请求')
  })

  it('★ 每次触发的外部请求至多 2 次（主查 + 一次回落）', async () => {
    // 实测：真实语料上每次触发 1.89 次外网请求 ⇒ 预算的**上界**是 2。
    const fake = makeFetch(() => ({ ok: true, json: async () => ['q', [], [], []] }))
    await searchRelatedWeb('预算 里程碑', { fetchImpl: fake.impl })
    assert.equal(fake.seen.length, 2, `发了 ${fake.seen.length} 次外网请求（预算上界是 2）`)
  })

  it('★ 主查命中时不发回落请求（不多打一次外网）', async () => {
    const fake = makeFetch(() => ({
      ok: true,
      json: async () => ['q', ['预算'], ['释义'], ['https://zh.wikipedia.org/wiki/预算']],
    }))
    const items = await searchRelatedWeb('预算 里程碑', { fetchImpl: fake.impl })
    assert.equal(items.length, 1)
    assert.equal(fake.seen.length, 1, `主查已经命中了还发回落请求（发了 ${fake.seen.length} 次）`)
  })

  it('★ 单 token 查询只发一次（回落词与主查相同 ⇒ 不重复打）', async () => {
    const fake = makeFetch(() => ({ ok: true, json: async () => ['q', [], [], []] }))
    await searchRelatedWeb('预算', { fetchImpl: fake.impl })
    assert.equal(fake.seen.length, 1, `单 token 查询发了 ${fake.seen.length} 次`)
  })

  it('上界必须高于实测最慢值，且不得比 App 自己的 30 s 默认更宽', () => {
    // 实测：真实会议语料 35 个不同查询串，max 6932 ms。
    assert.ok(WIKIPEDIA_TIMEOUT_MS > 6932,
      `上界 ${WIKIPEDIA_TIMEOUT_MS} ms 不高于实测最慢值 6932 ms ⇒ 会把「慢但成功」判成失败`)
    assert.ok(WIKIPEDIA_TIMEOUT_MS <= 30_000,
      `上界 ${WIKIPEDIA_TIMEOUT_MS} ms 比 App 自己的默认（http() 的 30 s）还宽 ⇒ 同族漏项没修干净`)
  })
})

describe('§926 相关推荐刷新的并发闸', () => {
  it('★ 至多一个在途：在途期间连发 5 次 ⇒ 最大并发 = 1', async () => {
    const gate = new RelatedRefreshGate()
    const started = deferred()
    const round1 = deferred()
    const round2 = deferred()
    let active = 0
    let maxActive = 0
    let starts = 0
    const task = async () => {
      starts += 1
      active += 1
      maxActive = Math.max(maxActive, active)
      if (starts === 1) { started.resolve(); await round1.promise } else { await round2.promise }
      active -= 1
    }
    const first = gate.run(task)
    await started.promise
    // ⭐ 这 5 次调用**同步**进入「在途」分支（run() 在第一个 await 之前是同步的）
    //   ⇒ 不需要任何 sleep 来等它们「进到分支里」。
    const rest = [gate.run(task), gate.run(task), gate.run(task), gate.run(task), gate.run(task)]
    round1.resolve()
    round2.resolve()
    await Promise.all([first, ...rest])
    assert.equal(maxActive, 1, `出现了 ${maxActive} 个在途刷新 ⇒ 外部请求数仍然没有上界`)
    assert.equal(starts, 2, `任务启动了 ${starts} 次（期望 1 次在途 + 1 次补跑）`)
  })

  it('★ 在途期间的请求被合并成至多一次补跑（5 次 ⇒ 任务只启动 2 次）', async () => {
    const gate = new RelatedRefreshGate()
    const started = deferred()
    const round1 = deferred()
    const round2 = deferred()
    let starts = 0
    const task = async () => {
      starts += 1
      if (starts === 1) { started.resolve(); await round1.promise } else { await round2.promise }
    }
    const first = gate.run(task)
    await started.promise
    const rest = [gate.run(task), gate.run(task), gate.run(task), gate.run(task), gate.run(task)]
    // ⚠ 两轮都要放行：漏放行任何一个 ⇒ 这个 promise 永远不落地
    //   ⇒ node 报「Promise resolution is still pending but the event loop has already resolved」
    //   ⇒ **整条 suite 会被父级取消**（后面几条看起来像「坏了」，其实是这一条拖垮的）。
    round1.resolve()
    round2.resolve()
    await Promise.all([first, ...rest])
    assert.equal(starts, 2, `任务启动了 ${starts} 次（期望 1 次在途 + 1 次补跑）`)
  })

  it('★ 补跑读到的是最新数据（合并中间轮次不等于丢内容）', async () => {
    const gate = new RelatedRefreshGate()
    const started = deferred()
    const round1 = deferred()
    let latest = 'v1'
    const observed: string[] = []
    const task = async () => {
      observed.push(latest)
      if (observed.length === 1) { started.resolve(); await round1.promise }
    }
    const first = gate.run(task)
    await started.promise
    latest = 'v2'
    const second = gate.run(task)
    round1.resolve()
    await Promise.all([first, second])
    assert.deepEqual(observed, ['v1', 'v2'], '补跑读的不是最新数据 ⇒ 中间那一次的内容被丢了')
  })

  it('★ 补跑跑的是「最新」那个任务，不是「在途」那次的（否则面板停在旧内容上）', async () => {
    const gate = new RelatedRefreshGate()
    const started = deferred()
    const round1 = deferred()
    const ran: string[] = []
    const first = gate.run(async () => { ran.push('old'); started.resolve(); await round1.promise })
    await started.promise
    const second = gate.run(async () => { ran.push('new') })
    round1.resolve()
    await Promise.all([first, second])
    assert.deepEqual(ran, ['old', 'new'],
      '补跑跑的是旧任务 ⇒ 合并掉的正是「更新的那次」⇒ 面板停在旧内容上')
  })

  it('★ 合并掉的调用方不会被提前放行（await 返回时面板已追上）', async () => {
    const gate = new RelatedRefreshGate()
    const started = deferred()
    const round1 = deferred()
    const order: string[] = []
    const first = gate.run(async () => { order.push('t1-start'); started.resolve(); await round1.promise; order.push('t1-end') })
    await started.promise
    const second = gate.run(async () => { order.push('t2') })
    second.then(() => order.push('second-resolved'))
    await turn()
    assert.deepEqual(order, ['t1-start'], '在途还没结束就放行了 ⇒ await 的时序语义被改掉了')
    round1.resolve()
    await Promise.all([first, second])
    assert.deepEqual(order, ['t1-start', 't1-end', 't2', 'second-resolved'])
  })

  it('★ 一次失败不会把闸门卡死（后续刷新仍能真的发起）', async () => {
    const gate = new RelatedRefreshGate()
    let calls = 0
    await assert.rejects(gate.run(async () => { calls += 1; throw new Error('boom') }))
    await gate.run(async () => { calls += 1 })
    assert.equal(calls, 2, '一次失败之后闸门卡在「在途」⇒ 后面所有刷新都被合并掉了')
  })

  it('没有并发时每次请求都真的跑（闸门不吞请求）', async () => {
    const gate = new RelatedRefreshGate()
    let starts = 0
    const task = async () => { starts += 1 }
    await gate.run(task)
    await gate.run(task)
    await gate.run(task)
    assert.equal(starts, 3, '闸门吞掉了请求 ⇒ 面板不再更新')
  })
})

describe('§926 接线（形态门 + 负控）', () => {
  /**
   * 剥掉注释再扫。
   *
   * ⚠ 必须剥：`fire-and-forget-audit` 门自己就是先剥注释再找 `catch` 的
   *   ⇒ 不剥的话，**注释里提到 catch 会被判成代码里有 catch**（假红）。
   * ⚠ `//` 的剥法要避开 `://`（URL 里的），否则会把一行代码后半段吃掉。
   */
  function stripComments(src: string): string {
    return src
      .replace(/\/\*[\s\S]*?\*\//g, (m) => m.replace(/[^\n]/g, ' '))
      .replace(/(^|[^:])\/\/[^\n]*/g, '$1')
  }

  /**
   * 取 `MeetingDetailView.vue` 里 `refreshRelated` 的函数体。
   *
   * ⚠ 必须按括号配平找函数体的 `{`（§925 踩过：返回类型里的花括号
   *   `Promise<{...} | null>` 会把切分点带偏）。
   */
  function bodyOf(rawSrc: string, name: string): string {
    const src = stripComments(rawSrc)
    const at = src.search(new RegExp(`(?:^|\\n)\\s*(?:export\\s+)?(?:async\\s+)?function\\s+${name}\\s*\\(`))
    assert.ok(at > 0, `找不到 ${name}`)
    let i = src.indexOf('(', at)
    let depth = 0
    for (; i < src.length; i += 1) {
      if (src[i] === '(') depth += 1
      else if (src[i] === ')') { depth -= 1; if (depth === 0) { i += 1; break } }
    }
    while (i < src.length && /\s/.test(src[i])) i += 1
    assert.equal(src[i], '{', `${name} 的函数体起点不对`)
    let braces = 0
    for (let k = i; k < src.length; k += 1) {
      if (src[k] === '{') braces += 1
      else if (src[k] === '}') { braces -= 1; if (braces === 0) return src.slice(i, k + 1) }
    }
    throw new Error(`${name} 的函数体没闭合`)
  }

  it('★ refreshRelated 必须走并发闸（否则在途请求数没有上界）', () => {
    const src = readSrc('MeetingDetailView.vue')
    const body = bodyOf(src, 'refreshRelated')
    assert.match(body, /relatedGate\.run\(/,
      'refreshRelated 没有走 relatedGate.run ⇒ 并发闸形同虚设（每次段到达仍会叠一个在途刷新）')
  })

  it('★ 第三方检索必须走带超时的核心（文件里不得再有裸 fetch）', () => {
    const src = readSrc('meeting-related-search.ts')
    assert.match(src, /from '\.\/wikipedia-search\.ts'/, '没有引入带超时的第三方检索核心')
    // 裸 fetch 的形态：await fetch( / = fetch( / return fetch(
    assert.doesNotMatch(src, /(^|[^.\w])fetch\s*\(/,
      'meeting-related-search.ts 里还有裸 fetch ⇒ 第三方请求重新变成无上界等待')
  })
})
