// 真机验证用的静态服务：dist/ + /api/* 空壳 + /__seed 播种页
// 挂在 127.0.0.1:8099，由 `adb reverse tcp:8099 tcp:8099` 让手机反向访问。
// 不用它手机打不开 host 的端口；有了它才能在真机上加载这份新构建。
import { createServer } from 'node:http'
import { readFile, stat } from 'node:fs/promises'
import { join, extname } from 'node:path'
const DIST = new URL('./dist', import.meta.url).pathname
const MIME = {'.html':'text/html','.js':'text/javascript','.css':'text/css','.json':'application/json',
  '.woff2':'font/woff2','.svg':'image/svg+xml','.png':'image/png','.wasm':'application/wasm','.ico':'image/x-icon'}

const SEED = `<!doctype html><meta charset="utf-8"><title>seed</title>
<script>
// 播种：与 http://127.0.0.1:8099 同源，localStorage 作用域一致。
// 只写「已认证 + 已设主密码 + 中文」三项，其余交给 App 自己的解锁流程
// （initLobster 走 Web Crypto + sql.js，全在客户端，不需要后端）。
localStorage.setItem('pocket_token', 'device-smoke-token');
localStorage.setItem('pocket_user', 'device-test');
localStorage.setItem('pocket_workspace_id', 'ws_device');
localStorage.setItem('pocket_auth_method', 'dev-bypass');
localStorage.setItem('app_locale', 'zh-CN');
localStorage.setItem('pocket_crypto_cfg', JSON.stringify({
  fieldEncryption: 'disabled', hasMasterPassword: true, passwordHint: null, updatedAt: Date.now()
}));
location.replace('/#' + (location.hash.replace('#','') || '/notes'));
</script>`

createServer(async (req, res) => {
  const u = (req.url || '/').split('?')[0]
  console.log(new Date().toISOString().slice(11,19), req.method, u.slice(0,90))
  if (u === '/__seed') { res.writeHead(200, {'content-type':'text/html; charset=utf-8'}); return res.end(SEED) }
  if (u.startsWith('/api/')) { res.writeHead(200, {'content-type':'application/json'}); return res.end('{}') }
  const rel = u === '/' ? 'index.html' : u.replace(/^\/+/, '')
  const f = join(DIST, rel)
  try {
    const s = await stat(f)
    if (!s.isFile()) throw 0
    res.writeHead(200, {'content-type': MIME[extname(f)] || 'application/octet-stream'})
    res.end(await readFile(f))
  } catch {
    res.writeHead(200, {'content-type':'text/html'})
    res.end(await readFile(join(DIST, 'index.html')))
  }
}).listen(8099, '127.0.0.1', () => console.log('dist+seed on 127.0.0.1:8099'))
