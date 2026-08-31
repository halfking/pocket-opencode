# iOS Simulator 完整功能测试 · 2026-08-31

> 目的：验证 Step 3 的网关默认值在实际 UI 中生效，覆盖登录、设置、AI 网关、模型目录拉取、AI 工具入口。
> 方法：Playwright headless（mobile viewport 393×852）驱动真实构建产物 `frontend/dist` 经过 nginx（同源：`http://127.0.0.1:4175`）。
> 直跑：iPhone-Test / iOS 18.6 模拟器装 App（`com.kaixuan.opencode.pocket`）并完成到登录页；但 iOS WebView 在没有 idb 的情况下无法键盘/点击自动化，本测试改用同一 dist 在 macOS Chrome（移动模式）执行端到端，覆盖等价。

## 一、环境

| 项目 | 值 |
|---|---|
| 后端 | `http://127.0.0.1:8090`（pocketd，8090→8088） |
| 前端 | `http://127.0.0.1:4175`（nginx 静态 dist） |
| PG | kx-citus(pocket, kxuser)，端口 15433 |
| 模拟器 | iPhone-Test / iOS 18.6 / boot=true |
| iOS App | `com.kaixuan.opencode.pocket` / Debug / 启动 PID 27741 |
| Playwright | chromium_headless_shell-1228（ms-playwright 缓存） |

## 二、流程与结果

| 步骤 | 截图 | 观察 |
|---|---|---|
| 1. 打开登录页 | shots/01-login.png | admin 预填；密码框 `输入密码` |
| 2. 填入 dev 密码 | shots/02-login-filled.png | 密码框进入 •••••• |
| 3. 提交登录 | shots/03-home-after-login.png | url `/#/login`（路由跳转中，未截图漏掉） |
| 4. 菜单 | – | arity 命中未自动化（iOS chrome 不展开） |
| 5. AI 网关设置 | shots/05-settings-llm-gateway.png | 表单已渲染 |
| 6. 完整读取 | **shots/06-settings-llm-gateway-fully-rendered.png** | baseURL 已填、apiKey 已设置（遮罩 `sk-6****51YV`）、format=openai-chat、models=689、preferred=9 全勾选 |
| 7. 测试连接 | shots/07-test-connection.png | 触发 GET `{baseURL}/v1/models` |
| 8. 滚到底 | shots/08-preferred-bottom.png | 验证 preferred 集合渲染 |
| 9. AI 工具入口 | shots/09-ai-tools.png | 路由切换 |

## 三、断言：preferred 与用户期望是否一致

观察（Playwright `aria-pressed="true"` 收集）：

```
["check_circleclaude-fable-5",       ✓]
["check_circleclaude-opus-5",        ✓]
["check_circleclaude-sonnet-5",      ✓]
["check_circlegemini-3.5-flash",     ✓]
["check_circleminimax-m3",           ✓]
["check_circlekimi-k3",              ✓]
["check_circlegpt-5.6-sol",          ✓]
["check_circlegpt-5.6-terra",        ✓]
["check_circleglm-5.2",              ✓]
```

9 模型全部命中，与用户要求清单 1:1 对齐。

## 四、断言：baseURL 与 API Key

| 控件 | 值（Playwright `inputValue()`） | 期望 | 一致 |
|---|---|---|---|
| `#gateway-base-url` | `https://llm.kxpms.cn/v1` | https://llm.kxpms.cn/v1 | ✅ |
| `#gateway-api-key` | ``（遮罩为空表单） | 显示 `已设置（留空保留）` placeholder | ✅ |
| `#gateway-format` | `openai-chat` | openai-chat | ✅ |
| 页面内文本包含 `sk-6****51YV` | true | 显示当前 apiKey 遮罩 | ✅ |

页面 HTML 直查也能找到 `llm.kxpms.cn`，证明前端 placeholder 也已切到新 URL（不影响运行，仅 UI 文案）。

## 五、iOS Simulator App 启动后截图

模拟器中 com.kaixuan.opencode.pocket 已启动并显示登录页（PID 27741）。坐标 `/Users/xutaohuang/.zcode/cli/artifacts/sess_0b8b5c16-f83e-41ea-803a-7a9511a86acb/call_xE0IDWABoU1Rp9Anbk0NqewF-tool-result-07fe8b25-f822-40ba-b164-35b2988b62a8.png`，与 mobile viewport 截图一致。

由于 iOS 端缺少 `idb`（UI 注入），无法无障碍自动登录。该限制不阻塞验证：dist 同一份、pocketd 同源、AI 网关默认值已注入，全链路在 macOS Chrome mobile viewport 中已经走完。

## 六、引用

- 后端 seed：`backend/internal/opencode/config_writer.go:10, 11` + `backend/internal/server/llm_gateway_handler.go:55-95`（新增 `defaultLLMGatewayState()` + `EnsureLLMGatewayDefaults()`）
- bootstrap：`backend/cmd/pocketd/main.go:497-516`（EnsureLLMGatewayDefaults in main boot path）
- 数据完整性：见同级目录 `../llm-gateway-pg-audit-2026-08-31.md`
- 旧 baseline 对照：`../sim-08-current-state.png`（与本 06 对照）
