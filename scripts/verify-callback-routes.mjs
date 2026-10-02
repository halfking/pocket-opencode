// verify-callback-routes.mjs — 静态卡口：**凡是把未知路径回落到前端(SPA)的 vhost，
// 都必须有 /callback/ 反代规则。**
//
// 为什么要有（2026-10-03 实测踩出来的）：
//   飞书 / 企业微信事件回调是 POST 到 /callback/feishu 与 /callback/weixin 的。
//   后端 server.go:682/689 把这两条路由挂在 requireAuth 之外（外部平台带的是协议签名，
//   不是本站 JWT），所以**后端是对的**；问题全在边缘 nginx：
//   此前全仓**一个 /callback/ 规则都没有**，请求落进 `location /`，被前端当 SPA 返回
//   index.html。实测 GET https://pocket.itestu.cn/callback/feishu 返回 200 + 一段 HTML，
//   而同一个路由在应用里是活的（POST 空体 -> 200 {"code":0,"msg":"ok"}）。
//
//   这个缺陷有两层难受：
//   ① 静默 —— nginx 不报错，TLS 正常，页面能开，只有事件投递静默失败；
//   ② 报错形态误导 —— 平台侧看到的是 HTML/非预期内容，很容易被结论成
//      「飞书那边没配好」，于是去反复重配平台，而真正的原因是边缘缺一条 location。
//
// 判据问的是**不变量**（形状），不是「某个文件里有没有某行」：
//   只要一个 vhost 同时满足
//     (1) 有 location /api/ 且它转到某个上游 A，且
//     (2) location / 转到**另一个**上游 B（= 未知路径回落前端）
//   那它就处在「/callback/ 会被吃掉」的形状里，必须有 location /callback/。
//   这样新加的 vhost 也会被自动纳入，不依赖我记哪几个域名。
//
// 第二条断言（上游必须与 /api/ 相同）不是多余的：复制粘贴时把 proxy_pass 写成
// 前端上游，nginx 同样不会报错，只是继续返回 HTML。形状对了、指向错了更难查。
//
// 用法：
//   node scripts/verify-callback-routes.mjs [edgeDir]
// 可传 argv[2] 覆盖被扫目录 —— 负控就是拿一份改坏的副本喂进来，
// 不必去动真文件（真文件是被 apply-edge-conf.sh 直接上线的东西）。
import { readFileSync, readdirSync, existsSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

const DEFAULT_DIR = fileURLToPath(new URL('../deploy/edge', import.meta.url))
const EDGE = process.argv[2] ?? DEFAULT_DIR

if (!existsSync(EDGE)) {
  console.error('[FAIL] 边缘配置目录不存在：' + EDGE)
  process.exit(3)
}

/** 去掉 nginx 注释：# 到行尾。注释里出现 location /api/ 之类会污染形状判定。 */
function stripComments(src) {
  return src
    .split(/\r?\n/)
    .map((line) => {
      let cut = line.length
      for (let i = 0; i < line.length; i++) {
        const ch = line[i]
        // 引号内的 # 不是注释（证书路径之类极少出现，这里保守处理）
        if (ch === '"' || ch === "'") break
        if (ch === '#') { cut = i; break }
      }
      return line.slice(0, cut)
    })
    .join('\n')
}

/** 取某个 location 块里的 proxy_pass 目标。 */
function upstreamOf(src, locationHeader) {
  // location 头之后到该块结束（下一个 location 或文件尾）之间的第一个 proxy_pass
  const start = src.indexOf(locationHeader)
  if (start === -1) return null
  const rest = src.slice(start + locationHeader.length)
  // 该块的范围：到下一个 "location " 或 "}" + 换行为止
  const nextLoc = rest.search(/\n\s*location\s/)
  const block = nextLoc === -1 ? rest : rest.slice(0, nextLoc)
  const m = block.match(/proxy_pass\s+(https?:\/\/[^;\s]+)/)
  return m ? m[1] : null
}

const files = readdirSync(EDGE).filter((f) => f.endsWith('.conf'))
if (!files.length) {
  console.error('[FAIL] ' + EDGE + ' 下没有任何 .conf —— 判据会空转通过，拒绝给结论。')
  process.exit(3)
}

const needing = []
for (const f of files) {
  const src = stripComments(readFileSync(join(EDGE, f), 'utf8'))
  const apiUp = upstreamOf(src, 'location /api/')
  const rootUp = upstreamOf(src, 'location / {')
  if (!apiUp || !rootUp) continue // 纯 API 域 / 纯 ACME 域，不在这个形状里
  if (apiUp === rootUp) continue // 未知路径也回 API，回调本来就通
  const cbUp = upstreamOf(src, 'location /callback/')
  needing.push({ file: f, apiUp, rootUp, cbUp })
}

// ---- 防空跑：判据必须真的扫到了处在该形状里的 vhost ----
if (needing.length < 2) {
  console.error(
    '[FAIL] 只识别出 ' + needing.length + ' 个「未知路径回落前端」的 vhost。\n' +
      '       形状解析多半失明了（多半是 location 头写法变了），别把这当成通过。',
  )
  process.exit(4)
}

console.log('扫描 ' + files.length + ' 个 vhost，其中 ' + needing.length + ' 个把未知路径回落到前端：')
for (const v of needing) {
  console.log(
    '  ' + v.file.padEnd(34) +
      '/api/ -> ' + v.apiUp.padEnd(32) +
      'fallback -> ' + v.rootUp.padEnd(30) +
      '/callback/ -> ' + (v.cbUp ?? '（缺失）'),
  )
}

const missing = needing.filter((v) => !v.cbUp)
const wrongUpstream = needing.filter((v) => v.cbUp && v.cbUp !== v.apiUp)

if (missing.length) {
  console.error('\n[FAIL] 这些 vhost 把 /callback/ 吃进前端兜底了，事件投递必然失败：')
  for (const v of missing) console.error('  ✗ ' + v.file + ' —— 缺 location /callback/')
  console.error('\n  补一条 location /callback/，proxy_pass 指向与 /api/ 相同的后端上游。')
  process.exit(1)
}

if (wrongUpstream.length) {
  console.error('\n[FAIL] 这些 vhost 的 /callback/ 指到了错误的上游（多半是复制粘贴写成了前端）：')
  for (const v of wrongUpstream) {
    console.error('  ✗ ' + v.file + '：/callback/ -> ' + v.cbUp + '，但 /api/ -> ' + v.apiUp)
  }
  process.exit(1)
}

console.log('\n[OK] 所有「未知路径回落前端」的 vhost 都把 /callback/ 转到了与 /api/ 相同的后端上游。')
