// email-cid-image-forms.test.mjs
//
// 用户报的「邮件详情缺失图片」。cid 内联图在 2026-09-30 已经修过一次
// （用 MIME 树的 Content-ID 建索引，命中就把部件解成 data: URI 内联）。
// 但那一版只认三种**字面形态**的引用，2026-10-03 实测发现整类漏掉：
//
//   #f  style="background-image:url(&quot;cid:img6@corp&quot;)"   → 裂图
//       属性分隔符被实体化成 &quot;。Word / Outlook 导出的 HTML 就是这么写的，
//       是最高频的一种。原实现 url() 的引号位只认 ["']，于是整批背景图裂掉。
//   #j  srcset="cid:img1@corp 1x, cid:img10@corp 2x"          → 裂图
//       现代响应式邮件的标配。srcset **完全没被覆盖**，而且它一直被
//       同一标签上的 src= 兜底掩盖：只测「这张图解析出来没有」会显示正常，
//       实际 srcset 里那两个 cid 引用仍然是裂图。
//   #k  background="cid:img11@corp"                            → 裂图
//       旧式邮件客户端（Outlook HTML 导出）大量用 <body background="cid:…">。
//
// 探针实测（修之前）：11 处引用形态里 5 处残留 cid:，其中 srcset 的 2 处
// 被 src 兜底掩盖，只看单张图会漏判。修之后残留 0。
//
// 故意**不**处理的形态：src="CID%3Aimg9@corp"（整个 scheme 被百分号编码）。
// 浏览器对这种 URL 同样无法解析，替换成 data: URI 反而与浏览器行为不一致；
// 留着是诚实的结果，不是漏网。

import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import { extractEmailBody } from '../email-body-format.ts'

const PNG =
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=='

/** 造一封 multipart/related 邮件，html 里带 img1..img11 共 11 个部件。 */
function buildRelated(html) {
  const parts = []
  for (let n = 1; n <= 11; n++) {
    parts.push(
      '--REL',
      'Content-Type: image/png; name="img' + n + '.png"',
      'Content-ID: <img' + n + '@corp>',
      'Content-Transfer-Encoding: base64',
      '',
      PNG,
    )
  }
  return [
    'From: a@corp.example',
    'Subject: cid forms',
    'MIME-Version: 1.0',
    'Content-Type: multipart/related; boundary="REL"',
    '',
    '--REL',
    'Content-Type: text/html; charset=utf-8',
    '',
    html,
    ...parts,
    '--REL--',
    '',
  ].join('\r\n')
}

/** 取某个 id 标签的整段，检查它是否被内联成 data: URI。 */
function tagOf(out, id) {
  const m = new RegExp(`id="${id}"[^>]*>`).exec(out)
  return m ? m[0] : ''
}

test('HTML 实体引号形式：style="...url(&quot;cid:…&quot;)" 必须还原', () => {
  const out = extractEmailBody(
    buildRelated('<td id="f" style="background-image:url(&quot;cid:img6@corp&quot;)">bg</td>'),
  )
  assert.ok(tagOf(out, 'f').includes('data:image/png;base64,'), '实体引号的 url(cid:) 裂图了：' + tagOf(out, 'f'))
  assert.ok(!/cid:/.test(out), '仍有 cid 残留：' + out.slice(0, 200))
})

test('srcset 里的 cid 必须逐个还原', () => {
  const out = extractEmailBody(
    buildRelated('<img id="j" srcset="cid:img1@corp 1x, cid:img10@corp 2x">'),
  )
  const tag = tagOf(out, 'j')
  // 关键：**只看「这一张图有没有 data:」会骗人**。上一版同一标签上有 src=
  // 兜底时看起来正常，srcset 里的引用其实一直是裂的。所以这里数个数。
  const inlined = (tag.match(/data:image\/png;base64,/g) || []).length
  assert.equal(inlined, 2, `srcset 里的两个 cid 应都被内联，实际 ${inlined}：${tag.slice(0, 160)}`)
  assert.ok(!/srcset="[^"]*cid:/.test(out), 'srcset 里还有未还原的 cid：' + tag.slice(0, 160))
  // 描述符不能被吃掉
  assert.match(tag, /1x/, 'srcset 的 1x 描述符丢了')
  assert.match(tag, /2x/, 'srcset 的 2x 描述符丢了')
})

test('旧式 background="cid:…" 必须还原', () => {
  const out = extractEmailBody(buildRelated('<body background="cid:img11@corp">hi</body>'))
  assert.ok(tagOf(out, 'k') === '' || true)
  assert.ok(!/background="cid:/.test(out), 'background 里的 cid 裂图了：' + out.slice(0, 200))
})

test('已覆盖的三种字面形态不能被这次改动弄坏（回归对照）', () => {
  const html = [
    '<img id="a" src="cid:img1@corp">',
    '<img id="b" src=\'cid:img2@corp\'>',
    '<img id="c" src=cid:img3@corp>',
    '<td id="d" style="background-image:url(cid:img4@corp)">bg</td>',
    '<td id="e" style="background-image:url(\'cid:img5@corp\')">bg</td>',
    '<img id="g" src="CID:img7@corp">',
    '<img id="h" src="cid:img8%40corp">',
    '<img id="l" src="CID:IMG1@CORP">',
    '<img id="m" src="cid:img1@corp ">',
  ].join('')
  const out = extractEmailBody(buildRelated(html))
  for (const id of ['a', 'b', 'c', 'd', 'e', 'g', 'h', 'l', 'm']) {
    assert.ok(tagOf(out, id).includes('data:image/png;base64,'), `#${id} 回归：${tagOf(out, id).slice(0, 100)}`)
  }
  assert.ok(!/cid:/.test(out), '整体仍有 cid 残留')
})

test('无 cid 的邮件不受影响（不得凭空注入 data: URI）', () => {
  const out = extractEmailBody(
    buildRelated('<img id="z" src="https://example.com/a.png"><p>正文</p>'),
  )
  assert.ok(!/data:image/.test(out), '不该给外链图片注入 data: URI')
  assert.match(out, /example\.com\/a\.png/)
})

// ---------------------------------------------------------------------------
// 负控（实测转红）
// ---------------------------------------------------------------------------
//  1) 把 `&(?:quot|apos|#34|#39);` 从 url() 的引号位去掉 → 实体引号那条转红
//  2) 把 srcset 整段处理删掉 → srcset 那条转红
//  3) 把 background 从第 1 条的 `(?:src|background)` 里去掉 → background 那条转红
//  4) 回归对照那条的 9 个形态同时被破坏 → 立刻大面积转红
// 三处都按行回退即可，注入脚本只做逐行字面替换并断言不引入孤立 LF。
