/**
 * ia-smoke.mjs — 2026-10-03 全局 IA 重组的真渲染冒烟。
 *
 * 为什么需要它：gates 全绿只证明「类型对、i18n 齐、图标有字形、单测过」，
 * 证明不了**页面在浏览器里真的渲染出来了**。本轮新增了两个聚合页（/notes、
 * /messages）并重排了 BottomNav，这三样都只有真渲染才暴露：
 *
 *   1. 组件语法 / 模板编译问题；
 *   2. i18n key 运行时解析不到，界面渲染出字面量 "notesHub.filter.all"
 *      —— 本轮真的踩过：带点的平铺键能过静态卡口，运行时却断；
 *   3. BottomNav 四个 tab 的渲染与激活态；
 *   4. 来源 chips 是否出现、是否可点。
 *
 * ## 为什么要走完整的登录 + 解锁流程
 *
 * 直接改 hash 导航进不去：routeGuards 有两道门——requiresAuth（要 pocket_token）
 * 和 requiresLobster（要主密码解锁本地加密库）。/notes 与 /messages 都是
 * requiresLobster:true，绕过守卫去断言等于测了一个用户永远看不到的状态。
 * 所以这里走真实链路：注入 token → 被守卫弹到 /login?unlock=1 → 填主密码解锁
 * → 回到目标页。API 全部 stub 成空壳（只关心渲染，不关心数据）。
 *
 * 用法：
 *   MOBILE_ALLOW_EMPTY_API_BASE=1 npm run build
 *   node scripts/ia-smoke.mjs
 */
import { chromium } from 'playwright'
import { createServer } from 'node:http'
import { readFile, stat } from 'node:fs/promises'
import { join, extname, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..')
const DIST = join(ROOT, 'dist')
const MASTER_PW = 'ia-smoke-master-12345'
const TOKEN = 'ia-smoke-token'

const MIME = {
  '.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css',
  '.json': 'application/json', '.woff2': 'font/woff2', '.svg': 'image/svg+xml',
  '.png': 'image/png', '.ico': 'image/x-icon',
  // sql.js（本地库 web 回落路径）要 .wasm。不给对 MIME，浏览器会拒绝
  // streaming compile 并回落到 ArrayBuffer——回落本身能用，但控制台会留下
  // 一条 error，把真正的渲染失败淹掉。
  '.wasm': 'application/wasm',
}

const server = createServer(async (req, res) => {
  const url = (req.url || '/').split('?')[0]
  const rel = url === '/' ? 'index.html' : url.replace(/^\/+/, '')
  const file = join(DIST, rel)
  try {
    const s = await stat(file)
    if (!s.isFile()) throw new Error('dir')
    res.writeHead(200, { 'content-type': MIME[extname(file)] || 'application/octet-stream' })
    res.end(await readFile(file))
  } catch {
    res.writeHead(200, { 'content-type': 'text/html' })
    res.end(await readFile(join(DIST, 'index.html')))
  }
})
await new Promise((r) => server.listen(0, '127.0.0.1', r))
const base = `http://127.0.0.1:${server.address().port}`

const results = []
const ok = (n, d = '') => { results.push({ pass: true, n, d }); console.log(`  ✅ ${n}${d ? ' — ' + d : ''}`) }
const bad = (n, d = '') => { results.push({ pass: false, n, d }); console.log(`  ❌ ${n}${d ? ' — ' + d : ''}`) }

/**
 * API stub：所有 /api/* 一律回 200 空对象。
 * 冒烟只关心「页面骨架能不能渲染」，数据源的真实形态由各自模块的单测覆盖。
 * 不 stub 的话后端不存在，http() 会抛「API 返回了 HTML 页面而非 JSON」，
 * 那是环境噪声，会把真正的渲染失败淹没在控制台错误里。
 */
async function stubApi(page) {
  await page.route('**/api/**', (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: '{}' }),
  )
}

const browser = await chromium.launch()

/** 造一个已登录、已解锁的 page。 */
async function newSession() {
  const ctx = await browser.newContext({ viewport: { width: 360, height: 800 } })
  // 必须在任何脚本执行前写入：auth store 在 state 初始化时就读 localStorage。
  // app_locale 同样要预置——i18n 在模块求值期就 resolveStartupLocale()，
  // 晚一步写就会先按设备语言（en-US）起一次再切换，断言会读到英文。
  await ctx.addInitScript(
    ([token, ws]) => {
      localStorage.setItem('pocket_token', token)
      localStorage.setItem('pocket_user', 'ia-smoke-user')
      localStorage.setItem('pocket_workspace_id', ws)
      localStorage.setItem('pocket_auth_method', 'dev-bypass')
      localStorage.setItem('app_locale', 'zh-CN')
    },
    [TOKEN, 'ws_ia_smoke'],
  )
  const page = await ctx.newPage()
  const errs = []
  // 没有后端，WS 握手必然失败；那是环境噪声不是渲染缺陷。
  const NOISE = ['HTML 页面而非 JSON', 'WebSocket', 'Failed to load resource']
  page.on('console', (m) => {
    if (m.type() === 'error' && !NOISE.some((n) => m.text().includes(n))) errs.push(m.text())
  })
  page.on('pageerror', (e) => {
    if (!NOISE.some((n) => String(e).includes(n))) errs.push(String(e))
  })
  await stubApi(page)
  return { page, ctx, errs }
}

/** 首次进入受保护路由会落到 /login?unlock=1；这里完成主密码解锁。 */
async function ensureUnlocked(page, target) {
  await page.goto(`${base}/#${target}`, { waitUntil: 'domcontentloaded' })
  await page.waitForTimeout(1500)
  if (!page.url().includes('/login')) return true
  const input = await page.$('input[placeholder="输入主密码解锁"], input[placeholder*="主密码"]')
  if (!input) return false
  await input.fill(MASTER_PW)
  const btn = await page.$('button:has-text("解锁")')
  if (!btn) return false
  await btn.click()
  // 解锁成功后应被送回 returnTo
  try {
    await page.waitForURL((u) => !u.hash.includes('/login'), { timeout: 20_000 })
  } catch { return false }
  await page.waitForTimeout(1500)
  return true
}

async function goto(page, target) {
  if (await ensureUnlocked(page, target)) return
  // 已解锁过：直接跳
  await page.evaluate((h) => { location.hash = h }, target)
  await page.waitForTimeout(1200)
}

/** 界面里出现 "a.b.c" 形式的文本节点 = i18n 没解析到。 */
async function findRawKeys(page) {
  return page.evaluate(() => {
    const hits = []
    const re = /^[a-z][a-zA-Z0-9]*(\.[a-zA-Z0-9_-]+){1,3}$/
    const walk = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT)
    for (let n = walk.nextNode(); n; n = walk.nextNode()) {
      const t = (n.textContent || '').trim()
      if (re.test(t)) hits.push(t)
    }
    return [...new Set(hits)]
  })
}

/**
 * 扫出渲染成字面量的 i18n key。
 *
 * ⚠️ 这条断言的强度取决于**行列表是否真的渲染出来**。
 * 全新空库下 <ul> 里一行都没有，模板表达式根本没求值，于是「没有字面量 key」
 * 是**空断言**——2026-10-03 审计正是用它当证据、实际漏掉了
 * NotesHubView 的 `t(\`notesHub.filter.${row.filterKey}\`)` 渲染成
 * "notesHub.filter.note" 的缺陷。所以这里把行数一起报出来，
 * 让「没查到」和「根本没渲染」在输出上可区分。
 *
 * 空库场景下的该类缺陷现在由 check:i18n-keys.mjs 的「模板 key 前缀 +
 * 片段组合」规则静态拦截（见该文件 TPL_KEY / KEY_PROP 注释），
 * 不再依赖运行时冒烟。
 */
const uncovered = []
async function checkNoRawKeys(page, where) {
  const raw = await findRawKeys(page)
  const rowCount = await page.$$eval('[data-testid$="-hub-list"] > li', (els) => els.length).catch(() => 0)
  if (raw.length) { bad(`${where}：无字面量 i18n key`, raw.join(', ')); return }
  if (rowCount === 0) {
    // 明确标成「未覆盖」而不是 pass。web fallback 的库是内存态（initWebStore
    // 走 DOM 里的 jeepSqliteElement），外部无法预置数据，所以冒烟跑到的是空库。
    uncovered.push(`${where}：行列表 0 行，字面量 key 维度未覆盖`)
    console.log(`  ⚠️  ${where}：行列表 0 行 —— 「无字面量 key」本轮**未覆盖**（非通过）`)
    return
  }
  ok(`${where}：无字面量 i18n key`, `${rowCount} 行已渲染`)
}

// ---------- 1. BottomNav ----------
console.log('\n【1】BottomNav 四个一级 tab')
{
  const { page, ctx, errs } = await newSession()
  await goto(page, '/ai')
  const tabs = await page.$$eval('nav.bottom-nav a.nav-item', (els) =>
    els.map((e) => ({ label: e.querySelector('.label')?.textContent?.trim(), href: e.getAttribute('href') })),
  )
  tabs.length === 4 ? ok('tab 数量为 4') : bad('tab 数量为 4', `实际 ${tabs.length}`)
  for (const e of [
    { label: '首页', href: '#/ai' },
    { label: '笔记', href: '#/notes' },
    { label: '消息', href: '#/messages' },
    { label: '更多', href: '#/more' },
  ]) {
    const got = tabs.find((t) => t.label === e.label)
    if (!got) bad(`tab「${e.label}」存在`, '缺失')
    else if (got.href !== e.href) bad(`tab「${e.label}」→ ${e.href}`, `实际 ${got.href}`)
    else ok(`tab「${e.label}」→ ${e.href}`)
  }
  errs.length ? bad('首页无控制台错误', errs.slice(0, 2).join(' | ')) : ok('首页无控制台错误')
  await ctx.close()
}

// ---------- 2. /notes ----------
console.log('\n【2】/notes 笔记 Hub（笔记 + 会议纪要 + PKM）')
{
  const { page, ctx, errs } = await newSession()
  await goto(page, '/notes')
  const inNotes = page.url().includes('#/notes')
  inNotes ? ok('解锁后回到 /notes') : bad('解锁后回到 /notes', page.url())
  ;(await page.$('.notes-hub')) ? ok('.notes-hub 根节点渲染') : bad('.notes-hub 根节点渲染', '未找到')
  const chips = await page.$$eval('[data-testid^="src-chip-"]', (els) => els.map((e) => e.textContent.trim()))
  chips.length === 4 ? ok('4 个来源 chips 渲染', chips.join(' / ')) : bad('4 个来源 chips 渲染', `实际 ${chips.length}：${chips.join(' / ')}`)
  await checkNoRawKeys(page, '/notes')
  const meet = await page.$('[data-testid="src-chip-meeting"]')
  if (meet) {
    await meet.click()
    await page.waitForTimeout(300)
    // 点击会触发 chips 列表重渲染，旧 ElementHandle 已脱离文档 —— 必须重新查询。
    const pressed = await page.getAttribute('[data-testid="src-chip-meeting"]', 'aria-pressed')
    pressed === 'true' ? ok('chips 可切换（aria-pressed=true）') : bad('chips 可切换', `aria-pressed=${pressed}`)
  } else bad('会议纪要 chip 存在')
  errs.length ? bad('/notes 无控制台错误', errs.slice(0, 2).join(' | ')) : ok('/notes 无控制台错误')
  await ctx.close()
}

// ---------- 3. /messages ----------
console.log('\n【3】/messages 消息 Hub（邮件 + 订阅 + 任务）')
{
  const { page, ctx, errs } = await newSession()
  await goto(page, '/messages')
  const inMsg = page.url().includes('#/messages')
  inMsg ? ok('解锁后回到 /messages') : bad('解锁后回到 /messages', page.url())
  ;(await page.$('.msg-hub')) ? ok('.msg-hub 根节点渲染') : bad('.msg-hub 根节点渲染', '未找到')
  const chips = await page.$$eval('[data-testid^="src-chip-"]', (els) => els.map((e) => e.textContent.trim()))
  chips.length === 4 ? ok('4 个来源 chips 渲染', chips.join(' / ')) : bad('4 个来源 chips 渲染', `实际 ${chips.length}：${chips.join(' / ')}`)
  await checkNoRawKeys(page, '/messages')
  const rss = await page.$('[data-testid="src-chip-rss"]')
  if (rss) { await rss.click(); await page.waitForTimeout(300) }
  rss ? ok('订阅 chips 存在且可点') : bad('订阅 chips 存在')
  errs.length ? bad('/messages 无控制台错误', errs.slice(0, 2).join(' | ')) : ok('/messages 无控制台错误')
  await ctx.close()
}

// ---------- 4. /more ----------
console.log('\n【4】/more 更多宫格（学习已进、RSS 已移出）')
{
  const { page, ctx, errs } = await newSession()
  await goto(page, '/more')
  const cells = await page.$$eval('.grid-cell', (els) => els.map((e) => e.textContent.trim()))
  cells.some((c) => c.includes('学习')) ? ok('「学习」已在宫格内') : bad('「学习」已在宫格内', cells.join(' / '))
  cells.some((c) => c.includes('订阅')) ? bad('RSS 已从宫格移出', '仍能找到「订阅」') : ok('RSS 已从宫格移出（收敛到消息 tab）')
  await checkNoRawKeys(page, '/more')
  errs.length ? bad('/more 无控制台错误', errs.slice(0, 2).join(' | ')) : ok('/more 无控制台错误')
  await ctx.close()
}

// ---------- 5. 下沉页深链仍可达 ----------
console.log('\n【5】下沉页深链仍可达（/study、/meetings、/notes/voice）')
for (const [hash, sel, name] of [
  ['/study', '.study-hub', '/study 学习'],
  ['/meetings', '.meetings-page', '/meetings 会议'],
]) {
  const { page, ctx } = await newSession()
  await goto(page, hash)
  const landed = page.url().includes(`#${hash}`)
  landed ? ok(`${name} 可直达`) : bad(`${name} 可直达`, page.url())
  ;(await page.$(sel)) ? ok(`${name} 渲染`) : bad(`${name} 渲染`, `未找到 ${sel}`)
  await ctx.close()
}

await browser.close()
server.close()

const failed = results.filter((r) => !r.pass)
console.log(`\n【IA 冒烟】${results.length - failed.length} / ${results.length} 通过`)
if (uncovered.length) {
  console.log('\n⚠️  以下维度本轮未覆盖（不是通过）：')
  for (const u of uncovered) console.log(`   - ${u}`)
  console.log('   空库场景下这类「行内模板 key」缺陷由 check:i18n-keys.mjs 的')
  console.log('   模板前缀 + 片段组合规则静态拦截，不依赖运行时冒烟。')
}
if (failed.length) {
  console.log('失败项：')
  for (const f of failed) console.log(`  - ${f.n}${f.d ? ' — ' + f.d : ''}`)
  process.exit(1)
}
console.log('✅ 全部通过')
