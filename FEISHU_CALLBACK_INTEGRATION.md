# Pocket 飞书事件回调集成 - 部署完成报告

**部署时间**: 2026-07-01
**状态**: ✅ 端到端公网验证通过

---

## 1. 概述

Pocket 后端 (m.kxpms.cn/callback/feishu) 接收飞书事件回调 URL 验证、HMAC-SHA256 签名验证、事件分发。
事件包括：消息、文档/多维表、URL 验证等。

**完整链路**:
```
飞书 (公网) → 14.103.169.56:443 nginx (m.kxpms.cn:443) 
            → 172.31.0.4:9010 (Pocket backend /callback/feishu)
            → internal/feishu/handler.go (URL 验证 + 签名 + 事件分发)
```

---

## 2. 改动文件清单

### 2.1 Pocket 后端 Go 代码 (5 个文件)

| 文件 | 改动 |
|------|------|
| `services/opencode-pocket/backend/internal/config/config.go` | +5 飞书配置字段（APP_ID, APP_SECRET, VERIFY_TOKEN, VERIFY_SECRET, ENCRYPT_KEY） |
| `services/opencode-pocket/backend/internal/feishu/handler.go` | **新建** 飞书回调处理（URL 验证 + HMAC 验签 + 事件分发） |
| `services/opencode-pocket/backend/internal/notification/event.go` | +2 事件类型（EventFeishuMessage, EventFeishuDoc） |
| `services/opencode-pocket/backend/internal/server/server.go` | +1 路由（/callback/feishu） |
| `services/opencode-pocket/backend/internal/server/server.go` (handleFeishuCallback) | 调用 feishu.PublicEntry 包装 |

### 2.2 56 nginx (1 个文件)

| 文件 | 改动 |
|------|------|
| `configs/nginx/m.kxpms.cn.conf` | 新增 `upstream pocket_backend { server 172.31.0.4:9010 }`；`location /callback/feishu` 的 `proxy_pass` 从 `companion_api_backend` 改为 `pocket_backend` |

### 2.3 184 systemd (systemd unit)

`/etc/systemd/system/opencode-pocket.service` 新增：
```
Environment="POCKET_FEISHU_APP_ID=${POCKET_FEISHU_APP_ID}"
Environment="POCKET_FEISHU_APP_SECRET=${POCKET_FEISHU_APP_SECRET}"
Environment="POCKET_FEISHU_VERIFY_TOKEN="
Environment="POCKET_FEISHU_VERIFY_SECRET="
Environment="POCKET_FEISHU_ENCRYPT_KEY="
```

---

## 3. 飞书协议处理逻辑

### 3.1 URL 验证 (URL Verification)
飞书首次订阅时发送 `{"type":"url_verification","token":"...","challenge":"..."}`，
Pocket 返回 `{"challenge": "..."}`。

如果配置了 `POCKET_FEISHU_VERIFY_TOKEN`，会先校验 `token` 字段；未配置则跳过（dev 模式）。

### 3.2 事件回调 (Event Callback)
飞书发送 `{"schema":"2.0","header":{...},"event":{...}}`，
Pocket 必须在 3 秒内返回 `{"code":0,"msg":"ok"}`，否则飞书会持续重试。

### 3.3 HMAC-SHA256 签名验证
V2 协议（未加密）下，校验请求头 `X-Lark-Signature`：
```
base64(hmac_sha256(timestamp + nonce + secret, body))
```
其中 `timestamp` 来自 `X-Lark-Request-Timestamp`，`nonce` 来自 `X-Lark-Request-Nonce`。

如果 `POCKET_FEISHU_VERIFY_SECRET` 为空，则跳过签名校验（dev 模式，生产前必须配置）。

### 3.4 事件分发
- `im.message.receive_v1` → 推 WebSocket（type: `feishu.message`）+ 日志
- `im.message.message_read_v1` → 推 WebSocket（type: `feishu.message`）+ 日志
- `docx.document.*` / `drive.file.*` / `wiki.*` → 推 WebSocket（type: `feishu.doc`）+ 日志
- 未知事件 → 仅日志，返 `{"code":0,"msg":"ok"}` 不重试

---

## 4. 端到端验证结果

### 4.1 184 主机 (Pocket 后端直测)
| 步骤 | 响应 |
|------|------|
| healthz | `ok` |
| URL 验证 | `{"challenge":"test123"}` |
| im.message.receive_v1 | `{"code":0,"msg":"ok"}` |
| docx.document.created_v1 | `{"code":0,"msg":"ok"}` |
| 未知事件 | `{"code":0,"msg":"ok"}` |

### 4.2 184 主机 (经 nginx 9011)
| 步骤 | 响应 |
|------|------|
| 9011/healthz | `ok` |
| 9011/api/instances | 返回 4 个实例 |
| **9011/callback/feishu (URL 验证)** | `{"challenge":"e2e-via-nginx-9011"}` |

### 4.3 公网 (经 56 nginx)
| 步骤 | 响应 | 184 日志 |
|------|------|---------|
| **公网 URL 验证** | `{"challenge":"e2e-via-public-m-kxpms-cn"}` | `[feishu] url_verification OK` |
| **公网 event_callback** | `{"code":0,"msg":"ok"}` | `[feishu] message received: chat=oc_public_test ...` |

---

## 5. 飞书后台手工配置步骤

请在飞书开放平台完成以下配置：

### 5.1 添加事件请求 URL
1. 登录 [飞书开放平台](https://open.feishu.cn)
2. 进入应用 `${POCKET_FEISHU_APP_ID}`（即 App ID；Feishu 文档中亦称 `appkey`）
3. 「事件订阅」→ 添加「事件请求 URL」: `https://m.kxpms.cn/callback/feishu`
4. 保存。飞书会自动发送 `url_verification`，Pocket 后端日志会显示：
   ```
   [feishu] url_verification OK challenge="..."
   ```
5. 期望看到「连接成功」提示

### 5.2 添加事件权限
在「权限管理」中勾选以下事件：

| 事件 | 用途 |
|------|------|
| `im.message.receive_v1` | 接收消息 |
| `im.message.message_read_v1` | 消息已读 |
| `docx.document.created_v1` | 文档创建 |
| `docx.document.edited_v1` | 文档编辑 |
| `docx.document.deleted_v1` | 文档删除 |
| `drive.file.created_v1` | 文件创建 |
| `drive.file.edited_v1` | 文件编辑 |
| `drive.file.title_updated_v1` | 文件标题更新 |
| `wiki.space.created_v1` / `wiki.space.edited_v1` | 知识库空间 |
| `wiki.node.created_v1` / `wiki.node.edited_v1` | 知识库节点 |

### 5.3 获取 Encrypt Key 并配置生产签名验证
1. 飞书后台「事件订阅」→「加密策略」→ 获取「Encrypt Key」
2. 在 184 systemd unit 添加 `Environment="POCKET_FEISHU_VERIFY_SECRET=<encrypt_key>"`
3. `systemctl daemon-reload && systemctl restart opencode-pocket`
4. 重新触发 url_verification，签名应通过

### 5.4 验证（可选）
1. 在飞书群里发消息
2. 184 日志应有 `[feishu] message received: chat=oc_xxx type=text msg=om_xxx sender=ou_xxx`
3. 前端 WebSocket 客户端（如果有）会收到 `feishu.message` 消息

---

## 6. 生产加固建议

### 6.1 56 nginx IP 白名单
在 `m.kxpms.cn.conf` 的 `location /callback/feishu` 中添加：
```nginx
allow 116.63.159.0/24;   # 飞书官方 IP 段（具体以最新飞书文档为准）
allow 220.181.0.0/16;
deny all;
```

### 6.2 时间同步
签名校验使用时间戳，必须保证 184 服务与飞书服务器时间同步（±5分钟）。建议 NTP。

### 6.3 监控告警
- 184 日志中 `[feishu] signature verification failed` 应触发告警（攻击探测）
- 飞书后台会重试 3 次都失败后告警
- Pocket 持续 5xx 应告警

### 6.4 Rate Limit
当前未做 rate limit。若有 DDoS 风险，在 56 nginx 加上：
```nginx
limit_req_zone $binary_remote_addr zone=feishu_callback:10m rate=30r/m;
location /callback/feishu {
    limit_req zone=feishu_callback burst=10 nodelay;
    ...
}
```

---

## 7. 故障排查

| 现象 | 排查 |
|------|------|
| 飞书后台「URL 验证失败」 | `curl -sk -X POST https://m.kxpms.cn/callback/feishu -H "Content-Type: application/json" -d '{"type":"url_verification","challenge":"test"}'` |
| 飞书事件 3 秒超时（持续重试） | 184 `journalctl -u opencode-pocket -f` 看是否有 5xx |
| 签名验证失败 | 184 日志看具体错；确保 `POCKET_FEISHU_VERIFY_SECRET` 与飞书后台 Encrypt Key 完全一致（无空格/换行） |
| 56 nginx 502 | `curl http://172.31.0.4:9010/callback/feishu` 从 56 看 184 端口可达性 |
| 56 → 184 网络不通 | 走 56 → 172.31.0.4 (NPS VPN 内网) |

---

## 8. 备份位置

- 56: `/etc/nginx/sites-available/m.kxpms.cn.backup.20260701031135` 和 `m.kxpms.cn.before-feishu`
- 56: `/etc/nginx/conf.d/00-pocket.kxpms.cn.conf.bak-8088.20260701`
- 184 systemd: 已通过 git commit (backend 源码 backend-src-feishu/) 持久化

## 9. 代码部署位置

- Mac 本地: `/Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend/`
  - `internal/feishu/handler.go` (新文件)
  - `internal/config/config.go` (修改)
  - `internal/notification/event.go` (修改)
  - `internal/server/server.go` (修改)
- 184 部署: `/data/services/opencode-pocket/backend-src-feishu/` (源码) + `/data/services/opencode-pocket/backend/bin/pocketd` (编译后 binary)
- 56 nginx: `/etc/nginx/sites-available/m.kxpms.cn` (含 pocket_backend upstream 和改写后的 /callback/feishu)
