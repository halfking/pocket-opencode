#!/usr/bin/env node
/**
 * 真机 → LLM 网关 的 HTTP 反向代理（带 CORS 注入）。
 *
 * 为什么要这个：
 *   1. 真机路由表只有 `192.168.31.0/24 dev wlan0` 一条 on-link 路由，没有默认路由，
 *      同网段的 PC(192.168.31.20) 与网关(192.168.31.34) 都不可达（疑似 AP 客户端隔离），
 *      唯一稳定通道是 `adb reverse`；而 reverse 只能转到本机端口，所以需要一层转发。
 *   2. `adb reverse` 只能解决可达性，**解决不了跨域**。真机 WebView 的 origin 是
 *      `http://localhost`，而网关不返回 `Access-Control-Allow-Origin`，
 *      浏览器会直接判定 fetch 失败（表现为 "Failed to fetch"，与网络不通长得一样）。
 *      之前用裸 TCP 透传时，连接明明到达了上游，页面却仍报 Failed to fetch —— 就是这一层。
 *
 * 因此这里做成 HTTP 层代理：自己控制响应头，注入 CORS；正文用 pipe 直通，
 * 不做缓冲，SSE 流式输出不会被攒包。
 *
 * 用法：
 *   node scripts/gateway-tunnel.mjs           # 常驻
 *   node scripts/gateway-tunnel.mjs --check   # 自检后退出
 */
import http from 'node:http'
import { execFileSync } from 'node:child_process'

const UPSTREAM = process.env.GW_UPSTREAM || '192.168.31.34:8080'
const LISTEN = Number(process.env.GW_TUNNEL_PORT || 18080)
const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '4c308e2e'

const upstreamUrl = new URL(`http://${UPSTREAM}`)
const stats = { in: 0, preflight: 0, proxied: 0, errors: 0 }

const server = http.createServer((req, res) => {
  stats.in++
  const origin = req.headers.origin

  // 预检：直接本地应答，不打扰网关
  if (req.method === 'OPTIONS') {
    stats.preflight++
    res.writeHead(204, corsHeaders(origin))
    res.end()
    return
  }

  const headers = { ...req.headers, host: upstreamUrl.host }
  delete headers['accept-encoding'] // 避免上游压缩后长度对不上

  const proxyReq = http.request(
    {
      hostname: upstreamUrl.hostname,
      port: upstreamUrl.port || 80,
      path: req.url,
      method: req.method,
      headers,
    },
    (proxyRes) => {
      stats.proxied++
      const out = { ...corsHeaders(origin), ...proxyRes.headers }
      // 自己算长度，避免上游 chunked 与本地 framing 打架
      delete out['content-length']
      delete out['transfer-encoding']
      res.writeHead(proxyRes.statusCode || 502, out)
      proxyRes.pipe(res) // 直通，SSE 不缓冲
    },
  )
  proxyReq.on('error', (e) => {
    stats.errors++
    console.error(`[proxy] ${e.message}`)
    if (!res.headersSent) res.writeHead(502, { 'content-type': 'application/json', ...corsHeaders(origin) })
    res.end(JSON.stringify({ error: `gateway unreachable: ${e.message}` }))
  })
  req.pipe(proxyReq)
})

function corsHeaders(origin) {
  return {
    'access-control-allow-origin': origin || '*',
    'access-control-allow-credentials': 'true',
    'access-control-allow-methods': 'GET,POST,PUT,PATCH,DELETE,OPTIONS',
    'access-control-allow-headers': req_all_headers(),
    'access-control-expose-headers': '*',
  }
}
function req_all_headers() {
  return 'authorization,content-type,accept,cache-control,x-requested-with'
}

server.listen(LISTEN, '127.0.0.1', () => {
  console.log(`HTTP 代理已启动：127.0.0.1:${LISTEN} → ${UPSTREAM}（注入 CORS，SSE 直通）`)
  try {
    execFileSync(ADB, ['-s', SERIAL, 'reverse', `tcp:${LISTEN}`, `tcp:${LISTEN}`], { encoding: 'utf8' })
    console.log(`adb reverse：设备 127.0.0.1:${LISTEN} → PC 127.0.0.1:${LISTEN}`)
  } catch (e) {
    console.error('adb reverse 设置失败：', e.message)
  }
  if (process.argv.includes('--check')) {
    setTimeout(async () => {
      const r = await fetch(`http://127.0.0.1:${LISTEN}/healthz`, { headers: { origin: 'http://localhost' } })
        .then(async (x) => `status ${x.status} ACAO=${x.headers.get('access-control-allow-origin')} ${(await x.text()).slice(0, 100)}`)
        .catch((e) => `ERR ${e.message}`)
      console.log('自检 /healthz:', r)
      console.log('统计:', JSON.stringify(stats))
      server.close()
      process.exit(0)
    }, 800)
  }
})

setInterval(() => {
  if (stats.in) console.log(`[stats] ${JSON.stringify(stats)}`)
}, 60_000).unref?.()

process.on('SIGINT', () => { console.log('\n关闭代理'); server.close(); process.exit(0) })
