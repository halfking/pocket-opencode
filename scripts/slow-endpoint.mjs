#!/usr/bin/env node
/**
 * 审计用慢端点（真机）。
 *
 * 目的：验证两条用户验收项，必须有**可控时长**的在途请求才能测：
 *   1. 请求在切换页面后仍能继续执行（不被组件卸载取消）
 *   2. 后台执行的 API 可以强行终止（AbortController 真的生效）
 *
 * pocketd 自身响应太快，观察不到"在途"窗口；而 34 号网关 ready=false
 * 会在 CORS 阶段就 Failed to fetch，测不到中止。所以起这个专用服务：
 *   /slow?ms=20000   —— 挂起指定毫秒后才响应
 *   /slow-json?ms=…  —— 慢，但返回 JSON（便于走应用的 assertNotHTML）
 *
 * 仅监听内网地址，端口默认 8099。
 * Run: node scripts/slow-endpoint.mjs
 */
import http from 'node:http'

const PORT = Number(process.env.POCKET_SLOW_PORT || 8099)
const HOST = process.env.POCKET_SLOW_HOST || '0.0.0.0'

const server = http.createServer((req, res) => {
  const url = new URL(req.url, `http://${req.headers.host || 'localhost'}`)
  const ms = Math.min(Number(url.searchParams.get('ms') || 20000), 120000)
  const started = Date.now()
  console.log(`[slow] ${req.method} ${url.pathname}?ms=${ms} from ${req.socket.remoteAddress}`)

  const timer = setTimeout(() => {
    if (res.writableEnded) return
    res.writeHead(200, {
      'Content-Type': 'application/json',
      'Access-Control-Allow-Origin': '*',
    })
    res.end(JSON.stringify({ ok: true, waitedMs: Date.now() - started }))
  }, ms)

  // 客户端主动断开时立刻取消定时器，并留下证据
  req.on('aborted', () => {
    clearTimeout(timer)
    console.log(`[slow] 客户端在 ${Date.now() - started}ms 处主动断开（请求已被终止）`)
  })
  res.on('close', () => {
    if (!res.writableEnded) {
      clearTimeout(timer)
      console.log(`[slow] 连接在 ${Date.now() - started}ms 处关闭且未完成（请求已被终止）`)
    }
  })
})

server.preflightHandler = null
server.on('request', (req, res) => {
  if (req.method === 'OPTIONS') {
    res.writeHead(204, {
      'Access-Control-Allow-Origin': '*',
      'Access-Control-Allow-Methods': 'GET,POST,OPTIONS',
      'Access-Control-Allow-Headers': 'Content-Type, Authorization',
      'Access-Control-Max-Age': '600',
    })
    res.end()
  }
})

server.listen(PORT, HOST, () => {
  console.log(`[slow] 慢端点已启动: http://${HOST}:${PORT}/slow?ms=20000`)
})
