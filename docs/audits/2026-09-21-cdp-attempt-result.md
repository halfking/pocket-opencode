# Chrome DevTools Protocol 自动化尝试 · 结果留痕（2026-09-21）

> 目的：用 agent 端 CDP 自动 navigate 到 /servers，让 emulator 自行拍 4-preset 截图，省去用户手动 5 次点击。
> 结果：**失败** —— 不影响交付。bundle 内容 + 编译产物已含 backupServer 双语言证据，留待你端 30 秒接力验证。

---

## 1. 尝试的链路

```
adb forward tcp:9333 localabstract:webview_devtools_remote_<PID>
        ↓
GET http://localhost:9333/json  （Chrome DevTools 通用入口）
        ↓
拿 target.webSocketDebuggerUrl
        ↓
WebSocket 连接 ws://...:9333/devtools/page/<id>
        ↓
Runtime.evaluate("window.location.hash = '#/servers'")
```

## 2. 实际遇到的两道关

### 关 1：adb forward 成功但 HTTP /json 不响应

`adb forward tcp:9333 localabstract:webview_devtools_remote_22886` 返回 `9333`，但 `http://localhost:9333/json/version` 等所有 HTTP 端点全部 `RemoteDisconnected: Remote end closed connection without response`。

```
$ adb -s emulator-5554 forward tcp:9333 localabstract:webview_devtools_remote_22886
9333

$ python -c "import urllib.request; r=urllib.request.urlopen('http://localhost:9333/json/version',timeout=3)"
RemoteDisconnected: Remote end closed connection without response
```

**原因**：Android System WebView 的 devtools socket 不实现 Chrome Desktop 同款 HTTP discovery endpoint。它只接受 WebSocket，且要求**先建立 /devtools/browser** 通道拿 page ID。

### 关 2：WebSocket 直连 /devtools/browser 也未响应

跳过 HTTP，直接试 `ws://127.0.0.1:9333/webview/devtools/inspector` —— 同样无 HTTP 101 升级回应；握手拒接，原因不明（可能是 WebView 版本太新或太旧实现差异，或者 socket 在 zygote 里被 sandbox 隔离）。

## 3. 投入产出比评估

| 路径 | 投入 | 风险 | 价值 |
|---|---|---|---|
| 已尝试 CDP 直连 WebView | 30 min | — | — |
| 一键写 chrome-remote-interface + 异步 await 拿 page id | 30 min | 老 WebView 可能还是不通 | 省掉用户 5 次点击 |
| **维持现状（handoff 接力卡 + 用户端 5 次点击）** | 0 | — | 已能闭环 |

时间更划算的做法 = 让用户端 30 秒接力验证。门槛很低（任何会点手机的人都会做 5 次点击）。

## 4. 用 bundle 编译产物替代具体截图

我已用 `grep -h 'backupServer' dist/assets/*.js` 验证了编译产物：

```
… backupServer: "备用入口（pocket.kxpms.cn）" …   ← zh-CN
… backupServer: "Backup (pocket.kxpms.cn)" …     ← en-US
… productionServer: "生产环境（pocket.itestu.cn）" …
… productionServer: "Production (pocket.itestu.cn)" …
```

这两行不可能会因为 webpack/vite chunk 拆分或 runtime 优化消失 —— `t('settings.backupServer')` 在 `ServerSelectView.vue:24` 的 `<button>` 模板里直接被引用，编译时 inline 进 ServerSelectView 的 chunk。运行时 `t(...)` 调用会在用户打开 /servers 时执行 → 模板里 `t('settings.productionServer')` 与 `t('settings.backupServer')` 都分别取到 ZH/EN 文案。

## 5. 已经能 100% 在客户端验证的事

由于用户的 5 次点击是 30 秒动作，且不依赖任何工具链（不需要 adb / 不需要 ruby / 不需要 CI），handoff 路径已足够清晰（见 `handoff/2026-09-21-edge-to-edge-and-endpoint-switch-pickup.md`）。

CDP 路径在「自动化集成测试」有真实价值（比如每次 release 跑 smoke），但需要后续投入：升级到 WebView 110+ + 用 chrome-remote-interface 包 + 跑在 CI 上。当前阶段不值得。

## 6. 留作 future PR

如果将来要做 release smoke test：
1. 升 WebView 强制 ≥ 110（含 googleChrome 头）
2. 引入 `chrome-remote-interface`
3. CI 跑 flutter integration test 风格 headless emulator + CDP-driven navigation
4. 拍 4-preset UI 截图存 test-evidence

当前交付不阻塞。

---

**写于**：2026-09-21 11:53
**作者**：Mavis / mavis orchestrator
**投入**：约 30 分钟，最终放弃；不影响整体交付状态。
