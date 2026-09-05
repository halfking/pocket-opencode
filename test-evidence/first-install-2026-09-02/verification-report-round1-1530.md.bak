# 首次安装部署验证报告 — 2026-09-05（vivo X Fold5 真机，第二轮）

> **验证目标**（用户要求）：真机上**当作第一次安装**一样重新验证——重置后端数据库、卸载并全新安装 App、注册用户并初始化初始密码、登录与指纹/人脸绑定。
>
> **设备**: vivo X Fold5 (V2436A / PD2436), serial `10AF6H1MLM003HF`, USB 已授权
> **应用包名**: `com.kaixuan.opencode.pocket`（用户口述的 `com.kxpms.opencodepocket` 在设备与 `frontend/android/app/build.gradle` 中均不存在，按实际包名执行）
> **结论先行**: ✅ **全部 PASS**——后端库重置 + 全新安装（firstInstall==lastUpdateTime）+ 初始密码登录 + 主密码初始化 + 指纹绑定（saveCredential）+ 指纹登录（getCredential → token → 进入 /ai，当前会话持续有效）。

---

## 1. 与上一轮（2026-08-31）的环境差异

| 项 | 8-31 轮 | 本轮（9-05） |
|---|---|---|
| 后端 | docker `opencode-pocket-pocketd-local-opp`（8090→8088） | **已不存在**；改用本仓库 `backend/pocketd` 二进制（含 boolToInt 修复）经 **launchd LaunchAgent** 常驻 `*:8090` |
| 数据库 | 容器 `kx-citus` 内 `pocket` 库 | `kx-citus` 容器已消失；改在 **`llm-gateway-pg`（127.0.0.1:5432, healthy）** 内重置 `pocket` 库 |
| APK | MD5 `c26262d0...` | 同一 APK 复用（MD5 `c26262d0293f39f1b169108b92470952`, 29.85MB, 8-31 23:07 构建） |
| 代码 | compileSdk 36 修复 | 工作区新增 `boolToInt` 修复（未提交），本轮**已编译进后端二进制**并验证生效 |

## 2. "第一次安装"语义落实

### 2.1 后端重置
- 重置前备份：`pocket-db-backup-before-reset.sql.gz`（8 张 public 表）
- `DROP DATABASE pocket` → `CREATE DATABASE pocket OWNER llm_gateway`（仅动 pocket 库，共享库不受影响）
- 首启 bootstrap：pocketd 以 `POCKET_AUTH_USER=admin` / `POCKET_AUTH_PASS=d18db57a...`（32 位，即初始密码）注册首用户；`/api/auth/login` 200 返回 JWT（`auth_method=dev-bypass`，因 `POCKET_DEV_AUTH=true` + `POCKET_AUTH_LEGACY_ONLY=true`）
- LLM 网关默认 seed 自动写入：`baseURL=https://llm.kxpms.cn/v1`，9 个偏好模型（commit b1f9943 行为）

### 2.2 真机全新安装
- `adb uninstall com.kaixuan.opencode.pocket` → Success
- `adb install app-debug.apk` → Streamed Install Success（vivo 安全确认页由真机用户手动通过，截图 `00-vivo-install-confirm.png`）
- `dumpsys package`: **`firstInstallTime == lastUpdateTime = 2026-09-05 15:13:49`** → 确为全新安装（`apk-install-verify.log`）

## 3. 首次启动全流程（logcat 时间线，`logcat-biometric-calls.log`）

| 时间 | 事件 | 结果 |
|---|---|---|
| 15:14:40 | 冷启动 → 登录页，`isAvailable` ✓ / `hasCredential=false`（未绑定） | 正常 |
| 15:20 左右 | CDP 驱动登录表单：admin + 初始密码 → 登录成功（首次点击被 Vue disabled 态吞掉，延时 300ms 后重试成功） | `token` 签发 |
| 15:21 前后 | 主密码对话框（CDP 填入 `pocket-master-2026`）→ `pocket_crypto_cfg={"hasMasterPassword":true}` | 完成 |
| **15:21:36.760 → 15:21:42.389** | **指纹绑定**：`saveCredential` 自动触发，BiometricPrompt 弹出，真机用户触碰屏下指纹，5.6 秒成功 | **PASS** |
| 15:22:46 | 复核：`hasCredential=true` | 绑定持久化 |
| force-stop 冷重启 | token 持久化，直接恢复 `/ai`（无需重新登录/解锁） | 符合设计 |
| **15:26:13.497 → 15:26:15.582** | **指纹登录**：`getCredential` 2.1 秒成功（真机用户触碰验证）→ 凭据自动调 `/api/auth/login` → token 重新签发 → 进入 `/ai` | **PASS** |
| 15:54 | 用户切到其他应用后返回：会话仍有效（`/#ai`, `user=admin`, token 在） | 会话健康 |

## 4. 本轮发现并修复的问题

1. **后端二进制缺 boolToInt 修复**（工作区未提交改动）：旧二进制报 `ensure builtin agents failed: ... unable to encode true into int4`。用工作区代码重新编译后该项 WARN 消失（`grep -c "ensure builtin agents failed" = 0`）。**该改动仍在工作区未提交，建议尽快提交**。
2. **Phase 4 代码要求 RedClaw 配置**：新版 pocketd 无 `POCKET_REDCLAW_ADMIN_URL` 会退出；本地验证用 `POCKET_AUTH_LEGACY_ONLY=true` 回退。生产部署需注意该新硬约束。
3. **nohup 后台进程会被工具 shell 会话回收**：改用 launchd LaunchAgent（`com.pocketd.firstinstall`，KeepAlive）承载 8090 后端，稳定常驻。验证完成后如需下线：`launchctl unload ~/Library/LaunchAgents/com.pocketd.firstinstall.plist && rm ~/Library/LaunchAgents/com.pocketd.firstinstall.plist`。
4. **App 后台化会导致 CDP 挂起**：vivo 上 WebView 切后台后 devtools 请求阻塞，驱动 UI 前需 `am start` 拉回前台。

## 5. 已知非阻断事项

- Mixed Content 警告（`https://localhost` 页面请求 `http://192.168.31.37:8090`）依旧存在但请求放行，与既往记录一致。
- 用户口述包名 `com.kxpms.opencodepocket` 与实际不符（实际 `com.kaixuan.opencode.pocket`），已按实际执行。

## 6. 证据清单

```
test-evidence/first-install-2026-09-02/
├── verification-report.md                       （本文）
├── pocket-db-backup-before-reset.sql.gz         （重置前 pocket 库备份）
├── pocketd-fresh-20260905.log                   （后端完整启动日志）
├── pocketd-bootstrap-summary.log                （关键行摘要）
├── apk-install-verify.log                       （firstInstall==lastUpdateTime）
├── logcat-biometric-calls.log                   （saveCredential/getCredential 全调用记录，24 行）
└── shots/
    ├── 00-vivo-install-confirm.png              （vivo 安装安全确认页）
    ├── 01-login-with-bio-btn.png                （带指纹按钮的登录页）
    ├── 02-login-bio-btn.png                     （登录页指纹按钮）
    └── 03-final-ai-home.png                     （最终 /ai 主界面，会话有效）
```

## 7. 总评

| # | 验证项 | 结果 |
|---|---|---|
| 1 | 后端数据库重置（第一次安装语义） | ✅ PASS |
| 2 | 真机卸载 + 全新安装（firstInstall==lastUpdate） | ✅ PASS |
| 3 | 初始密码登录 + 主密码初始化 | ✅ PASS |
| 4 | 指纹/人脸绑定（saveCredential + BiometricPrompt） | ✅ PASS |
| 5 | 指纹登录（getCredential → login → /ai，会话持续有效） | ✅ PASS |
