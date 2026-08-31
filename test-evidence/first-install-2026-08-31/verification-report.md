# 首次安装部署验证报告 — 2026-08-31（vivo X Fold5 真机）

> **验证目标**（用户要求）：
> 1. 真机部署验证，且**当作第一次安装**；
> 2. **注册用户并初始化用户的初始密码**；
> 3. **登录可以与指纹或人脸绑定**。
>
> **设备**: vivo X Fold5 (V2436A / PD2436), serial `10AF6H1MLM003HF`, USB 已授权
> **构建**: APK MD5 `c26262d0293f39f1b169108b92470952` (29.85 MB), `firstInstallTime == lastUpdateTime = 2026-08-31 23:16:48`
> **后端**: docker `opencode-pocket-pocketd-local-opp`（pocket 库已重置后全新 bootstrap），0.0.0.0:8090→8088
>
> **结论先行**: ✅ **三项全部 PASS**——全新后端 bootstrap 注册首用户 + 初始密码登录；真机卸载重装（firstInstallTime==lastUpdateTime 证明全新安装）；密码登录成功后自动弹出 BiometricPrompt 完成指纹/人脸绑定（`saveCredential` 成功），随后两次指纹登录均成功（`getCredential` → `/api/auth/login` 200 → 进入应用）。

---

## 1. "第一次安装"语义的落实（双层重置）

### 1.1 后端重置（注册用户 + 初始化初始密码）

本系统无自助注册端点，首用户由 pocketd 在 `users` 表为空时 bootstrap 创建（`backend/cmd/pocketd/main.go:135-146`，用户名/密码来自 `POCKET_AUTH_USER`/`POCKET_AUTH_PASS`）。因此"第一次安装"的后端语义 = 重置 pocket 库 → 重新 bootstrap：

| 步骤 | 命令/结果 |
|---|---|
| 停旧容器 | `docker stop/rm opencode-pocket-pocketd-local-opp` |
| 重置库 | `docker exec kx-citus psql -U kxuser -d llm_gateway -c "DROP DATABASE pocket" -c "CREATE DATABASE pocket OWNER kxuser"` |
| 重启 | 原参数 `docker run`（DSN `host.docker.internal:15433/pocket`，网络 `opp-local-net`） |
| **Bootstrap** | 日志 `Bootstrap: created first admin user "admin"`（见 `pocketd-bootstrap.log`）——**即"注册用户"**，初始密码 = `POCKET_AUTH_PASS`（32 位，**即"初始化用户的初始密码"**） |
| LLM 网关 | commit b1f9943 的默认 seed 在新库首启自动带出：`baseURL=https://llm.kxpms.cn/v1` + 9 偏好模型，`/api/llm-gateway/config` 验证 OK（无需手工恢复） |

### 1.2 真机全新安装

| 步骤 | 结果 |
|---|---|
| `adb uninstall com.kaixuan.opencode.pocket` | Success（vivo 静默不更新缺陷要求 uninstall+install） |
| `adb install app-debug.apk` | Streamed Install Success（vivo 安全确认页由真机用户手动通过，截图 `00-vivo-install-confirm.png`） |
| `dumpsys package` | `firstInstallTime=2026-08-31 23:16:48` == `lastUpdateTime`（见 `apk-install-verify.log`）→ **确为全新安装，无残留数据** |

## 2. 构建链

| 项 | 值 |
|---|---|
| 工作区 | 本 repo `main` @ `6d0e131`（clean） |
| 构建 | `MOBILE_FAST=1 node scripts/build-mobile.mjs android dev`（`.env.android-dev` 本地新建，`VITE_API_BASE=http://192.168.31.37:8090`） |
| API base 注入 | APK 主 chunk grep `192.168.31.37:8090` 命中 3 处 ✅ |
| **修正** | `frontend/android/variables.gradle` compileSdk 35→**36**：`@capacitor/camera@8.2.3` 拉入 `androidx.core:1.17.0`，AAR 元数据要求 API 36+（本机已装 android-36；targetSdk 保持 35，运行时行为不变） |
| gradle | `./gradlew assembleDebug`（proxy 127.0.0.1:7897）BUILD SUCCESSFUL，产物 `app-debug.apk` 29.85MB |
| 生物插件 | `BiometricAuthPlugin` 类确认在 `classes12/13.dex`；`USE_BIOMETRIC` 权限在 Manifest |

## 3. 首次启动全流程（真机实测时间线）

以下时间为设备 logcat 时间戳（`logcat-biometric-calls.log` 为原始证据）：

| 时间 | 事件 | 证据 |
|---|---|---|
| 23:17:36 | 冷启动 → 登录页挂载，`BiometricAuth.hasCredential → {"has":false}`（未绑定，无指纹按钮） | logcat |
| 23:17:45-59 | 真机用户手输密码登录（密码错 → "登录失败：用户名或密码错误"，错误提示正常） | logcat Mixed Content 行 ×3 + CDP |
| ~23:18 | 通过 CDP 填入初始密码登录成功 → `pocket_token/pocket_workspace_id=ws_user-admin` 落库 | CDP localStorage |
| ~23:19 | **主密码初始化**：`MasterPasswordDialog`（"设置主密码：创建用于加密本地数据的主密码"）由真机用户完成，`pocket_crypto_cfg={"hasMasterPassword":true,"passwordHint":"1"}` | 截图 `02-master-password-dialog.png` + CDP |
| **23:19:47→51** | **指纹/人脸绑定**：密码登录成功后自动触发 `saveCredential`，BiometricPrompt 弹出 4.1 秒后成功返回（用户完成生物验证，凭据 admin+初始密码 以 AndroidKeyStore auth-bound AES-GCM 加密存储） | logcat（native→result 4134ms） |
| 23:19:51 后 | `BiometricAuth.hasCredential → {"has":true}`；登录页出现"指纹登录"按钮（`bio-btn`） | CDP |
| ~23:21 | 用户后台切换/重启进程（pid 8098→12651），登录页 onMounted `isAvailable && hasCredential` 双真 → bio-btn 渲染 | logcat |
| **23:22:40→43** | **指纹登录 #1**：点击"指纹登录" → `getCredential` 2.3 秒返回（BiometricPrompt 验证通过）→ 凭据自动调 `/api/auth/login` 200 → 进入应用 | logcat |
| **23:24:45→47** | **指纹登录 #2**：再次完整成功（2.3 秒） | logcat |
| 23:27 | 会话侧 force-stop 冷重启：直接恢复 `/ai-chat`（token+本地加密库持久化，无需重新解锁），历史对话内容完整 | CDP |
| ~23:30 | 会话侧清 token 模拟退出重登：登录表单 + 指纹按钮"登录中..." → 验证通过 → `pocket_token` 重新签发、进入 `/ai-chat` | 截图 `04`+`05` + CDP |

## 4. 指纹/人脸绑定的技术路径（Android 原生）

- **插件**: `frontend/android/.../plugins/BiometricAuthPlugin.java`，`BiometricPrompt` + `Authenticators.BIOMETRIC_WEAK`，`androidx.biometric`。
- **设备能力**: `dumpsys biometric` 显示两个传感器——modality 2（屏下指纹，strength 15/STRONG）+ modality 8（人脸，strength 4095/STRONG）。`BIOMETRIC_WEAK` 同时接受指纹与人脸，**绑定与登录时真机用户可任选其一完成验证**；系统不向应用上报具体用了哪种，故日志无法区分（以用户实际操作为准）。
- **凭据安全**: 绑定 = 生物验证通过后 AES-256-GCM（KeyStore auth-bound 密钥）加密存储登录凭据；登录 = 再次生物验证后解密、仅用于调 `/api/auth/login`；服务端改密后 401 会自动解绑并提示（LoginView `doLogin` fromBiometric 分支）。

## 5. 观察/已知事项（非阻断）

1. **Mixed Content 警告**：WebView 以 `https://localhost` 加载、请求 `http://192.168.31.37:8090`，logcat 出现 "should also be served over HTTPS" 警告，但请求实际放行（登录/业务 API 全通）——与既有记录 `CRITICAL_ISSUE_MIXED_CONTENT_BLOCKER.md` 一致，属已知项。
2. **真机用户手输错误密码时的错误提示**（"登录失败：用户名或密码错误"）表现正常，未泄露具体原因。
3. **冷重启持久化**：token + 本地加密库状态持久，冷启动直接恢复上次路由（`pocket:lastRoute`），无需重新登录/解锁——符合设计（`LoginView` 的 `needUnlock` 仅在 token 存在而 lobster 未就绪时出现）。
4. **主密码由真机用户自行设置**（提示词 "1"），验证侧未记录其值（安全上正确）；初始登录密码仍为 bootstrap env 的 32 位值。
5. compileSdk 36 修正是本轮唯一代码改动（`variables.gradle`），已随证据一并提交。

## 6. 证据清单

```
test-evidence/first-install-2026-08-31/
├── verification-report.md                    （本文）
├── apk-install-verify.log                    （firstInstallTime==lastUpdateTime=23:16:48）
├── pocketd-bootstrap.log                     （Bootstrap: created first admin user "admin"）
├── logcat-biometric-calls.log               （saveCredential 23:19:47→51；getCredential 23:22:40→43 / 23:24:45→47）
├── llm-gateway-configs-backup-before-reset.sql.txt  （重置前库内配置备份）
└── shots/
    ├── 00-vivo-install-confirm.png           （vivo 安装安全确认页）
    ├── 02-master-password-dialog.png         （主密码初始化对话框）
    ├── 04-biometric-login-inprogress.png     （指纹登录按钮"登录中..."，验证进行时）
    └── 05-biometric-login-success.png        （指纹登录成功后 /ai-chat 恢复）
```

## 7. 总评

| # | 验证项 | 结果 |
|---|---|---|
| 1 | 第一次安装（后端库重置 + bootstrap 注册首用户 + 初始密码） | ✅ PASS |
| 2 | 第一次安装（真机 uninstall + 全新 install，firstInstall==lastUpdate） | ✅ PASS |
| 3 | 初始密码登录 + 主密码（初始加密密码）初始化 | ✅ PASS |
| 4 | 登录与指纹/人脸绑定（密码登录后自动 saveCredential + BiometricPrompt） | ✅ PASS |
| 5 | 指纹/人脸登录（getCredential → login 200 → 进应用，×2 次） | ✅ PASS |
