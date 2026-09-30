import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { parseEnex, type EvernoteNote } from './evernote-parser.ts'

// 真实 Evernote 导出（.enex）形态的样本：en-export 根 + DOCTYPE + 多 note +
// CDATA 包裹的 ENML 正文 + note-attributes + 多 resource（含/不含 attachment-hash）。
// 覆盖 fast-xml-parser 4→5 升级的行为契约（BUG 登记见 handoff §6.2 剔除项）。
const FULL_ENEX = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE en-export SYSTEM "http://xml.evernote.com/pub/evernote-export3.dtd">
<en-export export-date="20260930T081500Z" application="Evernote" version="Evernote Windows 10.32.4">
  <note>
    <title>买菜清单 &amp; 周末计划</title>
    <content><![CDATA[<?xml version="1.0" encoding="UTF-8" standalone="no"?>
<!DOCTYPE en-note SYSTEM "http://xml.evernote.com/pub/enml2.dtd">
<en-note style="word-wrap: break-word;">
  <div>牛奶两盒<br/>鸡蛋一打</div>
  <div><b>重要：</b>记得带 <i>会员卡</i></div>
  <en-todo checked="true"/>买咖啡豆
  <en-todo/>预约牙医
  <en-media hash="abc123def" type="image/png"/>
  <a href="https://example.com/recipe">菜谱链接</a>
</en-note>]]></content>
    <created>20260901T120000Z</created>
    <updated>20260915T093000Z</updated>
    <tag>生活</tag>
    <tag>购物</tag>
    <note-attributes>
      <author>张三</author>
      <source-url>https://www.example.com/list</source-url>
    </note-attributes>
    <resource>
      <data encoding="base64">iVBORw0KGgoAAAANSUhEUg==</data>
      <mime>image/png</mime>
      <width>16</width>
      <height>16</height>
      <resource-attributes>
        <file-name>coffee.png</file-name>
        <attachment-hash>abc123def</attachment-hash>
      </resource-attributes>
    </resource>
    <resource>
      <data encoding="base64">SGVsbG8gV29ybGQ=</data>
      <mime>text/plain</mime>
      <resource-attributes>
        <file-name>note.txt</file-name>
      </resource-attributes>
    </resource>
    <resource>
      <data encoding="base64"></data>
      <mime>application/octet-stream</mime>
      <resource-attributes>
        <file-name>empty.bin</file-name>
      </resource-attributes>
    </resource>
  </note>
  <note>
    <title>第二条：无资源的便签</title>
    <content><![CDATA[<?xml version="1.0" encoding="UTF-8" standalone="no"?>
<en-note><div>纯文本内容</div></en-note>]]></content>
    <created>20260910T080000Z</created>
  </note>
</en-export>`

// 单 note 时 Evernote 不产出数组——XMLParser 会把 note 解析成对象而非数组，
// parseEnex 必须自己包一层。
const SINGLE_NOTE_ENEX = `<?xml version="1.0" encoding="UTF-8"?>
<en-export>
  <note>
    <title>单条便签</title>
    <content><![CDATA[<en-note><div>hello <code>world</code></div></en-note>]]></content>
  </note>
</en-export>`

function parseFull(): EvernoteNote[] {
  return parseEnex(FULL_ENEX)
}

describe('parseEnex：结构与字段', () => {
  it('解析出两条笔记，标题实体已解码', () => {
    const notes = parseFull()
    assert.equal(notes.length, 2)
    assert.equal(notes[0].title, '买菜清单 & 周末计划')
    assert.equal(notes[1].title, '第二条：无资源的便签')
  })

  it('单 note 导出不丢条目（对象→数组包装）', () => {
    const notes = parseEnex(SINGLE_NOTE_ENEX)
    assert.equal(notes.length, 1)
    assert.equal(notes[0].title, '单条便签')
  })

  it('created/updated 按 Evernote 紧凑格式解析成时间戳；缺失时为 null', () => {
    const notes = parseFull()
    assert.equal(notes[0].createdAt, Date.parse('2026-09-01T12:00:00Z'))
    assert.equal(notes[0].updatedAt, Date.parse('2026-09-15T09:30:00Z'))
    // 第二条只有 created；updated 缺失必须是 null 而不是 NaN/0
    assert.equal(notes[1].createdAt, Date.parse('2026-09-10T08:00:00Z'))
    assert.equal(notes[1].updatedAt, null)
  })

  it('tag 是数组（多条）、author/source-url 来自 note-attributes', () => {
    const [first] = parseFull()
    assert.deepEqual(first.tags, ['生活', '购物'])
    assert.equal(first.author, '张三')
    assert.equal(first.sourceUrl, 'https://www.example.com/list')
    // 第二条没有 note-attributes 与 tag
    assert.deepEqual(parseFull()[1].tags, [])
    assert.equal(parseFull()[1].author, null)
  })

  it('content 保留 ENML 原文（未加工）', () => {
    const [first] = parseFull()
    assert.ok(first.content.includes('<en-note'), 'content 应保留 en-note 标签')
    assert.ok(first.content.includes('<en-todo checked="true"/>'))
  })
})

describe('parseEnex：resource', () => {
  it('data/mime/filename/hash 逐字段解析；空 data 的 resource 被过滤', () => {
    const [first] = parseFull()
    // 样本有 3 个 resource，其中第 3 个 data 为空 → 只剩 2 个
    assert.equal(first.resources.length, 2)
    const [png, txt] = first.resources
    assert.equal(png.mime, 'image/png')
    assert.equal(png.filename, 'coffee.png')
    assert.equal(png.data, 'iVBORw0KGgoAAAANSUhEUg==')
    assert.equal(png.hash, 'abc123def', 'attachment-hash 存在时必须原样采用')
    assert.equal(txt.mime, 'text/plain')
    assert.equal(txt.filename, 'note.txt')
    assert.equal(txt.data, 'SGVsbG8gV29ybGQ=')
  })

  it('缺 attachment-hash 时回退 stableHash（enex- 前缀且确定性）', () => {
    const [first] = parseFull()
    const txt = first.resources[1]
    assert.ok(txt.hash.startsWith('enex-'), `hash 应为 stableHash 产物，got ${txt.hash}`)
    // 同一输入再解析一次，hash 必须一致（enexId 幂等的基石）
    const again = parseFull()[0].resources[1]
    assert.equal(again.hash, txt.hash)
  })
})

describe('parseEnex：ENML → Markdown（Node 无 DOMParser 的降级路径）', () => {
  // Node 22 没有 DOMParser，enmlToMarkdown 走正则降级：去标签 + br→换行。
  // 这条链在浏览器里走 DOMParser，两条路径不能互为断言——这里只锁降级路径可见的行为。
  it('正文转纯文本：br 变换行，标签剥净，附件/待办标记不残留尖括号', () => {
    const [first] = parseFull()
    assert.ok(first.contentMarkdown.includes('牛奶两盒'))
    assert.ok(first.contentMarkdown.includes('鸡蛋一打'))
    assert.ok(/牛奶两盒\s*\n\s*鸡蛋一打/.test(first.contentMarkdown), '<br/> 应转成换行')
    assert.ok(first.contentMarkdown.includes('买咖啡豆'))
    assert.ok(first.contentMarkdown.includes('菜谱链接'))
    assert.ok(!first.contentMarkdown.includes('<'), '降级路径不应残留任何标签')
    assert.ok(!first.contentMarkdown.includes('en-media'))
  })

  it('content 为空时 contentMarkdown 为空串（不抛异常）', () => {
    const notes = parseEnex(`<?xml version="1.0"?><en-export><note><title>空</title></note></en-export>`)
    assert.equal(notes.length, 1)
    assert.equal(notes[0].contentMarkdown, '')
    assert.equal(notes[0].content, '')
  })
})

describe('parseEnex：enexId 稳定性', () => {
  it('同一笔记重复解析 id 不变；内容不同 id 不同', () => {
    const [a, b] = parseFull()
    const a2 = parseFull()[0]
    assert.equal(a.enexId, a2.enexId)
    assert.ok(a.enexId.startsWith('enex-'))
    assert.notEqual(a.enexId, b.enexId)
  })
})

describe('parseEnex：非正常输入', () => {
  it('空字符串返回空数组，不抛异常', () => {
    assert.deepEqual(parseEnex(''), [])
  })

  it('非 en-export 根返回空数组', () => {
    assert.deepEqual(parseEnex('<?xml version="1.0"?><other><note><title>x</title></note></other>'), [])
  })

  it('en-export 下没有 note 也返回空数组', () => {
    assert.deepEqual(parseEnex('<?xml version="1.0"?><en-export></en-export>'), [])
  })
})
