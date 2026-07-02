# Pocket + 飞书回调部署指引（184 重置后）

**触发场景**：184 主机被重置后，nginx + Pocket systemd unit + binary 全部丢失，但代码（含新飞书回调）已在 `/data/services/opencode-pocket/backend-src-feishu/`。本文档给出端到端的恢复步骤。

---

## Step 1: 在 184 上安装 nginx

```bash
export SSHPASS='Kaixuan2026&#*9527'
sshpass -e ssh -o StrictHostKeyChecking=no -p 25022 root@14.103.112.184 'apt update && apt install -y nginx && systemctl enable nginx --now'
```

## Step 2: 部署前端 (Pocket Web 静态文件)

前端 dist 需要从你的 Mac 重新上传（因为 `/data/www/pocket.kxpms.cn/` 也被清空了）：

```bash
# 在 Mac 上
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/frontend
npm run build
tar czf /tmp/pocket-frontend.tar.gz dist/

# 上传
sshpass -e scp -o StrictHostKeyChecking=no -P 25022 /tmp/pocket-frontend.tar.gz root@14.103.112.184:/tmp/
sshpass -e ssh -o StrictHostKeyChecking=no -p 25022 root@14.103.112.184 'mkdir -p /data/www/pocket.kxpms.cn && cd /data/www/pocket.kxpms.cn && tar xzf /tmp/pocket-frontend.tar.gz --strip-components=1'
```

## Step 3: 写 Pocket nginx 配置（使用仓库最新 SSOT）

`/etc/nginx/conf.d/pocket.kxpms.cn.conf`：

```nginx
server {
    listen 9011;
    server_name pocket.kxpms.cn;

    root /data/www/pocket.kxpms.cn;
    index index.html;

    # Frontend - SPA 路由
    location / {
        try_files $uri $uri/ /index.html;
    }

    # Backend API 代理
    location /api/ {
        proxy_pass http://127.0.0.1:9010;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection 'upgrade';
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_cache_bypass $http_upgrade;
    }

    # WebSocket 代理
    location /ws {
        proxy_pass http://127.0.0.1:9010;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "Upgrade";
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    }

    # 健康检查
    location /healthz {
        proxy_pass http://127.0.0.1:9010;
    }

    # 静态资源缓存
    location ~* \.(js|css|png|jpg|jpeg|gif|ico|svg|woff|woff2|ttf|eot)$ {
        expires 1y;
        add_header Cache-Control "public, immutable";
    }
}
```

```bash
# 禁用可能冲突的 kxpms.cn-all-vhosts 默认配置（k3s 184 自带 nginx 配置可能与手动 conf 冲突）
sshpass -e ssh -o StrictHostKeyChecking=no -p 25022 root@14.103.112.184 'mv /etc/nginx/conf.d/kxpms.conf /etc/nginx/conf.d/kxpms.conf.disabled 2>/dev/null; nginx -t && systemctl reload nginx'
```

## Step 4: 编译 Pocket backend（含飞书回调）

源码已在 `/data/services/opencode-pocket/backend-src-feishu/`，使用 Docker golang 镜像编译（因为 184 没装 Go）：

```bash
# 用 docker golang 镜像编译（CGO 启用，因为 go-sqlite3 需要 cgo）
sshpass -e ssh -o StrictHostKeyChecking=no -p 25022 root@14.103.112.184 'bash -c "
  docker run --rm \
    -v /data/services/opencode-pocket/backend-src-feishu:/src \
    -w /src \
    -e CGO_ENABLED=1 \
    docker.m.daocloud.io/library/golang:1.22-alpine \
    sh -c \"apk add --no-cache gcc musl-dev && go build -o /out/pocketd ./cmd/pocketd/\"
  mkdir -p /data/services/opencode-pocket/backend/bin
  cp /data/services/opencode-pocket/backend-src-feishu/../bin/pocketd /data/services/opencode-pocket/backend/bin/pocketd
  chmod +x /data/services/opencode-pocket/backend/bin/pocketd
  ls -lh /data/services/opencode-pocket/backend/bin/pocketd
"'
```

（注意：上述 docker run 需要挂载输出目录或用 cat 取 binary，简化版是：

```bash
sshpass -e ssh -o StrictHostKeyChecking=no -p 25022 root@14.103.112.184 'bash -c "
  docker run --rm \
    -v /data/services/opencode-pocket/backend-src-feishu:/src \
    -w /src \
    -e CGO_ENABLED=1 \
    docker.m.daocloud.io/library/golang:1.22-alpine \
    sh -c \"apk add --no-cache gcc musl-dev && go build -o /tmp/pocketd-static ./cmd/pocketd/ && cat /tmp/pocketd-static > /data/services/opencode-pocket/backend/bin/pocketd && chmod +x /data/services/opencode-pocket/backend/bin/pocketd\"
"'
```

## Step 5: 写 systemd unit + 注入环境变量

`/etc/systemd/system/opencode-pocket.service`：

```ini
[Unit]
Description=OpenCode Pocket Backend Service
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=/data/services/opencode-pocket/backend
ExecStart=/data/services/opencode-pocket/backend/bin/pocketd
Restart=always
RestartSec=5

# 端口
Environment="POCKET_HTTP_PORT=9010"
Environment="POCKET_DB_PATH=/data/services/opencode-pocket/backend/data/pocket.sqlite"
Environment="POCKET_VERSION_CONFIG_PATH=/data/services/opencode-pocket/backend/config/version.json"

# OpenCode 实例目录 (含 SSH 反向隧道到本机 OpenCode)
Environment="POCKET_INSTANCE_CATALOG_JSON=[{\"id\":\"opencode-local-test\",\"displayName\":\"Local Test\",\"apiBaseURL\":\"http://localhost:14096\",\"environment\":\"development\"},{\"id\":\"opencode-kaixuan1\",\"displayName\":\"Kaixuan 1\",\"apiBaseURL\":\"https://acc.kxpms.cn/mcp\",\"environment\":\"production\"},{\"id\":\"opencode-kaixuan2\",\"displayName\":\"Kaixuan 2\",\"apiBaseURL\":\"https://acc.kxpms.cn/mcp\",\"environment\":\"production\"},{\"id\":\"opencode-kaixuan3\",\"displayName\":\"Kaixuan 3\",\"apiBaseURL\":\"https://acc.kxpms.cn/mcp\",\"environment\":\"production\"}]"

# 飞书事件回调 (m.kxpms.cn/callback/feishu)
Environment="POCKET_FEISHU_APP_ID=${POCKET_FEISHU_APP_ID}"
Environment="POCKET_FEISHU_APP_SECRET=${POCKET_FEISHU_APP_SECRET}"
Environment="POCKET_FEISHU_VERIFY_TOKEN="
Environment="POCKET_FEISHU_VERIFY_SECRET="
Environment="POCKET_FEISHU_ENCRYPT_KEY="

StandardOutput=journal
StandardError=journal
SyslogIdentifier=opencode-pocket

[Install]
WantedBy=multi-user.target
```

```bash
sshpass -e ssh -o StrictHostKeyChecking=no -p 25022 root@14.103.112.184 'bash -c "
  mkdir -p /data/services/opencode-pocket/backend/data /data/services/opencode-pocket/backend/config
  cp /data/services/opencode-pocket/backend-src-feishu/config/version.json /data/services/opencode-pocket/backend/config/ 2>/dev/null || true
  systemctl daemon-reload
  systemctl enable opencode-pocket
  systemctl start opencode-pocket
  sleep 2
  systemctl is-active opencode-pocket
  journalctl -u opencode-pocket --no-pager -n 10 --since \"10 seconds ago\"
"'
```

期望日志包含：`Using OpenCode HTTP adapter`、`Loaded 4 OpenCode instances from config`、`pocketd listening on :9010`

## Step 6: 验证 9010 端口上飞书回调

```bash
# 1) URL 验证
curl -s -X POST http://localhost:9010/callback/feishu \
  -H "Content-Type: application/json" \
  -d '{"type":"url_verification","challenge":"test123","token":""}'
# 期望: {"challenge":"test123"}

# 2) event_callback (dev 模式，跳过签名校验)
curl -s -X POST http://localhost:9010/callback/feishu \
  -H "Content-Type: application/json" \
  -d '{"schema":"2.0","header":{"event_type":"im.message.receive_v1"},"event":{"type":"im.message.receive_v1","app_id":"${POCKET_FEISHU_APP_ID}","tenant_key":"test","message":{"chat_id":"oc_test","message_id":"om_test","message_type":"text","content":"{\"text\":\"hi\"}"},"sender":{"sender_id":"ou_test","sender_type":"user"}}'
# 期望: {"code":0,"msg":"ok"}
# 184 日志应有: "[feishu] message received: chat=oc_test ..."

# 3) 未知事件类型
curl -s -X POST http://localhost:9010/callback/feishu \
  -H "Content-Type: application/json" \
  -d '{"schema":"2.0","header":{"event_type":"unknown.event"},"event":{"type":"unknown.event","app_id":"x","tenant_key":"y"}}'
# 期望: {"code":0,"msg":"ok"}
```

## Step 7: 56 nginx m.kxpms.cn.conf 切换 upstream 到 pocket_backend

仓库文件 `configs/nginx/m.kxpms.cn.conf` 已修改：
- 新增 `upstream pocket_backend { server 172.31.0.4:9010; }`
- `/callback/feishu` location 的 `proxy_pass` 从 `companion_api_backend` 改为 `pocket_backend`

在 56 上部署：

```bash
# 在 Mac 上
cd /Users/xutaohuang/workspace/official-deploy
# 用部署脚本（脚本在 scripts/ 目录，需要找 m-kxpms 的具体脚本）
ls scripts/*m.kxpms* scripts/*m-kxpms-cn* 2>/dev/null
# 或手动 scp + 软链
sshpass -e scp -o StrictHostKeyChecking=no -P 25022 configs/nginx/m.kxpms.cn.conf root@14.103.169.56:/etc/nginx/sites-available/m.kxpms.cn
sshpass -e ssh -o StrictHostKeyChecking=no -p 22 root@14.103.169.56 'ln -sf /etc/nginx/sites-available/m.kxpms.cn /etc/nginx/sites-enabled/m.kxpms.cn && nginx -t && nginx -s reload'
```

## Step 8: 端到端验证 (通过 56 nginx)

```bash
# url_verification 走 56 nginx 转发到 184:9010
curl -s -X POST https://m.kxpms.cn/callback/feishu \
  -H "Content-Type: application/json" \
  -d '{"type":"url_verification","challenge":"test123"}'
# 期望: {"challenge":"test123"}
```

## Step 9: 飞书后台配置 (手工)

1. 登录飞书开放平台 → 应用 → `${POCKET_FEISHU_APP_ID}`
2. 「事件订阅」 → 添加「事件请求 URL」: `https://m.kxpms.cn/callback/feishu`
3. 勾选事件权限：`im.message.receive_v1`、`im.message.message_read_v1`、`docx.document.*`、`drive.file.*`、`wiki.*`
4. 保存后飞书会发送 url_verification，期望看到「连接成功」
5. 在飞书群里发消息，期望 184 Pocket 后端日志看到：
   ```
   [feishu] event=im.message.receive_v1 app=${POCKET_FEISHU_APP_ID} tenant=...
   [feishu] message received: chat=oc_xxx type=text msg=om_xxx sender=ou_xxx
   ```

## 完成标志

- [ ] `curl http://localhost:9010/healthz` 返回 `ok`
- [ ] `curl http://localhost:9011/healthz` 返回 200 (经 nginx 9011)
- [ ] `curl http://localhost:9011/api/tasks?instance_id=opencode-local-test` 返回 21 个真实 OpenCode session
- [ ] `curl https://m.kxpms.cn/callback/feishu` (POST url_verification) 返回 challenge
- [ ] 飞书后台「事件订阅」URL 验证通过
- [ ] 飞书群发消息后 184 日志有 `[feishu] message received` 输出

## 飞书生产加固 (后续)

- 在 184 systemd 添加 `POCKET_FEISHU_VERIFY_SECRET=<flybook后台生成的Encrypt Key>`
- 在 56 nginx m.kxpms.cn.conf 添加飞书 IP 白名单（allow 116.63.159.0/24 等）
- WebSocket Hub 已经把事件 broadcast 给前端，前端可以在 WebSocket 消息 type=`feishu.message`/`feishu.doc` 时做 toast/通知

## 回滚

如飞书回调需要暂时绕过：

```bash
sshpass -e ssh -o StrictHostKeyChecking=no -p 22 root@14.103.169.56 \
  'sed -i "s|http://pocket_backend|http://companion_api_backend|" /etc/nginx/sites-available/m.kxpms.cn && nginx -s reload'
```

## 备份

```bash
# 所有 184 配置文件备份到 /tmp/
sshpass -e ssh -o StrictHostKeyChecking=no -p 25022 root@14.103.112.184 \
  'cp /etc/systemd/system/opencode-pocket.service /tmp/opencode-pocket.service.feishu.bak
   cp /etc/nginx/conf.d/pocket.kxpms.cn.conf /tmp/pocket.kxpms.cn.conf.feishu.bak 2>/dev/null
   echo "backed up to /tmp/"'
```

## 故障排查

| 现象 | 排查 |
|------|------|
| 9010 端口无响应 | `systemctl status opencode-pocket` + `journalctl -u opencode-pocket -n 50` |
| 9011 502 错误 | `nginx -t` 看 conf 语法；`tail /var/log/nginx/pocket.kxpms.cn.error.log` |
| m.kxpms.cn 502 跨机转发 | `curl http://172.31.0.4:9010/healthz` 从 56 看 184 是否可达 |
| 飞书不重试 | 飞书要求 3s 内返回 {code:0}，检查 `journalctl -u opencode-pocket` |
| 签名校验失败 | 配置 POCKET_FEISHU_VERIFY_SECRET 后飞书发请求带 X-Lark-Signature=base64(hmac_sha256(ts+nonce+secret, body))，检查 timestamp 与服务端时间是否同步（±5min）|
