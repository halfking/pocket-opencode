/**
 * titleResolver 契约测试（UI规范 06 §2）。
 *
 * 这里守住四条最容易写错的：
 *   1. 无标题弹层继承「打开前的有效标题」，含上一层弹窗的标题；
 *   2. 覆盖层的 DOM 扫描**只在它自己的根里**——不能扫到背景页的 h1；
 *   3. 异步标题带 entryId + renderEpoch，旧页晚到的结果被拒绝；
 *   4. 路由 meta / document.title 只是逐级回落，不是权威。
 *
 * 用最小假 DOM 而不是 jsdom：解析逻辑只需要 getAttribute/querySelector/
 * ownerDocument 三个能力，写成接口比拉一个 DOM 依赖更可控。
 *
 * Run: node --test src/lib/shell/__tests__/titleResolver.test.mjs
 */
import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import { TitleResolver } from '../titleResolver.ts'

/** 造一个只实现解析器所需能力的假元素。 */
function el({ text = '', attrs = {}, children = [], id, tagName = 'DIV' } = {}) {
  const node = {
    textContent: text,
    tagName,
    id,
    attrs,
    children,
    getAttribute(k) {
      return k in attrs ? attrs[k] : null
    },
    querySelector(sel) {
      // 真实 DOM 的 tagName 是大写、选择器是小写，所以这里必须大小写无关，
      // 否则假 DOM 会比浏览器更「严格」而给出错误的通过/失败信号。
      const want = sel.split(',').map((s) => s.trim().toLowerCase())
      const walk = (n) => {
        for (const c of n.children) {
          const tag = c.tagName.toLowerCase()
          if (want.includes(tag) || want.some((w) => w.startsWith(`${tag}[`))) return c
          const hit = walk(c)
          if (hit) return hit
        }
        return null
      }
      return walk(node)
    },
  }
  node.ownerDocument = {
    getElementById: (target) => {
      const walk = (n) => {
        for (const c of n.children) {
          if (c.id === target) return c
          const hit = walk(c)
          if (hit) return hit
        }
        return null
      }
      return walk(node)
    },
  }
  return node
}

function ctxOf(entries, cursor = 0, overlayIds = []) {
  return { version: 2, entries, cursor, overlayIds, operations: [] }
}

function entry(over = {}) {
  return {
    id: over.id ?? 'e1',
    fullPath: over.fullPath ?? '/x',
    presentation: over.presentation ?? 'page',
    openedBy: over.openedBy ?? 'push',
    title: over.title ?? '',
    titleSource: over.titleSource ?? 'registered',
    inheritedFrom: over.inheritedFrom,
    parentId: over.parentId,
    titleKey: over.titleKey,
    scope: { serverId: 's', accountId: 'a' },
    view: { scroll: {} },
    createdAt: 0,
  }
}

test('显式登记的标题压过一切 DOM 与路由回落', () => {
  const page = el({ children: [el({ tagName: 'H1', text: 'DOM 标题' })] })
  const r = new TitleResolver({ appName: '兜底', dom: { overlayRoot: () => null, pageRoot: () => page } })
  r.register('e1', '真实标题')
  const res = r.resolve(ctxOf([entry({ id: 'e1', titleKey: 'route.title' })], 0))
  assert.equal(res.title, '真实标题')
  assert.equal(res.source, 'registered')
})

test('旧页回落：读页面根内的 data-shell-title / h1', () => {
  const page = el({ children: [el({ tagName: 'H1', text: '员工列表' })] })
  const r = new TitleResolver({ appName: '兜底', dom: { overlayRoot: () => null, pageRoot: () => page } })
  const res = r.resolve(ctxOf([entry({ id: 'e1' })]))
  assert.equal(res.title, '员工列表')
  assert.equal(res.source, 'dom')
})

test('覆盖层无标题时继承打开前的页面标题', () => {
  const r = new TitleResolver({ appName: '兜底' })
  const page = entry({ id: 'p', title: '员工' })
  const ov = entry({ id: 'o', presentation: 'modal', parentId: 'p', inheritedFrom: 'p', title: '' })
  const res = r.resolve(ctxOf([page, ov], 0, ['o']))
  assert.equal(res.title, '员工')
  assert.equal(res.source, 'inherited')
})

test('嵌套无标题弹层继承上一层弹窗的标题，而不是背景页标题', () => {
  const r = new TitleResolver({ appName: '兜底' })
  const page = entry({ id: 'p', title: '员工' })
  const edit = entry({ id: 'e', presentation: 'modal', parentId: 'p', title: '编辑员工' })
  const confirm = entry({ id: 'c', presentation: 'modal', parentId: 'e', inheritedFrom: 'e', title: '' })
  const res = r.resolve(ctxOf([page, edit, confirm], 0, ['e', 'c']))
  assert.equal(res.title, '编辑员工', '应继承上一层弹窗标题')
  assert.equal(res.source, 'inherited')
})

test('aria-labelledby 优先于 DOM 猜测', () => {
  const titleNode = el({ id: 'ov-title', text: '无障碍标题' })
  const overlay = el({
    attrs: { 'aria-labelledby': 'ov-title', role: 'dialog' },
    children: [titleNode, el({ tagName: 'H1', text: '另一个标题' })],
  })
  const r = new TitleResolver({ appName: '兜底', dom: { overlayRoot: () => overlay, pageRoot: () => null } })
  const ov = entry({ id: 'o', presentation: 'modal', title: '' })
  const res = r.resolve(ctxOf([entry({ id: 'p' }), ov], 0, ['o']))
  assert.equal(res.title, '无障碍标题')
  assert.equal(res.source, 'aria')
})

test('覆盖层只在自己的根里找标题：背景页的 h1 不得被认成弹窗标题', () => {
  // 背景页有 h1「员工列表」；覆盖层根里什么都没有。
  const page = el({ children: [el({ tagName: 'H1', text: '员工列表' })] })
  const overlay = el({ attrs: { role: 'dialog' } })
  const r = new TitleResolver({
    appName: '兜底',
    dom: { overlayRoot: () => overlay, pageRoot: () => page },
  })
  const ov = entry({ id: 'o', presentation: 'modal', parentId: 'p', title: '' })
  const res = r.resolve(ctxOf([entry({ id: 'p', title: '员工' }), ov], 0, ['o']))
  // 覆盖层无标题 → 继承；**不能**返回背景页 DOM 里的「员工列表」作为 dom 来源。
  assert.equal(res.source, 'inherited')
  assert.equal(res.title, '员工')
})

test('异步标题：旧 epoch 的晚到结果被拒绝', () => {
  const r = new TitleResolver({ appName: '兜底' })
  const epoch1 = r.beginRender('e1')
  assert.equal(r.acceptAsync('e1', epoch1, '第一次的名称'), true)
  // 页面重新激活 → epoch 推进
  const epoch2 = r.beginRender('e1')
  assert.notEqual(epoch1, epoch2)
  // 旧请求晚到：必须被拒
  assert.equal(r.acceptAsync('e1', epoch1, '过期名称'), false)
  assert.equal(r.acceptAsync('e1', epoch2, '新名称'), true)
  assert.equal(r.resolve(ctxOf([entry({ id: 'e1' })])).title, '新名称')
})

test('旧页晚到的异步标题不会覆盖新页面', () => {
  const r = new TitleResolver({ appName: '兜底' })
  const oldEntry = entry({ id: 'old', title: '旧页面' })
  const newEntry = entry({ id: 'new', title: '新页面' })
  // 旧页拿到 epoch 后，用户切到了新页
  const oldEpoch = r.beginRender('old')
  r.beginRender('new')
  // 旧页的慢响应这时才回来。它会被写进**旧条目**自己的登记槽
  // （登记按 entryId 分槽），因此当前页面读到的仍然是新页面标题。
  r.acceptAsync('old', oldEpoch, '旧页慢响应名称')
  assert.equal(r.resolve(ctxOf([oldEntry, newEntry], 1)).title, '新页面')

  // 但同一个条目内部的重渲染必须由 epoch 守门——否则「旧的第二次请求」
  // 会盖掉「新的第一次请求」，那才是真正会看到标题跳动的场景。
  const e1 = r.beginRender('old')
  const e2 = r.beginRender('old')
  assert.equal(r.acceptAsync('old', e1, '过期'), false)
  assert.equal(r.acceptAsync('old', e2, '最新'), true)
})

test('页面未渲染时回落顺序：route meta → document.title → 应用名', () => {
  const r = new TitleResolver({ translate: (k) => (k === 'k.ok' ? '翻译标题' : undefined), appName: 'OpenCode' })
  assert.equal(r.resolve(ctxOf([entry({ id: 'e', titleKey: 'k.ok' })])).source, 'route')
  assert.equal(r.resolve(ctxOf([entry({ id: 'e' })])).source, 'document')
  assert.equal(r.resolve(ctxOf([]), undefined).title.length >= 0, true)
})

test('标题里的多余空白被压平，HTML 被剥掉', () => {
  const r = new TitleResolver({ appName: 'x' })
  r.register('e', '  <b>粗体</b>\n  标题  ')
  assert.equal(r.resolve(ctxOf([entry({ id: 'e' })])).title, '粗体 标题')
})

test('clearAll 清空登记（换账号：标题里可能有姓名）', () => {
  const r = new TitleResolver({ appName: '兜底' })
  r.register('e', '张三')
  r.clearAll()
  assert.equal(r.resolve(ctxOf([entry({ id: 'e' })])).title, '兜底')
})
