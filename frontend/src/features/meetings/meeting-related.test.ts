import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import {
  mergeRecommendations, parseWikipediaOpenSearch, relatedQueryFromTranscript, wikipediaSearchUrl,
} from './meeting-related.ts'

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
