import http from 'http';
import httpProxy from 'http-proxy';

const proxy = httpProxy.createProxyServer({
  target: 'http://14.103.169.56:8088',
  changeOrigin: true,
  ws: true,
  timeout: 30000
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
  
  proxy.web(req, res, (err) => {
    console.error('代理错误:', err.message);
    if (!res.headersSent) {
      res.writeHead(502, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({ error: '代理服务器错误', message: err.message }));
    }
  });
});

// WebSocket 支持（带错误处理）
server.on('upgrade', (req, socket, head) => {
  console.log(`${new Date().toISOString()} - WebSocket upgrade ${req.url}`);
  
  socket.on('error', (err) => {
    console.error('Socket 错误:', err.message);
  });
  
  proxy.ws(req, socket, head, (err) => {
    console.error('WebSocket 代理错误:', err.message);
    socket.destroy();
  });
});

// 全局错误处理
proxy.on('error', (err, req, res) => {
  console.error('代理错误:', err.message);
  if (res && res.writeHead && !res.headersSent) {
    res.writeHead(500, { 'Content-Type': 'text/plain' });
    res.end('代理服务器错误: ' + err.message);
  }
});

// 防止进程崩溃
process.on('uncaughtException', (err) => {
  console.error('未捕获的异常:', err.message);
});

process.on('unhandledRejection', (reason, promise) => {
  console.error('未处理的 Promise 拒绝:', reason);
});

const PORT = 8088;
const HOST = '0.0.0.0';

server.listen(PORT, HOST, () => {
  console.log(`✅ 代理服务器运行在:`);
  console.log(`   http://192.168.31.41:${PORT}`);
  console.log(`   转发到: http://14.103.169.56:8088`);
  console.log(`\n📱 在手机 App 中使用: http://192.168.31.41:${PORT}`);
  console.log(`\n等待连接...`);
});
