import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

// invoice-feishu-push-wiring.test.mjs — 需求 3「发送到飞书上…如果没有办法发送，
// 可以建立共享文档及文件」的**客户端接线**。
//
// ## 需求落在两个分支上
//
// 服务端 handleEmailInvoicePush（server_email_pipeline.go:507-527）：
//
//	FeishuPushed > 0          → 真的发出去了，用户在飞书里看到文件
//	FeishuPushed == 0         → 兜底：建共享台账（shareDocUrl）+ 本地 CSV/MD
//	                            + message
//
// 也就是说**需求 3 的兜底交付物只在「一条都没推出去」这条分支上产生**。
// 客户端如果只弹一句 toast，那个链接一闪而过 ⇒ 共享文档建了但没人找得到，
// 需求的「可以建立共享文档及文件」在产品里等于没做。
//
// ## 为什么这值得判
//
// 后端这条链有 `feishu_skip_reason_test.go`、`invoice-totals-chain` 等覆盖，
// 但客户端这一段此前**零覆盖**：按钮、状态留存、链接渲染全靠人记。
// 与本轮另两个接线判据（需求 5 的 A4 导出、需求 1 的 dryRun 顺序）同类：
// 后端证明「算法对」，接线证明「用户拿得到」。

const HERE = path.dirname(fileURLToPath(import.meta.url)) // src/features/email/__tests__
const FEAT = path.resolve(HERE, '..')
const APIDIR = path.resolve(HERE, '..', '..', '..', 'api')
const REPO = path.resolve(HERE, '..', '..', '..', '..', '..')
const SERVER = path.join(REPO, 'backend', 'internal', 'server', 'server_email_pipeline.go')

const readSrc = p => fs.readFileSync(p, 'utf8')
const viewSrc = () => readSrc(path.join(FEAT, 'InvoiceListView.vue'))
const listSrc = () => readSrc(path.join(FEAT, 'use-invoice-list.ts'))

test('列表页有「推送飞书」入口并调用 pushFeishu', () => {
  const src = viewSrc()
  assert.match(src, /@click="pushFeishu\(\)"/, '「推送飞书」按钮不再调用 pushFeishu —— 判据可能已失效')
  assert.match(src, /推送飞书/, '发票页没有「推送飞书」按钮 —— 需求 3 的入口在产品里不存在')
  assert.match(src, /:disabled="pushing"/, '推送按钮没有在推送中禁用 —— 用户可连点，重复推送')
})

test('pushFeishu 必须把 shareDocUrl 留下来，不能只弹 toast', () => {
  const src = listSrc()
  const i = src.indexOf('async function pushFeishu(')
  assert.notEqual(i, -1, 'use-invoice-list.ts 里找不到 pushFeishu —— 判据可能已失效')
  const body = src.slice(i, i + 1200)
  assert.match(
    body,
    /shareDocUrl\.value\s*=\s*res\.shareDocUrl/,
    'pushFeishu 没有把 res.shareDocUrl 存进状态 —— 推不出去时服务端建的共享台账\n' +
      '  链接会被丢掉。用户看到一句「已生成共享汇总文档」就再也没法打开它，\n' +
      '  而需求 3 明确要求「可以建立共享文档及文件」供后续整理。',
  )
  // 兜底信息不能被 toast 吃掉：toast 是瞬时的，状态是持久的。
  assert.match(
    body,
    /toast\.(success|info|warning)\(/,
    'pushFeishu 没有任何结果反馈 —— 判据可能已失效（用户不知道推没推出去）',
  )
})

test('共享台账链接必须是可点的 <a href>，且新窗口带 noopener', () => {
  const src = viewSrc()
  assert.match(
    src,
    /v-if="shareDocUrl"/,
    '视图没有在 shareDocUrl 存在时渲染任何东西 —— 状态存了但用户看不到',
  )
  assert.match(src, /:href="shareDocUrl"/, '共享台账不是一个 href 链接（可能只渲染成了文字）')
  assert.match(src, /target="_blank"/, '共享台账链接不开新窗口')
  assert.match(
    src,
    /rel="noopener noreferrer"/,
    'target="_blank" 的链接缺少 rel="noopener noreferrer" —— 新窗口能反向操作本页',
  )
})

test('API 层打到 /api/emails/invoices/push，省略 ids 时发空体（= 全部待推）', () => {
  const src = readSrc(path.join(APIDIR, 'email.ts'))
  assert.match(src, /'\/api\/emails\/invoices\/push'/, 'pushInvoicesToFeishu 的端点变了 —— 与后端契约脱节')
  // 「不勾选就推全部」是需求 1「手工处理」最常用的一条路径。
  // 若这里发成 {ids: undefined} 或 {ids: []}，服务端会收到空数组而不是「省略」，
  // 两条路径在 Go 侧语义不同（省略=推全部 downloaded 且未推送）。
  assert.match(
    src,
    /body:\s*JSON\.stringify\(ids\s*\?\s*\{\s*ids\s*\}\s*:\s*\{\s*\}\)/,
    'pushInvoicesToFeishu 不再在省略 ids 时发 {} —— 「不勾选=推全部」这条语义断了',
  )
})

test('跨边界对账：前端读的字段名都真的由服务端返回', () => {
  assert.ok(
    fs.existsSync(SERVER),
    `读不到服务端源码 ${SERVER} —— 跨边界对账无法进行，不是「通过」而是「没做」。`,
  )
  const server = readSrc(SERVER)
  const fnAt = server.indexOf('func (s *Server) handleEmailInvoicePush')
  assert.notEqual(fnAt, -1, '服务端找不到 handleEmailInvoicePush —— 判据可能已失效')

  // 按大括号配平截取函数体。
  //
  // **不要用 fn.indexOf('\n}\n')**：本文件 100% CRLF（实测 624 个 CRLF、0 个
  // 裸 LF），那个模式永远匹配不到，slice(0, -1) 于是吞掉后面**所有** handler，
  // 把下游函数返回的键也算进来。负控实测过这个失败：把服务端 shareDocUrl
  // 改名后判据依然全绿，因为它从下游函数里又捡到了同名键 —— 恒真的判据。
  const openBrace = server.indexOf('{', fnAt)
  let depth = 0
  let end = -1
  for (let k = openBrace; k < server.length; k++) {
    if (server[k] === '{') depth++
    else if (server[k] === '}') {
      depth--
      if (depth === 0) { end = k; break }
    }
  }
  assert.notEqual(end, -1, 'handleEmailInvoicePush 的大括号不配平 —— 判据可能已失效')
  const body = server.slice(openBrace + 1, end)

  // 自检：确认截到的**就是这一个函数**。没有这条，上面那个越界会静默复发。
  assert.ok(
    !body.includes('func (s *Server) handleEmailInvoiceSummary'),
    '截取越界：函数体里混进了下一个 handler。判据会把下游返回的键也算进来，' +
      '于是「前端读了服务端不返回的字段」永远抓不到（恒真）。',
  )
  assert.ok(
    body.includes('result := map[string]any{'),
    '函数体里找不到响应 map 字面量 —— 判据的解析前提没了，这不是「通过」而是「没做」。',
  )

  // 服务端返回的键 = map 字面量里的键 ∪ 后面 result["k"] = ... 的条件赋值键
  const literal = body.slice(body.indexOf('result := map[string]any{'))
  const serverKeys = new Set()
  for (const m of literal.matchAll(/^\s*"([a-zA-Z]+)":/gm)) serverKeys.add(m[1])
  for (const m of body.matchAll(/result\["([a-zA-Z]+)"\]/g)) serverKeys.add(m[1])
  assert.ok(
    serverKeys.size >= 3,
    `只从服务端解析出 ${serverKeys.size} 个返回键（${[...serverKeys]}）—— ` +
      '解析方式失效了，这不是「通过」。',
  )

  // 前端接口的字段名。
  // 注意必须**按大括号配平截取接口体**：用 slice(bodyStart+1) 会把该接口
  // 之后整个文件都算进来，于是 PipelineReport 的 startedAt/feishuPushed
  // 之类字段被误判成「前端读了服务端不返回的字段」——判据自己先红了。
  const api = readSrc(path.join(APIDIR, 'email.ts'))
  const ifaceAt = api.indexOf('export interface EmailInvoicePushResult {')
  assert.notEqual(ifaceAt, -1, 'email.ts 里找不到 EmailInvoicePushResult —— 判据可能已失效')
  const ifaceOpen = api.indexOf('{', ifaceAt)
  let ifaceDepth = 0
  let ifaceEnd = -1
  for (let k = ifaceOpen; k < api.length; k++) {
    if (api[k] === '{') ifaceDepth++
    else if (api[k] === '}') {
      ifaceDepth--
      if (ifaceDepth === 0) { ifaceEnd = k; break }
    }
  }
  assert.notEqual(ifaceEnd, -1, 'EmailInvoicePushResult 的大括号不配平 —— 判据可能已失效')
  const feKeys = new Set(
    [...api.slice(ifaceOpen + 1, ifaceEnd).matchAll(/^\s{2}([a-zA-Z]+)\??:/gm)].map(m => m[1]),
  )
  assert.ok(feKeys.size >= 3, `前端只解析出 ${feKeys.size} 个字段——解析方式失效了`)

  const missing = [...feKeys].filter(k => !serverKeys.has(k))
  assert.deepEqual(
    missing, [],
    `前端会读但服务端从不返回的字段：${missing}。\n` +
      `  服务端实际返回：${[...serverKeys].sort().join(', ')}\n` +
      '  这类字段在 TS 里是合法的，运行时恒为 undefined —— 比编译报错更难查。',
  )
})
