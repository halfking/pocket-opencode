/**
 * 发票缩略图决策逻辑回归测试（2026-10-01 真机审计 P0：标题图必须是 PDF 缩略图）。
 *
 * 背景：原先缩略图只有一条来源——后端 /thumb，对 PDF 只能抽**内嵌位图**。
 * 增值税电子发票多为「矢量文字 + 版式」PDF，不含位图 → 后端 404 →
 * 卡片左侧长期是灰色文档图标，用户「点标题图看不到发票长什么样」。
 * 实测：云服务开票中心-1280.00.pdf（1537B 文字型）→ 抽不出图。
 *
 * 这里验证的是**取图决策**（planThumbStrategy）与卡片交互契约。
 * 决策被抽成纯函数，因此不依赖网络/原生插件，可直接 node --test。
 */
import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { planThumbStrategy, thumbCacheFileName } from '../invoice-thumb-plan.ts'

test('文字型 PDF 在原生可用时走页面栅格化（需求核心）', () => {
  // 没有这一步，文字型发票就永远是灰色图标。
  assert.equal(planThumbStrategy('餐饮-甲-10.00.pdf', true), 'native-render')
  assert.equal(planThumbStrategy('INVOICE-2026.PDF', true), 'native-render', '大写扩展名也要认')
})

test('web/iOS 无原生渲染时退回后端 thumb', () => {
  assert.equal(planThumbStrategy('发票.pdf', false), 'server-thumb')
})

test('图片类附件只走后端 thumb（后端原样返回，无需渲染）', () => {
  assert.equal(planThumbStrategy('餐饮-甲-10.00.jpg', true), 'server-thumb')
  assert.equal(planThumbStrategy('receipt.png', false), 'server-thumb')
  assert.equal(planThumbStrategy('scan.webp', true), 'server-thumb')
})

test('无文件名时按后端 thumb 处理（不误触发 PDF 渲染）', () => {
  assert.equal(planThumbStrategy(undefined, true), 'server-thumb')
  assert.equal(planThumbStrategy('', true), 'server-thumb')
})

test('Cache 文件名稳定且已净化路径分隔符', () => {
  assert.equal(thumbCacheFileName('inv-123'), 'thumb-inv-123.pdf')
  // id 里若混入 ../ 之类，必须被替换掉，否则会写到 Cache 目录之外。
  const name = thumbCacheFileName('../../etc/passwd')
  assert.ok(!name.includes('/'), `不应残留路径分隔符：${name}`)
  assert.ok(!name.includes('\\'))
  assert.equal(name, 'thumb-.._.._etc_passwd.pdf')
  // 同一 id 每次都得到同一文件名 → 重复渲染是覆盖而非堆积。
  assert.equal(thumbCacheFileName('inv-1'), thumbCacheFileName('inv-1'))
})

// ── 卡片交互契约（直接断言真实源码，防止样式/事件被改回去）──────────────────

const cardSrc = readFileSync(fileURLToPath(new URL('../InvoiceCard.vue', import.meta.url)), 'utf8')

test('缩略图不裁切：A4 竖版页要完整可见', () => {
  // 槽位 64x80，缩略图 CSS 必须 contain + 顶部对齐：
  // cover 会把发票抬头与金额裁掉，用户认不出内容。
  assert.match(cardSrc, /object-fit:\s*contain/, '缩略图不得用 cover 裁切')
  assert.match(cardSrc, /object-position:\s*top center/, '应对齐顶部，保留抬头与金额')
})

test('缩略图带 lazy/async 解码，避免解码阻塞列表滚动', () => {
  assert.match(cardSrc, /loading="lazy"/)
  assert.match(cardSrc, /decoding="async"/)
})

test('点击标题图打开发票详情；仅在无文件时禁用', () => {
  assert.match(cardSrc, /@click\.stop="emit\('preview'\)"/, '点击标题图应触发预览')
  assert.match(cardSrc, /:disabled="!hasFile"/, '无文件时才禁用')
})

test('取图中显示加载态，避免闪一下「无图」图标', () => {
  assert.match(cardSrc, /thumbPending/, '应区分「正在取」与「取完没有」')
  assert.match(cardSrc, /thumb-busy|hourglass_top/, '取图期间应有加载指示')
})
