# Android 模拟器本地离线测试报告

- 日期：2026-07-14
- AVD：`pocket_test`（Android 11/API 30 arm64）
- 模拟器：`emulator-5554`
- SDK：`/Users/xutaohuang/Library/Android/sdk`
- Pocket API：`http://10.0.2.2:8088`（模拟器访问宿主 Docker）
- Pocket frontend：容器 `4175`，模拟器通过 WebView 内嵌

## 构建与部署

| 检查项 | 结果 | 备注 |
| --- | --- | --- |
| 前端 typecheck | PASS | vue-tsc --noEmit |
| 前端 build | PASS | Vite 1.24s |
| Capacitor sync android | PASS | assets 同步到 `frontend/android/app/src/main/assets/public` |
| Gradle assembleDebug --offline | PASS | 使用普通 Oracle JDK 21，GraalVM JDK 触发 jlink 错误 |
| APK size | 24 MB | `frontend/android/app/build/outputs/apk/debug/app-debug.apk` |
| 离线依赖 | 完整 | npm/Gradle 缓存已预热，`--offline` 成功 |

## 模拟器网络与连接

| 检查项 | 结果 | 备注 |
| --- | --- | --- |
| 模拟器启动 | PASS | `pocket_test` AVD，sys.boot_completed=1 |
| ADB 在线 | PASS | `emulator-5554` |
| 宿主网络 `10.0.2.2` | PASS | ping 0.192ms，模拟器标准宿主回环映射 |
| 安装 APK | PASS | `-r` 重新安装 |
| 应用启动 | PASS | `com.kaixuan.opencode.pocket/.MainActivity` |
| WebView 加载 | PASS | `https://localhost/` Capacitor 本地资产协议 |
| API base `http://10.0.2.2:8088` | PASS | 前端 `.env.local` 编译时注入 |

## SQLite 本地存储修复

**问题**：Capacitor SQLite 的 `execute()` 按分号拆分，导致 FTS trigger 的 `BEGIN...END` 体被错误截断，报错 `incomplete input (code 1)`。

**修复**：在 `frontend/src/native/local-db.ts` 新增 `splitSchemaStatements()`，识别 `CREATE TRIGGER` 并保留完整 `BEGIN...END` 块；改用 `executeSet()` 代替 `execute()`。

**验证**：重新构建 APK 后，logcat 不再出现 `incomplete input` 或 `local_notes_au` 错误；`executeSet` 成功执行 6 个 trigger（`local_notes_ai/ad/au`、`local_emails_ai/ad/au`）。

## 功能测试

| 检查项 | 结果 | 备注 |
| --- | --- | --- |
| 首次登录界面 | PASS | `admin/admin` WebView 表单可见 |
| 登录成功 | PASS | JWT token 生成并保存 |
| 主界面加载 | PASS | AI 工具、运行中、会话、笔记、邮件、更多 tabs |
| 本地 SQLite 初始化 | PASS | `lobster` DB，17 张表，6 个 trigger |
| HTTP API 访问 | PASS | `/api/sessions`、`/api/agents`、`/api/tasks` |
| WebSocket 连接 | PASS | `ws://10.0.2.2:8088/ws?token=...`，后端日志显示 `192.168.65.1` 客户端连接 |
| Mixed Content 警告 | 预期 | `https://localhost` 访问 `http://10.0.2.2` 和 `ws://`，Android 11 允许明文流量，Capacitor 已配置 `allowMixedContent` |
| 笔记页面 | PASS | 页面可导航，暂无笔记（空数据库） |
| OpenCode 实例 | SKIPPED | `4096` 未监听，实例显示 offline，符合预期 |

## 截图证据

测试产物位于 `deploy/本地方案/artifacts/emulator-20260714/`：

- `launch.png`：首次启动主界面
- `after-clear.png`：清理数据后启动
- `after-login.png`：登录后主界面
- `after-schema-fix.png`：修复 trigger 后启动
- `notes.png`：笔记页面
- `after-login-schema-fix.log`：完整 Capacitor/SQLite/WebSocket 日志

## JDK 版本差异

| JDK | 结果 | 说明 |
| --- | --- | --- |
| GraalVM CE 21.0.2 | FAIL | `jlink` 错误，Gradle `JdkImageTransform` 失败 |
| Oracle JDK 21.0.6 | PASS | 标准 Oracle JDK，离线构建成功 |

本次构建显式指定 `JAVA_HOME=/Library/Java/JavaVirtualMachines/jdk-21.jdk/Contents/Home`。

## 已知限制

- OpenCode `4096` 未运行，实例/会话链路标记为 SKIPPED。
- 当前无真实笔记/邮件/会议数据，只验证空数据库初始化与 UI 导航。
- WebSocket 建立连接但未测试实时消息推送/响应。
- Mixed Content 警告不影响功能，因 AndroidManifest 已允许明文流量且 MainActivity 允许 WebView mixed content。

## 结论

离线 Android 模拟器测试通过：

- 容器化 Pocket API/frontend 运行正常
- 模拟器通过 `10.0.2.2` 访问宿主 Docker 服务
- APK 使用预热的离线 Gradle/npm 缓存成功构建
- SQLite trigger 分拆问题已修复
- WebSocket 实时连接正常
- 核心 UI 页面可导航
- 本地加密存储初始化成功
