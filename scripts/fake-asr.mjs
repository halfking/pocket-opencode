#!/usr/bin/env node
/**
 * 假 ASR 上游（黑盒验证专用，不入库依赖）。
 *
 * 存在的理由：真正可用的外部 ASR key 在本机不可达（api.openai.com i/o timeout），
 * 网关侧 2026-10-01 实测也没有任何 ASR 上游。没有它就无法验证
 * 「/api/stt/transcribe-full 与 transcribe-incremental 的真实 HTTP 链路」——
 * 而这正是本轮改动风险最大的部分（鉴权中间件、请求体上限、JSON 解码、
 * 会话状态机，单测的 httptest 覆盖不到这些）。
 *
 * 它**不能**证明识别质量，只证明链路正确。识别质量由设置页「录 3 秒试转」验证。
 *
 * 用法：node fake-asr.mjs <port>
 * 响应：按调用顺序返回预设文本，OpenAI 兼容 {"text": "..."}。
 */
import { createServer } from 'node:http'

const port = Number(process.argv[2] || 18102)

// 文本里带序号，便于验证「按序聚合」而不是「全返回第一段」。
const texts = [
  '第一段内容', '第二段内容', '第三段内容',
  '第四段内容', '第五段内容', '第六段内容', '第七段内容', '第八段内容',
]

let calls = 0
const seenSeconds = []

createServer((req, res) => {
  if (req.method === 'GET' && req.url === '/__calls') {
    res.writeHead(200, { 'Content-Type': 'application/json' })
    res.end(JSON.stringify({ calls, seenSeconds }))
    return
  }
  // 消费请求体（multipart 可能有数 MB，不消费会让连接挂住）
  const chunks = []
  req.on('data', (c) => chunks.push(c))
  req.on('end', () => {
    calls += 1
    const text = texts[Math.min(calls - 1, texts.length - 1)]
    // 必须显式带 charset=utf-8：PowerShell 5.1 的 Invoke-RestMethod 在
    // 没有 charset 时按本地 ANSI 解码，中文会变成乱码，断言随之失败——
    // 那是**客户端解码问题**，但会让验证脚本给出误导性的结论。
    // 一个正经的 JSON API 就应该声明 charset，这里照做。
    res.writeHead(200, { 'Content-Type': 'application/json; charset=utf-8' })
    res.end(JSON.stringify({ text }))
  })
}).listen(port, '127.0.0.1', () => {
  console.log(`fake-asr listening on 127.0.0.1:${port}`)
})
