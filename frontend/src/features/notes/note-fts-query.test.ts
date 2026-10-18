/**
 * 移动端 FTS 查询：中文不能整句加引号，否则 MATCH 几乎为零。
 * Run: node --test --experimental-strip-types src/features/notes/note-fts-query.test.ts
 */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { DatabaseSync } from 'node:sqlite'
import {
  buildFtsQuery,
  cjkBigramTokens,
  extractiveBriefing,
  mergeSearchHits,
  needsCjkLike,
} from './note-fts-query.ts'
// §886：likePattern 是新增的，与 note-fts-query 无关，直接从 native 实现取
// （判据要测的是**那一份**实现，不是某个 re-export 的别名）。
import { likePattern } from '../../native/fts-query.ts'

const HERE = dirname(fileURLToPath(import.meta.url))

/** 剥掉注释：判据的锚点必须落在真代码上，不能落在自己写的说明文字上。 */
const strip = (t: string): string =>
  t.replace(/\/\*[\s\S]*?\*\//g, (m) => m.replace(/[^\n]/g, ' '))
   .replace(/(^|[^:])\/\/[^\n]*/g, '$1')

describe('buildFtsQuery', () => {
  it('quotes latin tokens to avoid FTS operators', () => {
    assert.equal(buildFtsQuery('docker AND k8s'), '"docker" "k8s"')
  })

  it('turns a Chinese phrase into overlapping bigrams', () => {
    assert.equal(buildFtsQuery('周会纪要'), '"周会" OR "会纪" OR "纪要"')
  })

  it('mixes chinese bigrams with quoted english words', () => {
    const q = buildFtsQuery('周会 docker')
    assert.match(q, /"周会"/)
    assert.match(q, /"docker"/)
  })
})

describe('cjkBigramTokens / mergeSearchHits', () => {
  it('emits overlapping two-character tokens for unicode61 FTS', () => {
    assert.equal(cjkBigramTokens('周会纪要'), '周会 会纪 纪要')
  })

  it('keeps first-list order and drops duplicate ids', () => {
    const merged = mergeSearchHits(
      [{ note: { id: 'a' }, score: 2 }, { note: { id: 'b' }, score: 1 }],
      [{ note: { id: 'b' }, score: 9 }, { note: { id: 'c' }, score: 0 }],
      3,
    )
    assert.deepEqual(merged.map((r) => r.note.id), ['a', 'b', 'c'])
  })
})

describe('extractiveBriefing', () => {
  it('writes a readable digest from titles and first sentences', () => {
    const text = extractiveBriefing('周会', [
      { title: '产品周会', content: '决定下周三发布。还要改文案。' },
      { title: null, content: '记得同步设计稿给张三。' },
    ])
    assert.match(text, /2 条/)
    assert.match(text, /产品周会/)
    assert.match(text, /下周三发布/)
    assert.match(text, /同步设计稿/)
  })

  it('says none when the hit list is empty', () => {
    assert.equal(extractiveBriefing('周会', []), '没有找到相关笔记。')
  })
})

/**
 * §886 D56：`asset-store.search()` 曾把用户输入**原样**喂给 FTS5 的 `MATCH ?`，
 * 而本文件里的 `buildFtsQuery`（notes 侧）早就在做这件事 —— 资产侧一处没抄。
 *
 * 实测读数（真 SQLite + fts5，16 个探针，**每条都是跑出来的不是推出来的**）：
 *
 * | 输入                     | 原样喂入                          | 过 buildFtsQuery |
 * |--------------------------|-----------------------------------|------------------|
 * | `138-0013-8000`          | ❌ `no such column: 0013`         | 3 段加引号，0 抛错 |
 * | `database AND`           | ❌ `fts5: syntax error near ""`   | 只留 `"database"` |
 * | `AND` / `OR` / `NOT`     | ❌ syntax error                   | 空串 ⇒ 走 LIKE    |
 * | `-quarterly`             | ❌ `no such column: quarterly`    | `"quarterly"`    |
 * | `"database`              | ❌ `unterminated string`          | `"database"`      |
 * | `(a` / `*` / `---`       | ❌ syntax error                   | `"a"` / 空 / `"---"` |
 * | 中文「数据库」「数据」    | **0 行**（不报错）                | 见下一条：仍 0 行 |
 *
 * ⭐ 最后一行是本节最要紧的发现：**构造器解决不了中文**。
 *   `local_assets_fts` 索引的是**原文** `body_text`（schema.ts:541 只有
 *   title/body 两列，触发器写 `new.body_text`），而 `local_notes` 那一侧
 *   有一个 `search_text` 列专门存 CJK bigram（schema.ts:38 + 触发器
 *   `COALESCE(NULLIF(new.search_text,''), new.content)`）
 *   ⇒ unicode61 把连续汉字当成一个 token ⇒ 中文在 assets FTS 上**必然 0 行**，
 *   且**不报错**（静默的「搜不到」）。
 *   所以 `asset-store.search` 必须**并一层 LIKE**（与 notes 侧
 *   `searchFullText` 的 `needsCjkLike(query) || localResults.length === 0`
 *   同一策略），而那需要真 SQLite 才能验 —— 正是本节用 `node:sqlite` 建的夹具。
 */
describe('§886 · D56：用户输入进 FTS5 MATCH（真 SQLite 夹具）', () => {
  const db = new DatabaseSync(':memory:')
  db.exec(`CREATE VIRTUAL TABLE a_fts USING fts5(title, body)`)
  db.exec(`CREATE TABLE a(id INTEGER PRIMARY KEY, title TEXT, body TEXT)`)
  const ins = db.prepare(`INSERT INTO a(title, body) VALUES (?, ?)`)
  for (const r of [
    ['项目复盘', '今天讨论了数据库索引的优化方案'],
    ['会议纪要', '季度预算和人员安排需要确认'],
    ['购物清单', '买牛奶和鸡蛋 138-0013-8000'],
    ['English note', 'quarterly budget review with team'],
  ]) ins.run(r[0], r[1])
  db.exec(`CREATE TRIGGER a_ai AFTER INSERT ON a BEGIN INSERT INTO a_fts(rowid,title,body) VALUES (new.rowid,new.title,new.body); END`)
  db.exec(`INSERT INTO a_fts(a_fts) VALUES('rebuild')`)

  const runFts = (q: string): string[] =>
    db.prepare(`SELECT title FROM a_fts WHERE a_fts MATCH ? ORDER BY rank`).all(q).map((r: any) => r.title)
  const runLike = (q: string): string[] =>
    db.prepare(`SELECT title FROM a WHERE title LIKE ? OR body LIKE ? ORDER BY id`).all(likePattern(q), likePattern(q)).map((r: any) => r.title)

  // ⚠ 这 11 条是**从真实缺陷读出来的**（每一条都会让原样喂入抛错），
  //   少一条就等于给回归留一个口子。
  const HOSTILE = [
    '138-0013-8000', '-quarterly', 'database AND', 'AND', 'OR', 'NOT',
    'NEAR(a b', '"database', '(a', '*', '---',
  ]

  it('★★ 阳性对照：这 11 条原样喂入**真的会抛错**（否则下面几条是空的）', () => {
    let threw = 0
    for (const raw of HOSTILE) {
      try { runFts(raw) } catch { threw++ }
    }
    assert.equal(threw, HOSTILE.length,
      `只有 ${threw}/${HOSTILE.length} 条会抛错 —— FTS 版本或行为变了，请重新核对整张读数表`)
  })

  it('★★★ 过 buildFtsQuery 之后，这 11 条一条都不能再抛错（D56 的核心）', () => {
    for (const raw of HOSTILE) {
      const built = buildFtsQuery(raw)
      if (!built) continue // 空串会走 LIKE 降级，不进 MATCH
      assert.doesNotThrow(() => runFts(built), `构造后仍抛错：输入 ${JSON.stringify(raw)} → ${JSON.stringify(built)}`)
    }
  })

  it('★★★ 中文在 assets FTS 上必然 0 行 ⇒ 必须靠 LIKE 兜底（这是实测结论，不是推测）', () => {
    // 这条是本节**证伪「构造器就够了」**的关键：三条中文查询在 FTS 上一条都搜不到。
    for (const raw of ['数据库', '数据', '索引']) {
      assert.deepEqual(runFts(buildFtsQuery(raw)), [],
        `${raw} 在 local_assets_fts 上居然有结果 ⇒ 表结构可能变了（是否已加 search_text 列？）`)
      assert.ok(runLike(raw).length > 0,
        `${raw} 的 LIKE 兜底也搜不到 ⇒ asset-store 那侧的并层逻辑有问题`)
    }
  })

  it('★★★ likePattern 必须让中文与手机号都能命中（LIKE 降级的验收）', () => {
    assert.deepEqual(runLike('数据库'), ['项目复盘'])
    assert.deepEqual(runLike('138-0013-8000'), ['购物清单'])
    assert.deepEqual(runLike('quarterly'), ['English note'])
  })

  it('★★ ⚠ 边界登记：likePattern 是**删除** %/_ 而不是转义（搜 100% 实际搜 100）', () => {
    // 刻意把已知取舍钉成用例：将来有人改成「转义 + ESCAPE」时这条会红，
    // 提醒那是**行为变更**（简报第 108 条），要让两侧一起改。
    assert.equal(likePattern('100%'), '%100%')
    assert.deepEqual(runLike('100%'), [], '删除 % 之后库里没有字面量 100% ⇒ 0 行（取舍已知）')
  })

  it('★★★ asset-store.search 必须走这套防护（结构断言，node 里跑不起 asset-store）', () => {
    // ⛔ `asset-store.ts` import `local-db` → `local-db-init` 那一族是**无扩展名**
    //   import，`node --experimental-strip-types` 加载不了（§875 登记的同一限制）
    //   ⇒ 这里只能钉结构。钉的是**性质**不是写法：
    //   ① 必须调构造器；② 必须有 catch 降级；③ 必须有 CJK→LIKE 的并层。
    const src = readFileSync(resolve(HERE, '../../native/asset-store.ts'), 'utf8')
    const body = strip(src)
    const at = body.indexOf('async search(')
    assert.ok(at > 0, '★ 阳性对照失败：找不到 assetStore.search')
    const seg = body.slice(at, at + 6000)
    assert.match(seg, /buildFtsQuery\(/, 'asset-store.search 没有调用 FTS 查询构造器 ⇒ 用户输入仍原样进 MATCH（D56）')
    assert.match(seg, /catch\s*\{/, 'asset-store.search 没有 catch 降级 ⇒ 构造失败会冒泡成搜索报错')
    assert.match(seg, /needsCjkLike\(/, 'asset-store.search 没有 CJK 判断 ⇒ 中文仍静默 0 行（D56 的第二条）')
    assert.match(seg, /likePattern\(/, 'asset-store.search 没有 LIKE 模式 ⇒ 降级路径拿不到模式串')
  })

  it('★★★ 正对限制：MATCH 的绑定实参必须是「构造结果」，不能是原始输入', () => {
    // ⚠⚠⚠ 这条是第一版判据的缺口，它来自一次**假绿**变异：
    //   把 `[ftsQuery, ...]` 改回 `[raw, ...]`（= D56 完全复现）时，第 6 条断言全绿 ——
    //   因为它只验「代码里出现过 buildFtsQuery(」，不验「构造结果流向哪里」。
    //   ⇒ 需要一条**自洽性**断言：先找出构造结果被赋给哪个标识符，
    //   再断言 MATCH 的绑定参数列里用的是**那个**。
    //   这样改名字不会误孤只要两处一起改就仍然正确；
    //   而若谁把实参换回未加工的输入 ⇒ 立即红。
    const src = strip(readFileSync(resolve(HERE, '../../native/asset-store.ts'), 'utf8'))
    const at = src.indexOf('async search(')
    assert.ok(at > 0, '★ 阳性对照失败：找不到 assetStore.search')
    const seg = src.slice(at, at + 6000)

    const assign = /const\s+(\w+)\s*=\s*buildFtsQuery\(/.exec(seg)
    assert.ok(assign, '★ 阳性对照失败：没有把构造结果赋给任何变量')
    const builtVar = assign[1]

    const matchAt = seg.indexOf('MATCH ?')
    assert.ok(matchAt > 0, '★ 阳性对照失败：没有 MATCH ?')
    // 绑定参数列在模板字符串**4e4b外**，不能用固定窗口取
    // （§880 记的连续 4 次因窗口越界翻车）—— 改用括号配平。
    const open = seg.indexOf('[', seg.indexOf('`', matchAt))
    assert.ok(open > 0, '★ 阳性对照失败：找不到绑定参数列')
    let depth = 0
    let close = -1
    for (let i = open; i < seg.length; i++) {
      if (seg[i] === '[') depth++
      else if (seg[i] === ']') { depth--; if (depth === 0) { close = i; break } }
    }
    assert.ok(close > 0, '★ 阳性对照失败：绑定参数列括号未配平')
    const binds = seg.slice(open, close + 1)
    assert.match(binds, new RegExp(`\\b${builtVar}\\b`),
      `MATCH 的绑定实参是 ${binds}，而不是构造结果 ${builtVar} ` +
      '⇒ 用户输入仍原样进 MATCH（D56）')
  })
})
