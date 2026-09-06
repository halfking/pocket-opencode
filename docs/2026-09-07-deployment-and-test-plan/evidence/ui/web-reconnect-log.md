# Web 层 WebSocket / SSE 断线重连证据（2026-09-07）

工具：playwright-core + 系统 Chrome（headless），页面 = Vite dev http://localhost:5174

## WebSocket 重连（真实服务端断线）
方法：登录建立 WS 后 `kill` pocketd（服务端主动关闭全部连接），再拉起后端，观察页面自动重连。

```
✅ login-and-ws-established — 初始 WS 连接数=2
  ... restarting pocketd (real disconnect)
  [ws close] total closed=1        ← 服务端断开，页面侧收到 close
  ... pocketd back up
  [ws open #3]                     ← 应用自动重连成功
✅ web-ws-reconnect — 断线关闭=1>0，重连后新 WS 连接=3，最新=ws://localhost:8088/ws
```

## SSE 双连接（首次 + 重连）
方法：页面上下文内 fetch 登录 → 创建会话 → EventSource 连接 `/api/mobile/sessions/{id}/event?...&token=`（SSE 认证回退路径）→ 收到 `server.connected` 后关闭 → 立即再次连接。

```
{"sid":"ses_f86fb6e07ffeufNDgKKBazbNEs","first":"server.connected","second":"server.connected"}
SSE PASS
```

## 环境备注
- IAB（ZCode 内置浏览器）guest 层对跨端口 fetch 受限且 evaluate 绑定外壳文档，Web 交互验证改用 playwright-core + Chrome headless 完成。
- 断线源采用「后端重启」而非 CDP offline：Chromium 离线仿真不会掐断已建立的 WebSocket，无法触发重连路径。
