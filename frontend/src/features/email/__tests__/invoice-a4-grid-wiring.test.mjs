import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

// invoice-a4-grid-wiring.test.mjs — 需求 5「A4 2x2 / 3x3 拼版导出」的**客户端接线**。
//
// ## 为什么后端测得那么厚还不够
//
// 后端有三层覆盖：export_pdf_test.go（合并与容错）、export_pdf_geometry_test.go
// （A4 尺寸与落点）、export_pdf_border_test.go（裁切线真的画进 PDF）。
// 但需求 5 要用户能拿到一个「打印后可直接剪裁」的 PDF，靠的是**另外三段接线**：
//
//	InvoiceListView.vue  ──按钮 + 2×2/3×3 选择──▶  use-invoice-list.ts
//	    ──exportGrid()──▶  api/email.ts  ──POST /api/emails/invoices/export──▶  服务端
//
// 这三段在本文件出现之前**零覆盖**。把「导出 A4 2×2」那个按钮删掉，
// 后端三层测试照样全绿，gates 照样全绿，而需求 5 在产品里**静默消失**。
// 「后端有实现」与「用户点得到」是两件事，只有后者才叫交付。
//
// ## 第 3 条是跨边界对账，不是重述
//
// 合法的 grid 取值**只定义在服务端**（export_pdf.go 的 `grid != 2 && grid != 3`）。
// 前端这里写死 {2,3} 的话，服务端哪天放开或收紧，前端会安静地漂。
// 所以这条从**服务端源码里正则取真值**再与前端的类型对账——
// 数字不写死在测试里，对方改了我这边一定知道。

const HERE = path.dirname(fileURLToPath(import.meta.url)) // src/features/email/__tests__
const FEAT = path.resolve(HERE, '..') // src/features/email
const APIDIR = path.resolve(HERE, '..', '..', '..', 'api') // src/api
const REPO = path.resolve(HERE, '..', '..', '..', '..', '..') // openpocket/
const SERVER_EXPORT = path.join(REPO, 'backend', 'internal', 'email', 'export_pdf.go')

function readSrc(p) {
  return fs.readFileSync(p, 'utf8')
}

test('列表页有一个可点的 A4 导出入口，并把当前网格选择传进去', () => {
  const src = readSrc(path.join(FEAT, 'InvoiceListView.vue'))
  assert.match(
    src,
    /@click="exportGrid\(gridChoice\)"/,
    'InvoiceListView 没有把 gridChoice 传给 exportGrid —— 用户点「导出」不会带上网格密度',
  )
  // 按钮文案必须把网格密度说出来：用户点之前就该知道每页 4 张还是 9 张。
  assert.match(
    src,
    /导出 A4 \$\{gridChoice\}×\$\{gridChoice\}/,
    '导出按钮的文案里没有网格密度，用户无法在点击前确认排版',
  )
})

test('2×2 与 3×3 两个选项都在（需求原文点名了两种）', () => {
  const src = readSrc(path.join(FEAT, 'InvoiceListView.vue'))
  for (const g of [2, 3]) {
    assert.match(
      src,
      new RegExp(`@click="gridChoice = ${g}"`),
      `InvoiceListView 缺少 ${g}×${g} 选项 —— 需求 5 明确要求两种排版都能选`,
    )
  }
})

test('前端允许的网格取值与服务端一致（跨边界对账，不写死数字）', () => {
  // 读不到服务端源码必须**响亮失败**：静默 skip 会让这条判据变成恒真，
  // 而「恒真」正是本仓库反复踩的坑。
  assert.ok(
    fs.existsSync(SERVER_EXPORT),
    `读不到服务端源码 ${SERVER_EXPORT} —— 跨边界对账无法进行，` +
      '不是「通过」而是「没做」。路径变了就同步改本文件。',
  )
  const server = readSrc(SERVER_EXPORT)
  const serverGrids = [...server.matchAll(/grid\s*!=\s*(\d+)\s*&&\s*grid\s*!=\s*(\d+)/g)]
    .slice(0, 1)
    .flatMap(m => [Number(m[1]), Number(m[2])])
  assert.equal(
    serverGrids.length,
    2,
    `没从 ${path.basename(SERVER_EXPORT)} 里取到「合法 grid 取值」的判定式。` +
      '服务端改写了校验写法时必须同步更新本判据，而不是让它悄悄变成空集。',
  )

  // 前端：gridChoice 的类型联合就是它声称的合法集合。
  const view = readSrc(path.join(FEAT, 'InvoiceListView.vue'))
  const union = view.match(/ref<\s*(\d+)\s*\|\s*(\d+)\s*>/)
  assert.ok(union, 'InvoiceListView 里找不到 gridChoice 的 ref<2 | 3> 类型声明')
  const feGrids = [Number(union[1]), Number(union[2])]

  assert.deepEqual(
    [...feGrids].sort(),
    [...serverGrids].sort(),
    `网格取值两侧不一致：前端 ${feGrids} vs 服务端 ${serverGrids}。` +
      '前端多一个值 ⇒ 每次都拿那个值撞服务端的 400；前端少一个 ⇒ 需求写明的排版选不了。',
  )
})

test('导出结果要真的落到用户手上：调 API 之后必须下载文件', () => {
  const src = readSrc(path.join(FEAT, 'use-invoice-list.ts'))
  assert.match(
    src,
    /emailApi\.exportInvoicesGrid\(/,
    'use-invoice-list 没有调用 exportInvoicesGrid —— 按钮点了不会产生任何 PDF',
  )
  // 这一条是整个文件里最要紧的：只拿一个文件名、不把字节交给用户，
  // 与「服务端生成了文件但路径被丢进 _」是同一类失效——报告和日志都正常，
  // 用户手上什么都没有。同构事故已经在 PipelineReport.ShareDocCSV 上发生过一次。
  assert.match(
    src,
    /emailApi\.fetchInvoiceExport\(/,
    'use-invoice-list 调完 API 却没有 fetchInvoiceExport —— PDF 留在服务端，' +
      '用户点了「导出」什么也拿不到，而界面上什么异常都没有',
  )
})

test('API 层把 ids 与 grid 一起发到导出端点', () => {
  const src = readSrc(path.join(APIDIR, 'email.ts'))
  assert.match(
    src,
    /exportInvoicesGrid\([^)]*grid:\s*2\s*\|\s*3/,
    'exportInvoicesGrid 的 grid 参数类型不再是 2 | 3 —— 它与服务端的合法集合失去了连接',
  )
  assert.match(
    src,
    /'\/api\/emails\/invoices\/export'/,
    'exportInvoicesGrid 没有打 /api/emails/invoices/export —— 前端与后端契约已脱节',
  )
  assert.match(
    src,
    /body:\s*JSON\.stringify\(\{\s*ids,\s*grid\s*\}\)/,
    '导出请求体不是 { ids, grid } —— 服务端解析不出网格密度',
  )
})
