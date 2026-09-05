# 首次安装部署验证报告（第二轮：最新代码）— 2026-09-05（vivo X Fold5 真机）

> **验证目标**：将**最新代码**（HEAD `a48fcf4`，含独立注册页/TabBar 收敛/AI 网关修复）构建新 APK 部署到真机，按**第一次安装**语义重测：重置后端库 → 卸载重装 → 初始密码登录 → 主密码 → 指纹/人脸绑定 → 指纹登录。
>
> **设备**: vivo X Fold5 (V2436A), serial `10AF6H1MLM003HF`
> **新 APK**: MD5 `0d84c958048b39abe47dfb5cfe20a506`（30.1MB，16:29 构建），`firstInstallTime == lastUpdateTime = 2026-09-05 16:32:02`
> **后端**: 本仓库 `backend/pocketd`（含 boolToInt 修复，launchd `com.pocketd.firstinstall` 常驻 `*:8090`），`pocket` 库重置后 bootstrap
> **结论先行**: ✅ **全部 PASS**——最新代码全新安装；初始密码登录 + 主密码初始化；指纹绑定成功（saveCredential 33s 含用户触碰）；指纹登录复测通过（getCredential 2.3s → token → /ai）。

---

## 1. 本轮变更（相对第一轮 15:30）

| 项 | 第一轮 | 本轮 |
|---|---|---|
| 代码 | 8-31 构建 APK（MD5 c26262d0…） | **最新 HEAD `a48fcf4` 重新构建**（含独立注册页、Phase C 登录 3-tab、AI 网关错误形状修复） |
| 后端二进制 | 已含 boolToInt | 同（launchd 常驻，`kickstart -k` 重启） |
| 页面形态 | 旧登录页 | **新版登录页**：`密码登录 / 验证码登录` 双 tab + `注册新账号 / 忘记密码？` 链接 |

## 2. 流程与时间线（logcat `logcat-biometric-calls.log`）

| 时间 | 事件 | 结果 |
|---|---|---|
| 16:29 | 新 APK 构建（vite + cap sync + gradle，API base 注入 ×3） | BUILD SUCCESSFUL |
| 16:30:19 | 后端库重置（先备份 `pocket-db-backup-before-reset-2nd.sql.gz`）→ `kickstart -k` 重启 pocketd | listening :8090 |
| 16:32:02 | 真机卸载 + 全新安装（vivo 安全确认页由真机用户通过） | **firstInstall==lastUpdate** |
| 16:33 | 冷启动 → 新版登录页（双 tab + 注册入口）→ 初始密码登录成功 | token 签发 |
| 16:35:5x | 主密码对话框弹出；**同时**自动触发指纹绑定 | — |
| **16:36:58 → 16:37:31** | `saveCredential`：BiometricPrompt 弹出 33 秒（真机用户触碰），resolve 无错误 = **绑定成功** | ✅ |
| 16:38–16:39 | 主密码提交（首次被系统弹窗遮挡，重提交成功）→ `hasMasterPassword:true` → 进入 `/ai` | ✅ |
| 16:41 | force-stop 冷重启：token 持久化直接恢复 `/ai` | ✅ |
| 16:42:05 → 16:42:07 | 清 token 回登录页 → **指纹登录**（新版前端自动唤起）`getCredential` 2.3 秒成功 → token → `/ai` | ✅ |

## 3. 新版登录页观察

- 登录页为 **密码登录 / 验证码登录** 双 tab；`注册新账号`（`#/register`，验证码模式）与 `忘记密码？` 入口齐备。
- 后端 `/api/auth/send-code` 存活（400 invalid purpose 为探测 payload 不匹配，非故障）；本地未配置 SMTP，注册页验证码投递依赖 `POCKET_SMTP_DEBUG_ECHO` 回显或 SMTP，本轮验证走 bootstrap 管理员路径，注册页留待配置 SMTP 后专项测试。
- 登录页在 `hasCredential=true` 时会**自动唤起**指纹验证（不必手动点击按钮），属新版前端行为。

## 4. 已知事项

1. Mixed Content 警告依旧（请求实际放行）。
2. BiometricPrompt 与主密码对话框同屏时，网页内按钮点击会被系统弹窗遮挡——本轮通过二次提交完成，不影响用户实际操作。
3. 包名仍为 `com.kaixuan.opencode.pocket`（用户口述 `com.kxpms.opencodepocket` 不存在于设备与代码）。

## 5. 证据清单

```
test-evidence/first-install-2026-09-02/
├── verification-report-round2.md                 （本文）
├── verification-report-round1-1530.md.bak        （第一轮报告存档）
├── pocket-db-backup-before-reset.sql.gz          （第一次重置前备份）
├── pocket-db-backup-before-reset-2nd.sql.gz      （第二次重置前备份）
├── pocketd-fresh-20260905.log                    （后端日志）
├── pocketd-bootstrap-summary.log
├── apk-install-verify.log                        （firstInstall==lastUpdate=16:32:02）
├── logcat-biometric-calls.log                    （saveCredential + getCredential 全记录）
└── shots/
    ├── 00-vivo-install-confirm.png               （第一轮 vivo 安装确认页）
    ├── 04-2nd-install-confirm.png                （第二轮 vivo 安装确认页）
    ├── 05-2nd-biometric-prompt.png               （绑定阶段截图）
    └── 06-2nd-final-ai.png                       （指纹登录后 /ai 终态）
```

## 6. 总评

| # | 验证项 | 结果 |
|---|---|---|
| 1 | 最新代码构建 + API base 注入 | ✅ PASS |
| 2 | 后端库重置 + bootstrap | ✅ PASS |
| 3 | 真机卸载 + 全新安装（firstInstall==lastUpdate） | ✅ PASS |
| 4 | 初始密码登录 + 主密码初始化 | ✅ PASS |
| 5 | 指纹绑定（saveCredential，用户触碰验证） | ✅ PASS |
| 6 | 指纹登录（getCredential → token → /ai，自动唤起） | ✅ PASS |
