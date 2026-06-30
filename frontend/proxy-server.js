const http = require('http');
const httpProxy = require('http-proxy');

const proxy = httpProxy.createProxyServer({
  target: 'http://14.103.169.56:8088',
  changeOrigin: true
});

const server = http.createServer((req, res) => {
  // 添加 CORS 头
  res.setHeader('Access-Control-Allow-Origin', '*');
  res.setHeader('Access-Control-Allow-Methods', 'GET, POST, PUT, DELETE, OPTIONS');
  res.setHeader('Access-Control-Allow-Headers', 'Content-Type, Authorization');
  
  if (req.method === 'OPTIONS') {
    res.writeHead(200);
    res.end();
    return;
  }
  
  console.log(`${new Date().toISOString()} - ${req.method} ${req.url}`);
  proxy.web(req, res);
});

// WebSocket 支持
server.on('upgrade', (req, socket, head) => {
  console.log(`${new Date().toISOString()} - WebSocket upgrade ${req.url}`);
  proxy.ws(req, socket, head);
});

proxy.on('error', (err, req, res) => {
  console.error('代理错误:', err);
  if (res.writeHead) {
    res.writeHead(500, { 'Content-Type': 'text/plain' });
    res.end('代理服务器错误');
  }
});

const PORT = 8088;
server.listen(PORT, '0.0.0.0', () => {
  console.log(`✅ 代理服务器运行在:`);
  console.log(`   http://192.168.31.41:${PORT}`);
  console.log(`   转发到: http://14.103.169.56:8088`);
  console.log(`\n📱 在手机 App 中使用: http://192.168.31.41:${PORT}`);
});
