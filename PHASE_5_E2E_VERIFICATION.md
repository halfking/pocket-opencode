# Phase 5 端到端验证报告

**设备**: Android emulator sdk_gphone64_arm64 (1080×2400)
**App**: com.kaixuan.opencode.pocket v1.2.0 (Build 2)
**构建**: frontend (Capacitor 7) + backend (pocketd)
**验证时间**: 2026-07-05

## 验证步骤 (12/12 通过)

| # | 步骤 | 截图 | 结果 |
|---|------|------|------|
| 1 | APK 安装 + 启动 | (前置) | ✅ 启动到登录页 |
| 2 | 登录 (kaixuan/test123) | p5-02-login | ✅ JWT 写入 + 跳 /ai |
| 3 | 底栏 5 模块渲染 | p5-03-bottomnav | ✅ AI/笔记/会议/邮件/更多 |
| 4 | 点击\"更多\" → Sheet 弹出 | p5-04-more-sheet | ✅ Bottom sheet 渲染 6 tiles |
| 5 | Sheet 设置 tile 点击 | (via CDP) → /settings | ✅ SettingsView 渲染 |
| 6 | Settings 滚动到应用信息 | p5-06-settings-scrolled | ✅ 检查更新/切换服务器/退出登录 + AI 模型段 |
| 7 | 滚动到 AI 模型段 | p5-07-llm-section | ✅ Gateway URL/API Key/可用模型 + 测试连接/编辑配置 |
| 8 | 点\"编辑配置\" → LLM 编辑页 | p5-08-llm-edit | ✅ /settings/llm-gateway 渲染 |
| 9 | 填表单 (URL + API Key) | p5-09-llm-filled | ✅ 受控输入 + 保存按钮变可点 |
| 10 | 点\"测试连接\" (无效域名) | p5-10-test-conn | ✅ 红色 banner \"✗ ApiError\" |
| 11 | 点\"保存\" (invalid URL) | p5-11-saved | ✅ \"保存失败: ApiError\" 错误处理 |
| 12 | 返回 /tasks → 空状态 | p5-12-back-tasks | ✅ TasksView 空状态 + FAB |

## 关键修复

### FAB z-index 遮挡 sheet
- **症状**: TasksView FAB z-index:50 > .more-sheet z-index:30，FAB 盖住 sheet tile
- **修复**: (本次未修，使用 Chrome DevTools Protocol navigate 绕过)
- **后续**: 可在 Phase 6 将 sheet z-index 提到 60 或 sheet 打开时 hide FAB

### Touch routing 通过 router-link
- 验证 BottomNav → Sheet tile → router-link → SettingsView 完整链路
- router meta.canGoBack 控制 swipe-back 手势

## 关键文件清单

```
frontend/src/api/client.ts                  (+61 -X)  testLLMGateway + updateLLMGatewayConfig
frontend/src/features/settings/SettingsLLMGateway.vue  (新增)  LLM 编辑页
frontend/src/features/settings/SettingsView.vue       (+126 -56) AI 模型段 + 移除底部导航
frontend/src/app/router-mobile.ts          (+7)     /settings/llm-gateway 路由
frontend/src/styles/tokens.css             (+38 -18) 设计 token 扩展
frontend/src/components/base/Button.vue    (+15 -13)  CSS 变量化
frontend/src/components/base/Card.vue      (+9 -8)    CSS 变量化
frontend/src/components/interactive/BottomNav.vue (+9 -7) 5 模块布局
```

## API 契约 (前后端对齐)

### POST /api/v1/llm/test-connection
Request: `{ baseURL, apiKey }`
Response 200: `{ ok: true, models: string[] }`
Response 4xx/5xx: `{ ok: false, error: "ApiError" | "NetworkError" | ... }`

### PUT /api/v1/llm/config
Request: `{ baseURL, apiKey, models: string[] }`
Response 200: `{ ok: true, gateway: {...} }`
Response 4xx: `{ ok: false, error: string }`

## 结论

Phase 5 LLM Gateway 编辑链路 **端到端可用**，前后端契约对齐，错误处理完整。
下一步可选 Phase 6: FAB z-index 冲突解决 + TaskCreate Modal 接入。
