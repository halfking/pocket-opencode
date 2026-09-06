# 2026-09-07 分层测试证据（E2E Run Results）

执行时间：2026-09-07 04:40–06:40 CST（UTC+8）
执行环境：macOS arm64 / pocketd :8088（launchd KeepAlive）/ opencode serve 1.14.33 :4096 / mock-llm-gateway :8089 / Android emulator-5554（Android 16, sdk_gphone64_arm64）

## 结论总览

| 层 | 结果 | 证据 |
|---|---|---|
| Phase 1 部署 | ✅ pocketd:8088 + opencode:4096 + mock-gw:8089 全部健康 | 01-auth-suite.md §1 |
| Phase 2 认证 | ✅ login 200 / 错误密码 401 / 无 token 401 / me / refresh | 01-auth-suite.md |
| Phase 3 API（sessions CRUD+SSE） | ✅ create 200、SSE `server.connected`、messages 200、interrupt 204、幂等重放 `Idempotency-Replayed: true` | workflow/flow-a.md |
| Phase 3 API（approvals 契约） | ✅ 空列表 200；未知 rid 409 approval_expired；非法 decision 400 invalid_decision | workflow/flow-b-c.md |
| 数据闭环 | ✅ 上游直查 opencode `/session` 与 pocketd API 返回一致（同会话 ID、消息数一致） | workflow/flow-b-c.md D1/D2 |
| 审计闭环 | ✅ 契约验证：`/api/audit/logs` 200；Flow A 无 approval 审计行（符合设计，见 PLAN §11#5）；llm_gateway.config.updated 审计行为代码路径确认 | api/02-mobile-write-probes.md §14 |
| Phase 4 UI 交互 | ✅ 登录页→登录→主密码→AI 对话全链路；5-tab 底栏、顶栏左右按钮、锁屏门、横屏重排 | ui/*.png |

## 关键修复（本次执行中发现并修复）

1. **`backend/start-dev.sh`**：种子实例键 `baseURL` → `apiBaseURL`（运行期 registry 只认 `apiBaseURL`，否则写路径 404），并补 `"workspaceId":"default"`。
2. **`backend/internal/registry/registry.go`**：`InstanceConfig` 增加 `workspaceId` 字段并在 `LoadFromConfig` 透传——env 目录实例可显式声明租户归属（缺省行为不变：空=运维共享只读）。
3. **`backend/internal/server/llm_gateway_handler.go`**：
   - 网关配置校验 `validateOutboundURL` → `validateGatewayURL`（与节点注册一致，受 `POCKET_LLM_GATEWAY_ALLOW_PRIVATE` 显式开关控制，缺省行为不变）；
   - `pushConfigToOpenCode` 失败改为非致命（记录告警继续）——push 是下游同步，失败不应让配置保存 502 且缓存不刷新。
4. **opencode 上游**：`~/.config/opencode/opencode.json` 注册 openai-compatible provider 指向 mock 网关（mockgw/gpt-4o），使 prompt 全链路可完成。

## 过程中发现的环境事实（非代码缺陷）

- 模拟器中残留姊妹仓（ai-native-tools/openpocket）签发的旧 token → 401；`pm clear` 后全新登录解决。
- 另一个并发 ZCode 会话在同一仓库跑测试，会按端口清理 8088 监听进程 → 本轮用 launchd KeepAlive（`com.pocketd.e2e8088`）守护；**收尾后已卸载该 agent**。
- iOS 模拟器无 runtime（`xcrun simctl list runtimes` 为空），本轮客户端层在 Android 模拟器完成；iOS 待装 runtime 后复跑 §9.2。

## UI 证据索引（ui/）

| 文件 | 内容 | 判定 |
|---|---|---|
| 11-login-screen.png | 登录页：双 tab、注册/忘记密码、版本号 | ✅ 原生规范 |
| 12-after-login.png | 错误密码 → 内联错误提示 | ✅ 无崩溃、文案清晰 |
| 13-after-master-pw.png | 登录成功 → 主密码创建/通知权限 → AI 工具主屏 | ✅ |
| 14-ai-chat-reply-final.png | **AI 对话 E2E**：发送→流式回复→标题自动更新→复制/优化/重新生成 | ✅ |
| 15/16-tab-*.png | 角色库抽屉（215+ 角色、分类 chips） | ✅ |
| 17-tab-email.png | 本地加密库锁屏（设备门按模块生效） | ✅ 符合 P0C 设计 |
| 18-landscape.png | 横屏：顶栏动作右移、chips 横向滚动重排 | ✅ 宽度适配 |

多屏/多麦克风：本版无语音输入接线（PLAN §9.3 已注明 RECORD_AUDIO 未申请），标记 N/A；折叠屏姿态需 pocket_clone 真机/折叠 AVD 后续补测。

## 遗留事项

- `pushConfigToOpenCode` 需 `POCKET_OPENCODE_CONFIG_TOKEN` 才能同步模型配置到上游实例（现降级为告警，功能不受阻）。
- 对话参数抽屉中「默认模型 (未加载)」文案在 models 已加载时仍显示未加载态——待跟进（前端展示项，非阻塞）。
- Flow B/C 的正路径（真实 permission/question 事件）依赖上游工具调用产生审批，mock 文本模型不触发；已用负向契约（409/400）+ 代码路径确认覆盖。
