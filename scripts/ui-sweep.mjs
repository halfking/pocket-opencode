#!/usr/bin/env node
/**
 * 逐页 UI 体检（真机自动化）。
 *
 * 用户验收项：「检查 UI 的展示是否正常，有没有字体及显示错位的问题」。
 * 逐页截图靠人眼看，48 个页面既慢又容易漏；逐页开一次 CDP 连接更慢
 * （每页约 60s，光 adb forward + WebSocket 握手就占大半）。这里改成
 * **把整批路由的遍历放进页面上下文里跑完**，Node 侧只建一次连接，
 * 一批 8 页约 25s，44 页约 3 分钟。
 *
 * 检测项（在页面里量 DOM）：
 *   1. 横向溢出      scrollWidth > clientWidth
 *   2. 文本截断      同上且 text-overflow: ellipsis
 *   3. 元素越界      getBoundingClientRect 超出视口
 *   4. 字体族分布    统计实际 font-family，核对是否都走 token
 *
 * 误报说明：横向滚动容器（overflow-x: auto/scroll）是设计使然，
 * 报告里带 overflowX 字段，离线分析时按需过滤，不要直接当 bug。
 *
 * 用法：node scripts/ui-sweep.mjs [--shots]
 */
import { spawnSync } from 'node:child_process'
import { writeFileSync, mkdirSync, existsSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..')
const SHOT_DIR = join(ROOT, 'logs', 'audit', 'sweep')
const WANT_SHOTS = process.argv.includes('--shots')
const SETTLE_MS = 2600
const BATCH = 8

const PAGES = [
  '/ai', '/ai-chat', '/study', '/meetings', '/more',
  '/notes', '/notes/new', '/pkm/today', '/email', '/email/summary', '/email/invoices',
  '/email/cleanup', '/email/settings', '/email/accounts', '/email/accounts/new',
  '/contacts', '/vault', '/rss', '/rss/add', '/finance', '/flashcards', '/flashcards/browser',
  '/flashcards/io', '/flashcards/new', '/flashcards/stats', '/tasks', '/sessions', '/instances',
  '/agents', '/agents/new', '/marketplace/agents', '/marketplace/skills', '/marketplace/workbuddies',
  '/local-agent', '/cost', '/gateway', '/settings', '/settings/llm-gateway',
  '/settings/permissions', '/settings/scheduled-tasks', '/settings/scheduled-tasks/new',
  '/notifications', '/servers',
]

/** 在页面上下文里跑的批量采集（一次调用处理一整批路由） */
const BATCH_PROBE = `
(async (paths, settle) => {
  const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
  // Vue transition 的状态类：出现即代表入场/离场动画进行中，此时量布局必然失真
  const TRANSITION_SEL = '[class*="-enter-active"],[class*="-enter-from"],[class*="-enter-to"],[class*="-leave-active"],[class*="-leave-from"],[class*="-leave-to"],[class*="nav-push"]';
  // 固定 sleep 会在路由过渡未结束时采样（实测 /settings/permissions 采到的
  // 还是上一页 #/agents 的入场态，报出 667px 的假溢出）。改为轮询：等 hash 到位
  // 且过渡类消失才算采完，超时才降级为固定等待。
  const waitQuiet = async (p) => {
    const deadline = Date.now() + settle;
    while (Date.now() < deadline) {
      await sleep(120);
      if (location.hash === '#' + p && !document.querySelector(TRANSITION_SEL)) return;
    }
    await sleep(200);
  };
  const collect = () => {
    const vw = document.documentElement.clientWidth;
    const out = { overflow: [], truncated: [], outOfView: [], fontFamilies: [] };
    const seen = new Set();
    const fontSet = new Set();
    const label = (e) => {
      const t = (e.textContent || '').replace(/\\s+/g, ' ').trim().slice(0, 26);
      const cn = (typeof e.className === 'string' && e.className)
        ? '.' + e.className.split(/\\s+/).filter(Boolean).slice(0, 2).join('.') : '';
      return e.tagName.toLowerCase() + cn + (t ? ' « ' + t + ' »' : '');
    };
    // 处于路由过渡中的元素不参与判定：入场视图正被水平位移（真机实测
    // /instances 曾报 17 处，全部带 nav-push-enter-active，内容宽 672px vs 视口 354px）。
    const inRouteTransition = (e) => !!(e.closest && e.closest(TRANSITION_SEL));
    const isScroller = (el) => {
      const px = getComputedStyle(el).overflowX;
      return px === 'auto' || px === 'scroll';
    };
    const scrollerState = new Map();
    const insideScroller = (e) => {
      const path = [];
      let p = e;
      let hit = false;
      while (p && p !== document.body) {
        const cached = scrollerState.get(p);
        if (cached !== undefined) { hit = cached; break; }
        path.push(p);
        if (isScroller(p)) { hit = true; break; }
        p = p.parentElement;
      }
      for (const n of path) scrollerState.set(n, hit);
      return hit;
    };
    for (const e of document.querySelectorAll('body *')) {
      const cs = getComputedStyle(e);
      if (cs.display === 'none' || cs.visibility === 'hidden' || +cs.opacity === 0) continue;
      // 仅供读屏的视觉隐藏文本：设计上就是 1px 宽，量它必然「溢出」，不是 bug
      if (e.classList && (e.classList.contains('sr-only') || e.classList.contains('visually-hidden'))) continue;
      // 横向滚动容器（chips 行等）的子元素本来就允许超出视口，不算页面溢出。
      // 「向上找祖先」用记忆化摊成 O(节点数)：getComputedStyle 强制样式/布局计算，
      // 逐节点逐祖先重复调用会把整批页面采集拖过 CDP 的 45s 预算。
      if (insideScroller(e)) continue;
      // 路由过渡动画进行中：入场视图被水平位移，量出来必然"越界"。
      // Vue transition 的状态类（*-enter-active / *-leave-to 等）是明确信号，直接跳过。
      if (inRouteTransition(e)) continue;
      const r = e.getBoundingClientRect();
      if (r.width === 0 && r.height === 0) continue;
      if (e.childElementCount === 0 && (e.textContent || '').trim()) fontSet.add(cs.fontFamily);
      // SVG 元素不参与「scrollWidth > clientWidth」判据：SVG 有自己的坐标系，
      // 图表 <text> 在布局未完成时会出现 clientW=2 / scrollW=4 这类退化盒，
      // 属于瞬时渲染态而非布局溢出（实测 /flashcards/stats 走查中偶现、直接访问不复现）。
      // 但它的 getBoundingClientRect 越界判断仍然有效，下面照常检查。
      const isSvg = e.namespaceURI === 'http://www.w3.org/2000/svg' || !!(e.ownerSVGElement || e.closest('svg'));
      if (!isSvg && e.scrollWidth > e.clientWidth + 1 && e.clientWidth > 0) {
        const k = e.tagName + ':' + cs.overflowX + ':' + String(e.className || '').slice(0, 22);
        if (!seen.has(k)) {
          seen.add(k);
          const rec = { el: label(e), scrollW: e.scrollWidth, clientW: e.clientWidth, overflowX: cs.overflowX };
          (cs.textOverflow === 'ellipsis' ? out.truncated : out.overflow).push(rec);
        }
      }
      if (r.width > 0 && (r.right > vw + 2 || r.left < -2)) {
        const k2 = 'oob:' + e.tagName + ':' + String(e.className || '').slice(0, 22);
        if (!seen.has(k2)) {
          seen.add(k2);
          out.outOfView.push({ el: label(e), left: Math.round(r.left), right: Math.round(r.right), vw });
        }
      }
    }
    out.fontFamilies = [...fontSet];
    out._vw = vw;
    return out;
  };
  const results = [];
  for (const p of paths) {
    location.hash = '#' + p;
    await waitQuiet(p);
    results.push(Object.assign({ path: p, hash: location.hash }, collect()));
  }
  return JSON.stringify(results);
})(__PATHS__, ${SETTLE_MS})
`

function evalInPage(expr) {
  const r = spawnSync(process.execPath, [join(ROOT, 'scripts', 'cdp.mjs'), 'eval', expr], {
    encoding: 'utf8', timeout: 180000, maxBuffer: 64 * 1024 * 1024,
  })
  if (r.status !== 0) throw new Error((r.stderr || r.stdout || 'cdp failed').trim().split('\n').pop())
  return r.stdout.trim()
}

function shot(name) {
  if (!existsSync(SHOT_DIR)) mkdirSync(SHOT_DIR, { recursive: true })
  const r = spawnSync(process.execPath, [join(ROOT, 'scripts', 'device.mjs'), 'shot', `sweep/${name}`], {
    encoding: 'utf8', timeout: 180000,
  })
  return (r.stdout || '').trim().split('\n').pop()
}

const report = []
for (let i = 0; i < PAGES.length; i += BATCH) {
  const batch = PAGES.slice(i, i + BATCH)
  let rows
  try {
    const expr = BATCH_PROBE.replace('__PATHS__', JSON.stringify(batch))
    rows = JSON.parse(evalInPage(expr))
  } catch (e) {
    console.log(`批次 ${i / BATCH + 1} 失败：${e.message}`)
    for (const p of batch) report.push({ path: p, verdict: 'ERROR: ' + e.message })
    continue
  }
  for (const row of rows) {
    // 横向滚动容器是设计使然，排除后再判定
    const realOverflow = (row.overflow || []).filter((o) => !/auto|scroll/.test(o.overflowX || ''))
    const rec = {
      path: row.path,
      hash: row.hash,
      viewport: row._vw,
      overflow: realOverflow,
      scrollableIgnored: (row.overflow || []).length - realOverflow.length,
      truncated: row.truncated || [],
      outOfView: row.outOfView || [],
      fontFamilies: row.fontFamilies || [],
    }
    rec.verdict = rec.overflow.length + rec.outOfView.length === 0 ? 'ok'
      : `${rec.overflow.length + rec.outOfView.length} 处需关注`
    if (WANT_SHOTS) rec.shot = shot(row.path.replace(/^\//, '').replace(/\//g, '-'))
    report.push(rec)
    console.log(
      `${row.path.padEnd(34)} ${rec.verdict.padEnd(16)}` +
      `overflow=${rec.overflow.length} scroll(忽略)=${rec.scrollableIgnored} trunc=${rec.truncated.length} oob=${rec.outOfView.length}` +
      (rec.shot ? `  ${String(rec.shot).split(/[\\/]/).pop()}` : ''),
    )
  }
}

const out = join(ROOT, 'logs', 'audit', 'ui-sweep-report.json')
writeFileSync(out, JSON.stringify(report, null, 2))
const bad = report.filter((r) => r.verdict !== 'ok')
console.log(`\n共 ${report.length} 页，需关注 ${bad.length} 页。报告：${out}`)
for (const b of bad) console.log(`  ! ${b.path} — ${b.verdict}`)
