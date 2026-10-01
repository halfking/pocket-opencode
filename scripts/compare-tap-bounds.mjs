// compare-tap-bounds.mjs — 对账：Maestro 打日志里「实际使用的 bounds」
// 与同一时刻 CDP 采样到的「真实 bounds」是否一致。
//
// 用法：node scripts/compare-tap-bounds.mjs <maestro.log> <bounds.csv>
//
// 为什么必须对账而不是推理（2026-10-01 13:50）：
//   我一直说「Maestro 用的是数据到达前的旧坐标」，但从没验证过。
//   两种可能对处置的影响完全相反：
//     · bounds 一致 → 坐标没问题，问题是「合成点击没被 WebView 当 click」
//     · bounds 不一致 → 陈旧坐标，改 flow 的等待策略才有用
//   所以这里把两边按时间戳对齐，把结论钉死或证伪。
import { readFileSync } from 'node:fs'

const logPath = process.argv[2]
const csvPath = process.argv[3]
if (!logPath || !csvPath) { console.error('用法: node scripts/compare-tap-bounds.mjs <maestro.log> <bounds.csv>'); process.exit(2) }

// ---- 1. 从 maestro.log 里取 tap 行 ----
// 形如：
//   13:51:02.345 [ INFO] ... Tapping on element:  UiElement(treeNode=TreeNode(attributes={text=+ 新任务,
//     ... bounds=[290,202][418,252], ...), bounds=Bounds(x=290, y=202, width=128, height=50))
const log = readFileSync(logPath, 'utf8').split(/\r?\n/)
const taps = []
for (const line of log) {
  if (!/Tapping on element/.test(line)) continue
  const ts = line.match(/^(\d{2}):(\d{2}):(\d{2})\.(\d{3})/)
  const m = line.match(/text=([^,}]*)/)
  const b = line.match(/bounds=\[(-?\d+),(-?\d+)\]\[(-?\d+),(-?\d+)\]/)
  if (!ts || !b) continue
  const hh = Number(ts[1]), mm = Number(ts[2]), ss = Number(ts[3]), ms = Number(ts[4])
  taps.push({
    wall: line.slice(0, 12),
    label: (m ? m[1] : '').trim().slice(0, 30),
    rect: [Number(b[1]), Number(b[2]), Number(b[3]), Number(b[4])],
    // ⚠️ 不要减 8 小时。maestro.log 里的 HH:MM:SS.mmm 是**设备本地时钟**，
    //    而本机也在 UTC+8，所以 `new Date(y,m,d,hh,mm,ss,ms)` 构造出来的
    //    epoch 已经就是对的。我第一次多减了 8h，导致窗口全部落空、
    //    对账脚本输出「该时刻没有采样」——那是**判据自己的 bug**，
    //    不是「采样断流」。判据坏了必须先修判据，不能拿它下结论。
    epochGuess: new Date(2026, 9, 1, hh, mm, ss, ms).getTime(),
  })
}

// ---- 2. 读采样 CSV ----
const csv = readFileSync(csvPath, 'utf8').split(/\r?\n/).filter(Boolean)
const head = csv.shift()
if (!head || !head.startsWith('epoch_ms')) { console.error('CSV 头不对：' + head); process.exit(2) }
const samples = csv.map((l) => {
  const p = l.split(',')
  return { epoch: Number(p[0]), wall: p[1], hash: p[2], y: p[3] === '' ? null : Number(p[3]), rect: (p[4] || '').replace(/"/g, ''), cards: Number(p[5]), sheet: Number(p[6]) }
}).filter((s) => Number.isFinite(s.epoch))

console.log(`采样 ${samples.length} 条，tap 事件 ${taps.length} 个`)
if (!taps.length) { console.log('日志里没有 Tapping on element —— 这次 run 没走到 tap，先看 flow 为什么挂在前面'); process.exit(0) }
if (samples.length < 20) {
  console.log('⚠️ 有效采样太少，判据自身不可靠，不要据此下结论')
}

const eq = (a, b) => a.length === b.length && a.every((v, i) => v === b[i])

for (const t of taps) {
  // 取 tap 时刻前后各 1.5s 的采样
  const win = samples.filter((s) => Math.abs(s.epoch - t.epochGuess) <= 1500)
  console.log(`\n=== tap ${t.wall}  ${t.label}  Maestro bounds=${JSON.stringify(t.rect)} ===`)
  if (!win.length) { console.log('  该时刻没有采样（采样断流）⇒ 这条不能用'); continue }
  for (const s of win) {
    const sr = s.rect ? s.rect.split(' ').map(Number) : null
    const same = sr ? eq(sr, t.rect) : false
    console.log(`  ${s.wall} hash=${s.hash} y=${s.y} rect="${s.rect}" cards=${s.cards} sheet=${s.sheet}  ${same ? '✅与Maestro一致' : '❌不一致'}`)
  }
  const uniq = [...new Set(win.map((s) => s.rect))]
  const moved = uniq.length > 1
  console.log(`  ⇒ 窗口内按钮位置出现过 ${uniq.length} 种取值：${moved ? '期间发生过移动' : '期间未移动'}`)
}
