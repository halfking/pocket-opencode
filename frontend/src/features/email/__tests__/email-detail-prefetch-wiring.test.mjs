// email-detail-prefetch-wiring.test.mjs
//
// 锁住「预取失败必须被说出来」这条接线。
//
// 为什么这道护栏不能省：这次的缺陷**不在纯函数里**。prefetchEmailBody 被
// 改成失败返回 `{ ok:false }` 之后，只测它自己的用例全绿，而用户照样看不到
// 任何提示——因为漏掉的是 EmailDetailView 里那几行**接线**：
//
//   const remote = isBodyPrefetching(id) ? await prefetchEmailBody(id, deps) : …
//   if (remote) { …替换正文… }
//
// `remote` 是字符串，失败时是 `''` → `if` 不成立 → 不写正文、不抛错、catch
// 也进不去 → 界面上只剩 snippet，零提示。而不走预取的那条分支（直接
// emailApi.getEmailBody）是**会抛**的，catch 里设了 bodyError。于是同一个
// 网络故障，报不报错取决于"点击时预取是否恰好在途"——时序相关的静默失败。
//
// 也就是说：判据必须指向**接线**，只测被调用的纯函数会漏掉这个缺陷。这正是
// 「判据指向的对象 ≠ 被断言的对象」那一类。
//
// 负控在文件末尾：把接线退回旧的三元形态，判据必须转红。

import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const VIEW = path.join(HERE, '..', 'EmailDetailView.vue')
const PREFETCH = path.join(HERE, '..', 'email-body-prefetch.ts')

const view = fs.readFileSync(VIEW, 'utf8')
const prefetch = fs.readFileSync(PREFETCH, 'utf8')

/** 取 `function <name>(...) { … }` 的函数体（大括号配平），null = 没找到。 */
export function fnBody(src, decl) {
  const start = src.indexOf(decl)
  if (start < 0) return null
  const open = src.indexOf('{', start)
  if (open < 0) return null
  let depth = 0
  for (let i = open; i < src.length; i++) {
    if (src[i] === '{') depth++
    else if (src[i] === '}') {
      depth--; if (depth === 0) return src.slice(open, i + 1)
    }
  }
  return null
}

/**
 * 接线判据：返回**违规清单**（空数组 = 通过）。
 *
 * 做成「返回清单」而不是一堆 assert，是为了让负控能把同一段判据喂给退回后的
 * 接线再跑一遍——否则负控只能靠"整个文件改坏→看它红不红"，红的原因说不清。
 */
export function judgeWiring(fnText) {
  const bad = []
  if (!fnText) return ['找不到 loadBodyInBackground（判据锚点已失效）']

  // ① 旧的 fail-open 形态：把预取结果当字符串判空。
  if (/const remote = isBodyPrefetching\([\s\S]*\? await prefetchEmailBody\(/.test(fnText)) {
    bad.push('仍在用 `const remote = isBodyPrefetching(…) ? await prefetch… : …` 把结果当字符串判空——'
      + '预取失败会被当成「本来就没有正文」')
  }

  // ② 必须显式处理判别结果的 ok 分支。
  if (!/\br\.ok\b/.test(fnText)) {
    bad.push('没有区分预取的 ok / 失败（`r.ok`）——失败会被静默吞掉')
  }

  // ③ 失败分支必须落到用户可见的 bodyError。
  if (!/bodyError\.value = apiError\(\s*r\.error\s*,/.test(fnText)) {
    bad.push('预取失败没有写进 bodyError——界面上不会显示任何提示')
  }

  // ④ 失败时不该把已有的 snippet/缓存正文也抹掉。
  if (!/else if \(!bodyText\.value\)/.test(fnText)) {
    bad.push('失败分支没有 `!bodyText.value` 保护——已有 snippet/缓存正文时不该再报错')
  }

  // ⑤ purged 是合法答案，不能当失败。
  if (!/if \(!r\.purged\)/.test(fnText)) {
    bad.push('没有区分 purged 与失败——「正文被清除」是合法结果，不该弹错误')
  }
  return bad
}

describe('邮件详情：预取失败必须被说出来（不能是时序相关的静默失败）', () => {
  const body = fnBody(view, 'async function loadBodyInBackground(')

  it('接线上没有违规项', () => {
    const bad = judgeWiring(body)
    assert.deepEqual(bad, [], `\n${bad.join('\n')}`)
  })

  it('bodyError 真的渲染在模板上（否则设了也没人看得见）', () => {
    assert.match(view, /v-if="bodyError"/, '模板里没有渲染 bodyError')
    assert.match(view, /class="body-error"/, 'bodyError 没有对应的样式类')
  })

  it('预取模块本身也区分了失败与空（判别联合，不是空串兜底）', () => {
    // 纯函数侧的契约。接线判据之外的第二道：防止有人只改接线不改源头。
    assert.match(
      prefetch,
      /export type PrefetchResult =[\s\S]*?\{ ok: true; body: string; purged: boolean \}[\s\S]*?\{ ok: false; error: unknown \}/,
      'PrefetchResult 不再是判别联合——失败又会退回空串',
    )
    assert.match(
      prefetch,
      /\.catch\(\(error\): PrefetchResult => \(\{ ok: false, error \}\)\)/,
      '失败没有被转成 { ok:false }',
    )

    // 旧的 fail-open 形态不能残留 —— 但**只在取正文那条路上**判。
    // `deps.readCache(id).catch(() => '')` 是合法且必要的：本地缓存读失败时
    // 应当继续走网络，而不是把整封邮件判成失败。第一版这里写的是全文件
    // doesNotMatch，结果匹配到了那一条**正确**的兜底，判据自己红了。
    const fetchAt = prefetch.indexOf('deps.fetchBody(id)')
    assert.notEqual(fetchAt, -1, '找不到 deps.fetchBody(id)（判据锚点已失效）')
    const tail = prefetch.slice(fetchAt)
    assert.doesNotMatch(
      tail,
      /\.catch\(\(\) => ''\)/,
      "取正文这条路上仍有 `.catch(() => '')`：失败被抹成空串，调用方无从区分",
    )
  })

  it('不走预取的那条分支仍然会抛（两条路径的失败表现必须一致）', () => {
    const b = fnBody(view, 'async function loadBodyInBackground(')
    assert.match(
      b,
      /extractEmailBody\(\(await emailApi\.getEmailBody\(/,
      '直连分支不再 await getEmailBody —— 失败与预取分支的表现就对不上了',
    )
    assert.match(
      b,
      /catch \(e: any\) \{[\s\S]*bodyError\.value = apiError\(e, 'errors\.loadEmailBodyFailed'\)/,
      'catch 里没有写 bodyError',
    )
  })
})

describe('判据自检：负控必须转红', () => {
  const body = fnBody(view, 'async function loadBodyInBackground(')

  it('把接线退回旧的三元形态 → 接线判据报出违规', () => {
    const good = judgeWiring(body)
    assert.deepEqual(good, [], '判据在真实代码上就没通过过（前提不成立）')

    // 按标记定位切片，不用一条大正则去描整个新接线块——第一版那么写，
    // 锚点因为缩进/换行差一点就没命中，负控直接变成 no-op（"没命中"的红
    // 与"判据生效"的红长得一样，必须分开）。
    const start = body.indexOf("let remote = ''")
    const end = body.indexOf('if (remote) {')
    assert.ok(start >= 0, '前提不成立：找不到 `let remote = \'\'`')
    assert.ok(end > start, '前提不成立：找不到新接线块之后的 `if (remote) {`')

    const oldWiring = [
      'const remote = isBodyPrefetching(found.id)',
      '      ? await prefetchEmailBody(found.id, bodyPrefetchDeps)',
      '      : extractEmailBody((await emailApi.getEmailBody(found.id)).body)',
      '',
      '    ',
    ].join('\n')
    const broken = body.slice(0, start) + oldWiring + body.slice(end)
    assert.notEqual(broken, body, '负控样本没有真的替换掉接线（切片没命中）')

    const bad = judgeWiring(broken)
    assert.ok(bad.length > 0, '负控本该转红却判成了通过 —— 接线判据抓不住这个缺陷')
    assert.match(bad[0], /当字符串判空|没有区分预取的 ok/, `报出来的违规与注入的缺陷对不上：${bad[0]}`)
  })

  it('把判别结果换回字符串 → 同样转红（不能只防一种写法）', () => {
    // 第二种退回形态：结果类型改了，但接线只判 truthy。
    const truthyOnly = body
      .replace('if (r.ok) {', 'if (r) {')
      .replace(/bodyError\.value = apiError\(r\.error, 'errors\.loadEmailBodyFailed'\)/g, '')
    assert.notEqual(truthyOnly, body, '负控样本没有真的改坏接线（替换没命中）')
    const bad = judgeWiring(truthyOnly)
    assert.ok(
      bad.some((v) => /没有区分预取的 ok|没有写进 bodyError/.test(v)),
      `负控本该转红却判成了通过：${JSON.stringify(bad)}`,
    )
  })

  it('把 `!bodyText.value` 保护去掉 → 报出"已有正文时不该再报错"', () => {
    const noGuard = body.replace('} else if (!bodyText.value) {', '} else {')
    assert.notEqual(noGuard, body, '负控样本没有真的摘掉保护（替换没命中）')
    const bad = judgeWiring(noGuard)
    assert.ok(
      bad.some((v) => /!bodyText\.value/.test(v)),
      `负控本该转红却判成了通过：${JSON.stringify(bad)}`,
    )
  })

  it('把 purged 当失败处理 → 报出"purged 是合法答案"', () => {
    const purgeAsFailure = body.replace('if (!r.purged) remote = r.body', 'remote = r.body')
    assert.notEqual(purgeAsFailure, body, '负控样本没有真的改掉 purged 分支（替换没命中）')
    const bad = judgeWiring(purgeAsFailure)
    assert.ok(
      bad.some((v) => /purged/.test(v)),
      `负控本该转红却判成了通过：${JSON.stringify(bad)}`,
    )
  })
})
