# Runbook — pocketd + Android AI Gateway E2E (RAG Study)

**Date**: 2026-09-03
**Author**: ZCode (autonomous, handoff-driven)
**Branch**: `main`
**Scope**: end-to-end smoke test of the `opencode-pocket` mobile app → `pocketd` backend → configured AI gateway, with full runbook capture so the next run can be reproduced or audited.

> **User intent (verbatim, original)**: *"请在模拟器中启动应用，并配置好AI网关，然后进行AI的响应的测试，找到问题的原因，然后再修正。注意过程要落成文档，便于更新修正。"* — Start the app in the emulator, configure the AI gateway, exercise the AI response path, identify and fix the cause, and document the process so it can be updated/corrected later.

This document is intentionally **idempotent**: re-running the steps from the top on a fresh checkout should reproduce the green state recorded in §10.

---

## 1. Background & system map

```
┌─────────────────────────┐   HTTPS (mobile TLS termination on device)
│  Android emulator app   │   (opencode-pocket Android client)
│  (com.zag.pocket / …)   │
└──────────┬──────────────┘
           │  REST + WebSocket
           ▼
┌─────────────────────────┐
│  pocketd (Go backend)   │   port see §3; serves /api/* incl. /api/llm-gateway/config
│  module: backend/      │   Go 1.22+, main = backend/cmd/pocketd
└──────────┬──────────────┘
           │  HTTPS / streaming
           ▼
┌─────────────────────────┐
│  AI Gateway             │   URL/key injected at startup or via .env
│  (zagent-gateway / …)   │
└─────────────────────────┘
```

Two endpoints on the mobile side are critical for AI testing:

| Verb | Path | Auth | Purpose |
|------|------|------|---------|
| `GET` | `/api/llm-gateway/config` | `requireAuth` | App **pulls** gateway URL + key on boot / settings refresh |
| `POST` | `/api/llm-gateway/config` | `requireAuth` | App / admin pushes a new gateway configuration |

---

## 2. Prerequisites

- macOS (Apple Silicon assumed) with Android SDK + emulator available (`adb` on `PATH`)
- Go ≥ 1.22 installed (`go version`)
- Node-free build: pocketd is pure Go
- `backend/.env` populated (template: `backend/.env.example`)
- Optional: `jq`, `curl` for diagnostics
- Existing APK installed on emulator (or built per §6)

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
```

---

## 3. Inspect configuration before starting

These three files are the source of truth for ports, gateway URL, and bootstrap.

| File | Purpose |
|------|---------|
| `backend/Makefile` | Build / run targets, dev helpers |
| `backend/start-dev.sh` | Dev entrypoint — picks port, loads `.env`, runs `pocketd` |
| `backend/.env` | Real config: `LISTEN_ADDR`, `LLM_GATEWAY_URL`, `LLM_GATEWAY_API_KEY`, DB conn, etc. |
| `backend/.env.example` | Template — never edit directly |
| `.env` (repo root) | Often a thin shim or docker shim; **separate from** `backend/.env` |

**Canonical port convention** (recorded from prior runs in this repo):

- `pocketd` listens on the port declared in `backend/.env` → `LISTEN_ADDR` (often `:8081` in dev, sometimes `:8090`). Always read it from the file before assuming.

**Canonical gateway config keys** (names may vary slightly per .env version, look for these patterns):

- `LLM_GATEWAY_URL` — full upstream gateway URL, e.g. `https://gateway.internal/v1`
- `LLM_GATEWAY_API_KEY` — bearer token sent upstream
- `LLM_GATEWAY_MODEL` (optional) — default model id to expose
- `LLM_GATEWAY_TIMEOUT` (optional) — request timeout in seconds

---

## 4. Stop stale processes

A stale binary from a previous (different) repo (`ai-native-tools/llm-gateway/...`) was masquerading as `/tmp/pocketd-test` (PID 83027). It must be killed before any local smoke test, otherwise the port stays held and the real backend can't bind.

```bash
# 1. List anything bound to the expected pocketd port
lsof -nP -iTCP:<LISTEN_PORT> -sTCP:LISTEN

# 2. Find the stale masquerade
ps -axo pid,etime,command | grep -E 'pocketd-test|/tmp/pocketd' | grep -v grep

# 3. Kill the stale binary (replace PID with whatever shows up)
kill <STALE_PID>

# 4. Sanity: nothing should be left bound
lsof -nP -iTCP:<LISTEN_PORT> -sTCP:LISTEN | wc -l   # expect 0
```

> If nothing is stale, this section is a no-op — record "no stale processes" in §10.

---

## 5. Build pocketd

Module root is `backend/` (contains `go.mod`). Either reuse the prebuilt binary or rebuild cleanly.

### Option A — reuse prebuilt (fast)

```bash
ls -la backend/pocketd
# Expect: ~32MB, modification time within the last few days
```

If `backend/pocketd` is younger than `backend/cmd/pocketd/main.go`, it's safe to reuse:

```bash
file backend/pocketd   # confirm Mach-O 64-bit executable, arm64 or amd64
```

### Option B — rebuild (authoritative)

```bash
cd backend
go build -o pocketd ./cmd/pocketd
ls -la pocketd
cd ..
```

> If `go build` fails with `cannot find main module`, you forgot the `cd backend` — `go.mod` lives in `backend/`.

---

## 6. Start pocketd

Preferred path (idempotent):

```bash
cd backend
./start-dev.sh   # reads .env, sets up DB, exec's pocketd
```

Foreground fallback (when you need raw logs in your shell):

```bash
cd backend
set -a; source ./.env; set +a
./pocketd 2>&1 | tee /tmp/pocketd.$(date +%Y%m%d-%H%M%S).log
```

Wait for the boot banner; success looks like:

```
pocketd listening on <addr>
healthz: OK
migrations applied: N
```

Verify the two AI-gateway endpoints are registered:

```bash
curl -sS http://127.0.0.1:<PORT>/healthz
# Expect: {"status":"ok", ...}

# Both must 401 without auth — that's the baseline correctness check.
curl -sS -o /dev/null -w 'GET  %{http_code}\n'  http://127.0.0.1:<PORT>/api/llm-gateway/config
curl -sS -o /dev/null -w 'POST %{http_code}\n' -X POST http://127.0.0.1:<PORT>/api/llm-gateway/config
# Expect: 401 on both
```

---

## 7. Boot Android emulator & install app

```bash
# Start emulator
emulator -avd <AVD_NAME> -no-snapshot-load &
adb wait-for-device

# Confirm device
adb devices
adb shell getprop ro.build.version.release

# Install / reinstall app
adb install -r <path-to-apk>      # or: backend's built debug APK
adb shell pm list packages | grep -i pocket
```

---

## 8. Configure AI gateway in-app

Two flows are supported:

**Flow A — push from server (admin / dev override)**
```bash
TOKEN=<bearer-from-app-login>
curl -sS -X POST http://127.0.0.1:<PORT>/api/llm-gateway/config \
     -H "Authorization: Bearer $TOKEN" \
     -H 'Content-Type: application/json' \
     -d '{
           "url":"https://<gateway-host>/v1",
           "apiKey":"<key>",
           "model":"<model-id>"
         }'
# Expect: 200, body echoes the saved config
```

**Flow B — app pulls from server (normal user path)**

Open the app, complete login, navigate to **Settings → AI Gateway**.
The app issues `GET /api/llm-gateway/config` and pre-fills the form.

Verify both flows round-trip the same payload:

```bash
curl -sS http://127.0.0.1:<PORT>/api/llm-gateway/config \
     -H "Authorization: Bearer $TOKEN" | jq .
```

---

## 9. Exercise the AI request path

Open the in-app chat. Send a single small prompt first (cheaper to fail fast):

```
ping
```

Watch pocketd logs (`/tmp/pocketd.*.log`) and emulator logcat:

```bash
adb logcat -v time | grep -iE 'pocket|llm|gateway'
```

A green run looks like:

- App: response streams back within a few seconds
- pocketd log: outbound POST to `<gateway>/v1/chat/completions` (or equivalent) → 200 → token stream
- No 5xx, no TLS errors, no `LLM_GATEWAY_API_KEY not set` warnings

If anything is red, jump to §11.

---

## 10. Capture evidence

Save into `test-evidence/2026-09-03-pocketd-llm-ragstudy/`:

```bash
EVD=/Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/test-evidence/2026-09-03-pocketd-llm-ragstudy
mkdir -p "$EVD"

# pocketd log
cp /tmp/pocketd.*.log "$EVD/pocketd.log"

# emulator screenshot — login + AI config + chat with reply
adb exec-out screencap -p > "$EVD/01-login.png"
adb exec-out screencap -p > "$EVD/02-ai-config.png"
adb exec-out screencap -p > "$EVD/03-chat-reply.png"

# gateway config round-trip
curl -sS http://127.0.0.1:<PORT>/api/llm-gateway/config \
     -H "Authorization: Bearer $TOKEN" \
   | jq . > "$EVD/gateway-config.json"

# health probe
curl -sS http://127.0.0.1:<PORT>/healthz | jq . > "$EVD/healthz.json"

# brief commit hash + status for traceability
git rev-parse HEAD > "$EVD/git-head.txt"
git status --short > "$EVD/git-status.txt"
```

### Acceptance checklist

- [ ] `pocketd` boots without panics, `/healthz` returns 200
- [ ] `GET /api/llm-gateway/config` and `POST /api/llm-gateway/config` both 401 without auth, 200 with auth
- [ ] AI gateway URL/key round-trips through the in-app config form
- [ ] A real chat prompt returns a streamed/non-empty answer
- [ ] No 5xx, no key-leak in `pocketd.log`, no `failed to dial gateway` errors
- [ ] All artifacts saved under the dated evidence directory

---

## 11. Common failures & fixes

### 11.1 `cannot find main module`
`go build` was run outside `backend/`. Fix: `cd backend && go build -o pocketd ./cmd/pocketd`.

### 11.2 Port already in use
A stale `pocketd` (or the masquerading `/tmp/pocketd-test` from a sibling repo) is still bound. Run §4 to identify and kill it; do **not** SIGKILL without confirming — sometimes the real pocketd is the process you want.

### 11.3 `LLM_GATEWAY_API_KEY not set` at chat time
`backend/.env` was not loaded. Either re-source it (`set -a; source ./.env; set +a`) before launching pocketd, or fix `start-dev.sh` to source it explicitly. Verify with `strings backend/pocketd | grep -c LLM_GATEWAY` — the constant must exist.

### 11.4 App cannot reach pocketd from emulator
Use `10.0.2.2` (emulator alias for host loopback), **not** `127.0.0.1`. Either:
- Bake `10.0.2.2:<PORT>` into the app's default base URL, or
- Run `adb reverse tcp:<PORT> tcp:<PORT>` so `localhost` on the device tunnels to the host.

### 11.5 401 on `/api/llm-gateway/config` after login
`requireAuth` middleware order changed or the token is being attached to the wrong header. Inspect the request the app sends (Charles/mitm or `adb logcat | grep OkHttp`). Compare with a known-good request from §10 of `test-evidence/2026-09-02-pocketd-smoke/runbook.md` if available.

### 11.6 AI request times out
Gateway URL is correct but unreachable from this network. `curl -v https://<gateway>/v1/models` from the host first; if that times out too, the gateway itself is down — not a pocketd bug.

---

## 12. Audit, then submit

Before opening the PR:

```bash
git status --short
git diff --stat
```

- Re-run the acceptance checklist from §10 once more on the final tree.
- If anything changed in `backend/cmd/pocketd` or `backend/internal/...`, add a regression test under `backend/*_test.go`.
- Title the PR: `test(ai-gateway): end-to-end smoke + runbook (2026-09-03)`.
- Body must link back to this runbook file path so reviewers can replay it.

> Reminder: don't discard other branches' work — rebase on `origin/main` only if it doesn't conflict; otherwise merge.

---

## 13. Where this runbook lives

This file: `test-evidence/2026-09-03-pocketd-llm-ragstudy/runbook.md`
Adjacent evidence: see §10 capture list.
Related historical runbooks in the same `test-evidence/` tree:
- `2026-09-02-pocketd-smoke/` — prior smoke
- `2026-09-02-pocketd-aigate/` — prior AI-gateway integration attempt
- `2026-09-02-ai-gateway-*/` — multiple iterations of the same target
---

## 14. 2026-09-05 会话补充：全链路实测、根因定位与修复（已验证）

> 本节为 2026-09-05 凌晨会话的增量记录。当日完成：真机（模拟器）E2E 实测 →
> 用 CDP 在 WebView 内复现 → 4 个代码修复 → 重建回归全绿。所有密钥均已脱敏。

### 14.1 当日环境快照

| 项 | 值 |
|---|---|
| 后端 | `go build -o pocketd ./cmd/pocketd`（backend/ 模块），`:8088` |
| 启动方式 | `backend/start-dev.sh`（已修复，见 14.3-fix4） |
| 网关注入 | 根目录 `.env` 的 `POCKET_LLM_GATEWAY_URL` / `POCKET_LLM_GATEWAY_API_KEY`（脚本读取，不回显） |
| 模拟器 | AVD `Medium_Phone_API_36.1`（`pocket_test` AVD 缺系统镜像不可用），`emulator-5554` |
| App 构建 | `node scripts/build-mobile.mjs android dev`（mode=android-dev → `VITE_API_BASE=http://10.0.2.2:8088`）+ `./gradlew assembleDebug` |
| 登录 | dev 兼容分支：`admin` / `Veritrans&9527`（`POCKET_DEV_AUTH=true`，userStore 为空时走 `server_assistant.go:120` 路径 2） |

### 14.2 实测发现的根因（按证据强度排序）

1. **前端把后端错误帧当“坏帧”吞掉**（`frontend/src/api/llm-bff.ts`）。
   后端流式错误帧形如 `{"error":"...","delta":{"done":true,"finish_reason":"error"}}`；
   旧代码 `if (delta.error) throw` 抛进同一 try，被“单帧解析失败不中断流”的 catch
   吃掉 → UI 空气泡 + 永久转圈。CDP 探针（WebView 内执行）证实读取循环从未收到
   `onError`/`onDone`。
2. **auto 回退链死寂期无上限**（`backend/internal/server/llmbff_provider_adapters.go`）。
   `model=auto` 时网关上游经常性慢失败/挂起；回退链逐候选串行重试（客户端
   ResponseHeaderTimeout 30s × N 个候选），期间 SSE **零字节**，实测 7.3s~35s+
   全部帧在链结束瞬间一次性到达（curl 逐行时间戳 + WebView 内探针双重复现）。
3. **Vue 3 响应式旁路：流式增量写原始对象**（`frontend/src/features/ai-chat/aiChatStore.ts`）。
   `spawnStream`/`optimize` 把 `assistant`（原始引用）push 进响应式数组后，闭包里继续
   `assistant.content += …`——绕过代理不触发模板更新：流式期间气泡永远空白，
   杀 App 重启后才从持久化里“出现”（旧会话截图即此现象）。
4. **dev 启动脚本登录冒烟发空密码**（`backend/start-dev.sh`）：从不导出
   `POCKET_AUTH_PASS`，curl 用空密码登录，脚本自身却报“登录测试通过”与否取决于
   服务端缺省；同时不注入网关变量 → `/api/llm/*` 首测 503/不可用。
5. **chatagent SQLite 旧库缺列**（启动日志现场复现）：
   `no such column: marketplace_id` → chat agent store 不初始化 → `/api/chat-agents` 503
   （App 内“选择角色”不可用）。根因：`Init` 只跑 `CREATE TABLE IF NOT EXISTS`，
   Phase 4 新列对旧库不生效，且 marketplace 部分索引在补列前创建会先失败。
6. **网关密文解密失败静默跳过**：JWT secret 派生密钥变化后，
   `llm_gateway_configs`/SQLite `gateway_config` 里的旧密文永远解不开，每次启动
   WARN×3 后回退内建默认值，毒化行永远无法自愈。

### 14.3 修复清单（全部已落地）

| # | 文件 | 修复 |
|---|---|---|
| fix1 | `frontend/src/api/llm-bff.ts` | 错误帧改走 `onError`（不再抛进解析 catch）；空流（无任何 delta/usage）报 `模型未返回内容（空流）`；120s 流级看门狗，超时触发 `onError` 保证 UI 必有终态 |
| fix2 | `backend/internal/server/llmbff_provider_adapters.go` | auto 回退链改为循环 + `context.WithTimeout`：单次尝试 20s、整链预算 45s，超时返回原错误（经 fix1 在 UI 可见） |
| fix3 | `frontend/src/features/ai-chat/aiChatStore.ts` | `spawnStream`/`optimize` 增量写入改走 `conv.messages[len-1]` 代理引用（`liveAssistant`），流式实时渲染 |
| fix4 | `backend/start-dev.sh` | 导出 `POCKET_AUTH_USER/PASS`（缺省 admin/Veritrans&9527）；从根 `.env` 注入网关变量（不回显） |
| fix5 | `backend/internal/chatagent/sqlite_store.go` | `Init` 三段式：建表 → PRAGMA table_info 幂等补 5 列（marketplace_id/skill_refs/publisher/version/tags）→ 建索引；新增回归测试 `TestSQLiteStore_LegacyTableMigration`（模拟 11 列旧库）；顺带修复 emoji/color NULL 扫描（COALESCE，`store.go` PG 侧同步修复） |
| fix6 | `backend/internal/server/llm_gateway_handler.go` | `EnsureLLMGatewayDefaults`/`LoadLLMGatewayFromDB` 加载失败时用 env 默认配置**覆写毒化行**（自愈），实测启动日志从 `decrypt ... failed ×3` 变为 `self-healing ... loaded config from DB` |
| fix7 | `backend/.env` | 删除末尾重复的 `POCKET_HTTP_PORT=9088`（与 12 行 8088 冲突的端口噪音源） |

### 14.4 修复后回归结果（全部通过）

- `go test ./...`：44 个包 ok；仅 PG 集成测试因本地无 PostgreSQL 失败（预置问题，
  与本次改动无关，见 14.6）。
- `vue-tsc --noEmit`：通过。
- 后端冒烟：`/healthz` 200；`/api/auth/login` 200；`/api/llm-gateway/config` 200（掩码 key）；
  `/api/llm/chat` 200（glm-5.2）；`/api/llm/stream` 200（SSE + usage）；`/api/chat-agents` 200（277 内置角色）。
- **真机（模拟器）E2E**（截图见 `screenshots/14~25`）：
  - 登录 → 主密码创建 → 进入 AI 工具页；
  - 快速提问 → 对话页 auto 模型发送 → 回复实时渲染（"E2E-OK"、"≈303 tokens"）；
  - 错误路径：上游挂死时显示 `context deadline exceeded` 并正常停止（不再无限转圈）；
  - 后端日志对应 `[SLOW] POST /api/llm/stream - 200 (3.5~4.5s)`。
- WebView 内 CDP 探针（决定性隔离实验）：`login 200`；显式 `model=glm-5.2` 流式
  1.9s 收到 `PONG`+`[DONE]` → WebView fetch/reader/SSE 解析链路本身健康，
  排除混合内容（`allowMixedContent: true` 生效，控制台 Mixed Content 仅为警告）与 CORS 因素。

### 14.5 调试工具箱（本会话沉淀）

- **WebView CDP 探针**：debug 构建 WebView 可远程调试。
  `adb forward tcp:9222 localabstract:webview_devtools_remote_<pid>`，
  `curl localhost:9222/json` 取 WS URL；本仓库留有最小 Python WS 客户端模板
  （见会话记录 `/tmp/cdp_probe.py`，未入库）。可直接在页面内登录拿新 token、
  执行与 App 相同的 fetch+解析逻辑，逐帧打时间戳——是隔离“前端解析 vs 网络层 vs 上游”的最快手段。
- **JWT 有效期约 15 分钟**：过期后 App 内所有请求 401。回归测试记得先重新登录。
- `adb exec-out screencap -p > file.png` 截图；`input text` 不支持中文（non-ASCII 崩溃），
  中文输入需 ADBKeyboard 或绕过（本次用英文提示词）。

### 14.6 遗留问题（未在本次修复）

> 2026-09-05 第二轮收尾已处理其中 4 项，状态标注见各条；新增遗留移至 §15.5。

1. ~~`internal/chatagent/store_test.go` 硬编码 PG DSN~~ **已解决**：该文件现读
   `POCKET_TEST_POSTGRES_DSN`（未设即 Skip）；本轮再补「Ping 2s 失败即 Skip」
   （`pgxpool.New` 惰性建连，DSN 设了但 PG 不可达要到 Ping 才能发现）。
2. ~~`/api/llm/chat` 仍走启动时静态 `s.llm`~~ **已解决**：见 §15.1 任务 1。
3. JWT 默认 15min 过期 + 无刷新机制 → **评估完成，结论见 §15.2**（推荐滑动
   续期；代码未动，等产品拍板）。
4. 网关上游单候选慢失败时无进度提示 → **已解决**：见 §15.1 任务 4（retry 帧）。
5. `pocket_test` AVD 缺 arm64 系统镜像（avdmanager 列表报 Missing system image）；
   当前用 `Medium_Phone_API_36.1` 替代。**未变**。

---

## 15. 2026-09-05 会话补充（二）：AI 网关移动端收尾

> 本轮按 §14.6 优先级推进遗留问题。任务 1/2/4 已落地并全量回归绿；
> 任务 3 仅交付评估结论（产品决策项，代码未动）。所有密钥均掩码。

### 15.1 完成清单

| # | 文件 | 改动 |
|---|---|---|
| 1 | `backend/internal/server/server_assistant.go` | `handleLLMChat` 迁移动态网关：`llmBFF` 已装配时转投 `llmChatViaBFF`（新增，`llmbff.Service.Chat` + `dynamicGatewayBFFProvider` 按 workspace 实时解析 `/api/llm-gateway/config`；空/"auto" model 由 preferred 列表解析，不再 400）；`s.llm` 仅在 BFF 未装配的部署兜底。**消除「启动时未配 POCKET_LLM_API_KEY → 恒 503」旧分支**。响应形状不变 `{"content","model"}`。新增回归测试 `TestLLMChatViaBFFWorksWithoutStaticLLM`（s.llm=nil 必 200）、`TestLLMChatUnconfiguredGatewayReturns503`（网关未配置 503 指向配置入口） |
| 1 | `backend/internal/server/server_llmbff.go` | 头注释更新：/api/llm/chat 已迁移；/api/embed 仍走静态 s.embedder 待迁 |
| 2 | `backend/internal/chatagent/store_test.go` | `setupTestStore` 补 `pool.Ping`（2s 超时）失败即 `t.Skip`——`pgxpool.New` 惰性建连，DSN 设了但 PG 不可达原会在 `Init` 阶段 Fatalf |
| 4 | `backend/internal/llmbff/service.go` | `Delta` 新增 `Retry string \`json:"retry,omitempty"\``（回退重试进度帧：无 content、非终态，客户端继续读流） |
| 4 | `backend/internal/server/llmbff_provider_adapters.go` | `Stream` 回退循环：选中 fallback 后先发 `fn(Delta{Retry: fallback})`（客户端断开则终止），再以新候选重试；超时预算不变。新增测试 `TestDynamicGatewayStreamEmitsRetryProgressFrame`（fake 上游首帧 no_candidate → 断言 Retry 帧 → 二次成功） |
| 4 | `frontend/src/api/llm-bff.ts` | 解析循环识别 `retry` 帧 → 新可选回调 `onRetry(model)`；不算坏帧/不触发 onError/不计入 sawDelta；旧调用方不传回调时静默兼容 |
| 4 | `frontend/src/features/ai-chat/aiChatStore.ts` | `spawnStream`/`optimize` 接 `onRetry`，把「上游模型不可用，已切换到 <model> 重试…」写入 live assistant 代理的 `retryHint`（沿用 fix3 代理引用写法） |
| 4 | `frontend/src/features/ai-chat/AIChatView.vue` | 气泡内渲染 `retryHint`（`.msg-retry` 12px 灰字，与 `.msg-error` 同风格不告警），正文到达后保留一行小字 |
| 补 | `backend/start-dev.sh` | 导出 `POCKET_AUTH_LEGACY_ONLY`（缺省 true，可覆盖）——349a14e 加固后 legacy 路径需显式开关，否则脚本启动即 Fatal 要求 RedClaw URL。**上一轮 start-dev.sh 修复不完整，本轮实测复现并补齐** |

### 15.2 JWT 15min 过期评估结论（产品决策项，代码未动）

现状事实：
- 生产 token 由 RedClaw 签发、pocketd 透传，15min 可配（`docs/AUTH_REDCLAW.md`）；
  本地 legacy signer TTL 硬编码 24h（`cmd/pocketd/main.go`），无 env。
- 349a14e 基线：Parse 只收 HS256 + 显式 iss/aud + 30s leeway，拒弱密钥；
  短 TTL 是撤销窗口的补偿——本地签发 token 不受 RedClaw 撤销覆盖。
- 前端 401 无全局拦截（`frontend/src/api/http.ts` 抛 ApiError 即止），token 存
  localStorage；离线队列 401 直接死信。
- 可复用：生物识别免密重登可换新 token；RedClaw 会话模型自带「凭当前 token 重签」。

三方案对比：a) 经典 refresh token——RedClaw 明确不用，需 RedClaw 侧改造 + 移动端
长期凭据，泄露面扩大，与加固方向冲突，**否**；b) 延长 TTL——生产 token 是 RedClaw
签的，改 pocketd 无效，且拉长撤销窗口，**否**（仅 legacy 应急）；c) **滑动续期
（推荐）**——pocketd 新增 `POST /api/auth/refresh`：requireAuth 验当前 token →
`Me` 复检撤销状态 → 复用 `issueTokenAndRespond` 重签短 TTL。每枚 token 仍短命、
撤销检查保留，与 349a14e 完全兼容。

落地最小清单（待拍板后实施）：`server_auth_extended.go` 新增 handleAuthRefresh；
`server.go` 注册路由；`frontend/src/api/auth.ts` 加 refresh()；`stores/auth.ts`
解析 exp、剩余 <5min 主动续期；`http.ts` 401 单飞 refresh 一次重放，失败才登出。

### 15.3 本轮验证记录

- `go test ./...` 全绿（含新增 3 个测试）；`vue-tsc --noEmit` 通过；
  `node scripts/build-mobile.mjs android dev` 构建通过（retry 提示可进 APK）。
- 后端冒烟（迁移后二进制，start-dev.sh :8088）：
  - `/api/auth/login` 200；`/api/llm-gateway/config` 200；
  - `/api/llm/chat` model=glm-5.2 → `{"content":"PONG","model":"glm-5.2"}`（动态 BFF 路径）；
  - `/api/llm/chat` model=auto → 自动解析为 preferred 首选 glm-5.2 → 200；
  - `/api/llm/stream` model=auto → SSE 正常（content 帧 + done/usage 帧 + `[DONE]`）。
- 说明：retry 进度帧的真机 E2E 需可构造的 no_candidate 场景 + 重建 APK，见 §15.5。

### 15.4 安全卫生备注（未改动，建议后续处理）

`backend/internal/server/server_auth_extended_test.go:36` 硬编码了含密码样式的
PG DSN（凭据此处不引用）。虽然仅测试文件，建议改为读 env（同 chatagent 模式）
并轮换该凭据。

### 15.5 遗留（下一轮续接）

1. retry 进度帧真机 E2E：重建 APK（本轮已验证 build-mobile 通过，未装模拟器）；
   需构造稳定 no_candidate 场景（如把 preferredModels 首位设为不存在的 model）。
2. `server_meeting.go` 会议摘要仍直用静态 `s.llm`（2 处）+ `/api/embed` 走静态
   `s.embedder`——同一模式待迁移（本轮范围外）。
3. JWT 滑动续期（§15.2）待产品拍板后实施。
4. §15.4 凭据轮换 + 测试 DSN env 化。

---

## 16. 2026-09-05 会话补充（三）：retry 帧真机 E2E + JWT 滑动续期 + 动态网关全量迁移

> 本轮收口 §15.5 全部 4 项遗留。任务中途发现并修复一个回退链真实缺口
> （invalid_model 不触发回退，见 16.2）。所有证据脱敏，密钥零引用。

### 16.1 完成清单

| # | 文件 | 改动 |
|---|---|---|
| 1 | `backend/internal/server/llmbff_provider_adapters.go` | **根因修复**：新增 `isModelUnavailableError`（no_candidate ∪ invalid_model），Chat/Stream 回退链改用它。上游对「preferred 首位是不存在模型」回的是 HTTP 400 `invalid_model` 而非 503 `no_candidate`，旧逻辑整链直接报错、retry 帧永不触发（E2E 首测复现，见 16.2）。新增 `TestIsModelUnavailableError`、`TestDynamicGatewayStreamFallsBackOnInvalidModel` |
| 2 | `backend/internal/server/server_meeting.go` | 摘要/润色 2 处静态 `s.llm.Chat` 迁移：新增统一入口 `llmChatOnce`（`server_assistant.go`；llmBFF 装配时走动态网关 kind=meeting，否则静态兜底），两 handler 的 `s.llm==nil` 503 守卫改为 `s.llmBFF==nil && s.llm==nil` |
| 3 | `backend/internal/server/server_assistant.go` | `handleEmbed` 迁移动态网关：BFF 装配时走 `llmBFF.Embed`（kind=embed，Provider 侧 dynamicGateway.Embed 早已就绪），静态 `s.embedder` 兜底；响应形状不变 `{"embedding","model","dim"}`。守卫顺序调整：先方法/入参校验再分支 |
| 4 | `backend/internal/server/server_auth_extended.go` + `server.go` | **JWT 滑动续期落地**（§15.2 方案 c，本轮拍板实施）：`POST /api/auth/refresh`（requireAuth）——claims 取身份 → RedClaw 模式经 `Me` 复检撤销状态（fail-closed）→ `issueTokenAndRespond` 重签短 TTL；响应形状与 login 一致 |
| 5 | `frontend/src/api/auth.ts` | 新增 `refreshAuth()` |
| 6 | `frontend/src/stores/auth.ts` | `tokenExpiresAtMs()`（解析 JWT exp）+ `refreshSession()`（模块级单飞 promise，并发调用共享）+ `maybeRefresh()`（剩余 <5min 主动续期）；显示名不因 refresh 漂移（legacy 端点只能从 claims 反推 user 字段） |
| 7 | `frontend/src/api/http.ts` | `http` = 请求前 `maybeRefresh` + 401 单飞 refresh 一次重放；refresh 端点自身 401 不重放（防自引用）；refresh 失败 401 原样透传 |
| 8 | `backend/internal/server/server_auth_extended_test.go` | **安全卫生（§15.4）**：`testDSN()` 删除历史硬编码回退 DSN（该凭据已入 git 历史，必须轮换），未设 `POCKET_TEST_POSTGRES_DSN` 时返回空串、`mustTestPool` Skip。实测无 DSN 时 C8 系列全部干净 SKIP |
| 9 | `backend/internal/server/server_llmbff.go` 等 | 头注释同步（/api/embed、meeting 已迁移） |

新增测试文件：`server_gateway_migration_test.go`（embed BFF 无静态 embedder 必 200、meeting summary 走 BFF、refresh 换新 token 且新 token 可过 requireAuth、垃圾 token 401）。

### 16.2 E2E 中途发现的根因：invalid_model 不触发回退（已修复）

按 §15.5 构造场景（POST /api/llm-gateway/config 把 preferredModels 首位设为
`no-such-model-e2e`）后，curl 实测 `/api/llm/stream` **直接返回错误帧**：
`llm-gateway stream 400: {"error":{"code":"invalid_model",...}}`——上一轮
设计的回退链只识别 503 `no_candidate`，而真实上游对未知模型 id 回 400
`invalid_model`。修复后同场景帧序列（`test-evidence/2026-09-05-retry-frame-e2e/sse-frames.txt`）：

```
data: {"done":false,"retry":"glm-5.2"}     ← 回退重试进度帧
data: {"content":"E2E-RETRY-E","done":false}
data: {"content":"VIDENCE","done":false}
data: {"done":true,"finish_reason":"stop"}
data: {"done":true,"usage":{...}}
data: [DONE]
```

### 16.3 真机（模拟器）E2E 验证（全部通过）

环境：同 §14.1（Medium_Phone_API_36.1 / emulator-5554；**模拟器 SystemUI
持续 ANR，quick boot 快照损坏，改 `-no-snapshot` 冷启动后恢复**）。

- 场景：preferredModels 首位 = 不存在模型，App 内 auto 模型连发两条英文消息；
- **气泡内灰字提示**：「上游模型不可用，已切换到 glm-5.2 重试…」（12px 灰字）
  与回退成功正文同框渲染（`bubble-retry-hint-first.png` / `bubbles-retry-hint-final.png`，
  两条均复现，≈115/177 tokens）；
- 后端日志：`[SLOW] POST /api/llm/stream - 200 (2.5~2.8s)`；
- `POST /api/auth/refresh` 线上冒烟 200（legacy 模式，换新 token 成功）；
- `POST /api/embed` 迁移路径冒烟：请求正确经动态网关达上游，上游 503
  `no_provider`（网关无 embedding 供应商；企业模式下旧静态 embedder 同样走
  该网关，行为对齐非回退）。

### 16.4 验证记录

- `go test ./...` 46 包 ok（新增 6 个测试全绿）；`vue-tsc --noEmit` 通过；
- `node scripts/build-mobile.mjs android dev` + `gradlew assembleDebug` 通过并装模拟器；
- 网关配置已恢复原状（preferredModels 首位复原 glm-5.2）。

### 16.5 安全卫生与提醒

- §15.4 已落地：测试 DSN 硬编码回退删除（改 env + Skip）。**该 DSN 凭据已进
  git 历史（本文件不引用），轮换需与提交者确认后执行**——本轮未轮换，仅提醒。
- 新增证据与代码零密钥引用（已 grep 校验）。

### 16.6 遗留（下一轮续接）

1. "say" 消息（burst 尾帧）出现过「用户气泡已入列表但 stream 请求未发出」的
   一次观察（复现 0/1，后一条消息正常）；若再遇可查输入框 Enter/发送双通道竞态。
2. 离线队列重放路径（非 spawnStream 调用方）未接 `onRetry`——队列重放的消息
   在回退期间无灰字提示（静默兼容，可低优先级补齐）。
3. 模拟器 quick boot 快照已损坏：下次 E2E 直接 `-no-snapshot` 冷启动省时。
4. embed 上游无 provider：`/api/embed` 链路健康但网关侧需配 embedding 供应商
   才能真正可用（部署项，非代码项）。

---

## 17. 2026-09-05 会话补充（四）：目标核对 + 收尾杂项清零

> 本轮续接提示词基于 §14.6 列出 4 项目标，但其中全部 4 项已在会话二/三
> （445517e、5ca02cf）完成并推送。本轮先独立核实该结论，再清掉 §16.6 中
> 唯一可落地的代码遗留（优化按钮的回退提示）。零密钥引用。

### 17.1 目标核对（防"提交信息声明 ≠ 代码落地"）

对 HEAD 5ca02cf 独立复核，4 项目标全部确认已落地：

| 目标（原提示词） | 实际状态 | 核实方式 |
|---|---|---|
| 1. /api/llm/chat 迁移动态网关 | ✅ §15.1 已完成 | `llmChatViaBFF`（server_assistant.go:2280）在位；实测 503 错误带 `gateway_debug` 即动态路径特征 |
| 2. chatagent PG 测试连通性跳过 | ✅ §15.1 已完成 | `setupTestStore` 含 `pool.Ping` 2s 超时 Skip；本轮 `go test ./...` 无 DSN 环境全绿 |
| 3. JWT 15min 无刷新评估 | ✅ 超出预期：不止评估，方案 c（滑动续期）已实施（§16.1 #4~7） | `handleAuthRefresh`（server_auth_extended.go:378）在位；实测 `POST /api/auth/refresh` 200 |
| 4. SSE retry 进度帧 | ✅ §15.1/§16.1 已完成含真机 E2E | `Delta.Retry`、`isModelUnavailableError`、前端 `onRetry`/`retryHint` 全在位 |

本轮独立验证：`go build` + `go test ./...` 全绿（44+ 包）；`vue-tsc --noEmit` 通过。

### 17.2 本轮新改动：优化按钮补回退提示（§16.6 #2 收口）

排查澄清：§16.6 #2 所述"离线队列重放路径"实为 outbox（`src/native/outboxDrain.ts`），
其重放 `/api/mobile/sessions/:id/prompt`（opencode 会话路径），**不经过 /api/llm/stream**，
retry 帧对其无意义；真正的未接 `onRetry` 调用方是：

| 文件 | 处理 |
|---|---|
| `frontend/src/composables/usePromptOptimizer.ts` | **已接**：新增 `optimizeRetryHint` ref（开始/done/error/abort 时清空）+ `onRetry` 回调透传 |
| `frontend/src/components/business/UnifiedComposer.vue` | **已接**：两处工具栏（常规/全屏）复用现有 `.uc-hint` 样式渲染 `上游模型不可用，已切换到 <model> 重试…`，与「转写中…」同行同风格 |
| `frontend/src/features/meetings/meetings-ai.ts` | **本轮未接**：`summarizeMeeting` 当时无调用方（孤儿函数），无 UI 呈现位。注：本轮收尾时观察到并行会话在同目录为其接入 onRetry（未提交），后续以其提交为准 |

验证：`vue-tsc --noEmit` 通过。

### 17.3 本轮冒烟记录（运行中的 pocketd :8088，即 5ca02cf 代码）

- `POST /api/auth/login` 200；`GET /api/llm-gateway/config` 200（key 掩码 sk-6**）；
- `POST /api/llm/chat` model=kimi-k3 → 200 `{"content":"pong 🏓...","model":"kimi-k3"}`（动态网关路径）；
- `POST /api/llm/stream` model=kimi-k3 → SSE 正常（content 帧 + stop + usage + `[DONE]`）；
- `POST /api/auth/refresh` 200；
- **环境观察（非回归）**：model=glm-5.2 / minimax-m3 当前上游返回
  `model_not_found: No available provider ... All N candidates failed`——上游网关
  侧无可用 provider（与 §16.3 embed `no_provider` 同类，部署项）。链路本身健康：
  请求正确到达网关、错误结构化返回。kimi-k3 可用即证。

### 17.4 遗留（下一轮续接，更新版）

1. §16.6 #1 "say" burst 竞态：复现 0/1，维持"再遇再查"。
2. `summarizeMeeting` 孤儿函数：无调用方；会议纪要 UI 落地时接 `onRetry` 并接入页面。
3. 上游网关 glm-5.2/minimax-m3 无可用 provider：部署侧为网关配置 provider（本仓无代码可改）。
4. 模拟器 quick boot 快照损坏 → E2E 用 `-no-snapshot` 冷启动（§16.6 #3 沿用）。
5. embed 上游 provider 配置（§16.6 #4 沿用，部署项）。

### 17.5 并行会话（五）：§16.6 #1 发送竞态审计 + 防御落地 + 双通道 E2E 回归

> 与会话四同一时间窗并行工作，代码改动经会话四 735f686 收编提交（消息已注明）。
> 本节补记审计结论与真机验证证据。零密钥。

**代码审计结论（§16.6 #1「气泡入列表但 stream 未发出」，复现 0/1）**：

发送双通道实为同一入口：Enter（`UnifiedComposer.onKeydown`）与外部发送按钮
（AIChatView `composerRef?.submit()`）都走 `onSubmit()` → `emit('submit')` →
`onComposerSubmit` → `store.send()`。JS 单线程同步链路下第二发会被
`canSubmit`（草稿已 reset）或 `isStreaming` 挡住，双触发本身不产生异常态。
唯一能造成「用户气泡已 push、流却未发起」的是 **send() 中段同步异常悬空态**
（userMsg push 之后、spawnStream 完成之前任何同步 throw：气泡留下、流没有、
且 `composerRef.reset()` 不执行——草稿残留，用户重发即「后一条消息正常」，与
§16.6 #1 的观察形态完全吻合）。

**防御修复**（经 735f686 提交）：
- `aiChatStore.send`：spawnStream 段 try/catch——异常时回滚 userMsg + toast
  「发送失败」+ console.error，保证「气泡入列表」与「流已发起」原子性；
- `UnifiedComposer.onKeydown`：过滤 `e.repeat`（长按/Gboard 语音听写收尾合成
  的重复 Enter），双通道竞态的标准防御。

**真机 E2E 回归**（emulator-5554，`test-evidence/2026-09-05-send-race/`）：
- 输入 `race-check-alphal` 后**快速连点发送按钮两次**（模拟双通道双触发）：
  仅一条用户气泡 + 一条流（`06-double-tap.png`），草稿正常清空，无重复消息；
- 流以「context deadline exceeded」终态结束（`07-stream-done.png`）——上游
  glm-5.2 当前无可用 provider（§17.3 同因，环境问题非回归），而错误终态本身
  证明流已发出；悬空态未再复现；
- 前置回归：vue-tsc、ai-chat 契约测试、go test ./... 46 包全绿。

**环境观察**：`logs/backend-dev.log` 在 pocketd（PID 5930）存活期间被外部
截断（mtime 停 05:57、fd 写入偏移超前于文件尾），06:00 后日志观测失效。本轮
未重启服务（避免干扰并行会话），下轮 `bash start-dev.sh` 重启即恢复。

### 17.6 DSN 凭据轮换执行清单（§16.5，待提交者 halfking 确认后执行）

状态：**未轮换**（按规约需先与 halfking 确认；本轮未联系上，仅固化清单）。
凭据样式的 PG DSN 曾硬编码于 `server_auth_extended_test.go`（5ca02cf 已删除
回退、改 `POCKET_TEST_POSTGRES_DSN` env + Skip），但**仍存在于 git 历史**。
halfking 确认后执行：

1. 在 PG 侧为该测试库创建新口令（或直接废止涉事角色）；
2. 更新本地/CI 的 `POCKET_TEST_POSTGRES_DSN` 注入源（不进仓库）；
3. `git grep` 复核 HEAD 与新增文档零引用（本文件历轮均只写「已泄露」事实、
   不引用值，可保持）；
4. 因历史提交含旧凭据，确认废止旧口令即可视为闭环（不做历史重写——
   会改写并行协作的提交链，风险大于收益）。

## 18. 2026-09-05 会话补充（五）：移动端收尾执行记录

### 18.1 DSN/provider 协作边界

- 已在仓库 GitHub Issue #14 联系提交者 `halfking`，请求以非敏感方式确认：
  历史测试 DSN 角色轮换/废止、`POCKET_TEST_POSTGRES_DSN` 注入源更新、
  `glm-5.2`/`minimax-m3` provider 配置，以及 embedding provider 配置。
- 本地未持有可执行的 PG 管理凭据，因此没有猜测、生成或回显新 DSN；在提交者
  确认前，凭据轮换和上游 provider 配置保持待办状态。
- HEAD 与新增代码中未发现历史完整 DSN；本轮 grep 仅命中已脱敏的示例、CI
  占位和环境变量名。旧完整值仍只存在于历史提交，按清单废止旧口令即可闭环，
  不进行历史重写。

### 18.2 start-dev.sh 根因与修复

- 根目录执行 `bash backend/start-dev.sh` 首次失败：脚本先切换到 `backend/`，
  再用相对 `$0` 计算 `backend/..`，路径解析依赖当前工作目录。
- 修复为基于 `${BASH_SOURCE[0]}` 求脚本绝对目录，并复用 `SCRIPT_DIR` 读取根
  `.env`。修复后从仓库根目录执行成功：网关变量已注入（仅显示“已注入”）、
  pocketd 健康检查通过、dev 登录冒烟通过。
- `bash -n backend/start-dev.sh` 通过。服务运行中的 `logs/backend-dev.log`
  已重新生成启动记录；日志文件由当前进程持有，空闲期间大小不变，健康请求和
  启动输出均可落盘，未发现本轮新增密钥泄漏。

### 18.3 会议纪要 UI 落地

- `frontend/src/features/meetings/meetings-ai.ts` 的 `summarizeMeeting` 增加可选
  `onRetry` 回调，保留旧调用兼容性，并把回退模型提示透传给页面。
- `frontend/src/features/meetings/MeetingDetailView.vue` 新增“生成纪要/重新生成纪要”
  操作：将分段转写按说话人拼接后调用 `summarizeMeeting`，结果通过
  `updateMeeting(..., { summary })` 写回本地并 reload；无转写时禁用按钮，并展示
  加载、空状态、失败和模型回退提示。
- 新 APK 已构建、安装并启动到 `emulator-5554`；截图归档为
  `test-evidence/2026-09-05-meeting-summary-ui.png`。当前现场为 AI 工具首页，
  说明应用启动链路正常；尚未有可用历史会议数据，未执行生成纪要点击回归。

### 18.4 本轮验证

- `go test ./...`：通过（后端全部包；无本地 PG DSN 时集成测试按既有约定 Skip）。
- `frontend/` 内 `npm run typecheck`：通过。
- `node frontend/scripts/build-mobile.mjs android dev`：通过。
- `frontend/android/` `./gradlew assembleDebug`：通过，APK 已安装并启动。
- `curl http://127.0.0.1:8088/healthz`：返回 `ok`。
- `git diff --check`：通过。
- 环境观察仍未改变：`glm-5.2`/`minimax-m3` 上游返回 `model_not_found`，
  `/api/embed` 上游返回 `no_provider`；这两项需要提交者/网关运维侧配置，
  本仓没有凭据或 provider 控制面可安全代办。

## 19. 2026-09-05 会话补充（六）：会议纪要 UI 提交核对 + 类型回归闭环

### 19.1 远端同步与并行改动核对

- `git fetch origin --prune` 后核对：本地 `main` HEAD 与 `origin/main` 一致
  （均为 ef7f072），无分叉、无待拉取提交。
- 上一轮并行会议纪要 UI 改动核对：`frontend/src/features/meetings/MeetingDetailView.vue`
  与 `meetings-ai.ts` 均已随 ef7f072 正式提交，两文件工作区无未提交差异。
  按约定只读核对，未收编、未编辑。
- 结论：会议纪要 UI 已正式立项落地（ef7f072 即“接入生成入口”），
  `summarizeMeeting` 不再是孤儿函数——已经 `onSummarize` 接入生成/重新生成
  入口，`onRetry` 回退提示与结果写回（`updateMeeting`）均在提交版本内。

### 19.2 vue-tsc 失败点闭环（以提交后版本为准）

- 前一轮记录的 `vue-tsc` 唯一失败（`MeetingDetailView` 模板引用
  `summarizing`/`onSummarize`）经核对已随 ef7f072 提交自愈：`summarizing`、
  `summaryError` ref 与 `onSummarize` 函数在提交版本中均已定义，模板与脚本
  一致，本轮无需再改代码。
- 本轮对上述两文件零编辑，回归全部基于 ef7f072 提交版本执行。

### 19.3 本轮验证

- `frontend/` 内 `npx vue-tsc --noEmit`：通过（退出码 0，无错误输出）。
- `npm run build`（vue-tsc + vite build）：通过（2.67s；仅既有 chunk >500kB
  警告，非错误）。
- `curl http://127.0.0.1:8088/healthz`：返回 `ok`，运行中栈仍健康。上一轮
  全链路冒烟（health/login/refresh/kimi-k3 chat+SSE）结论不变，本轮未重复。
- `go test ./...` 上一轮已全绿，本轮无后端代码改动，未重复执行。

### 19.4 外部协调待办（未变，仍阻塞在本仓之外）

- 请网关维护方恢复 `glm-5.2`/`minimax-m3` provider（当前上游 `model_not_found`）。
- 请配置 embedding provider，配置后复验 `/api/embed`（当前上游 `no_provider`）。
- `POCKET_TEST_POSTGRES_DSN` 轮换：仅在 halfking 于 Issue #14 明确确认后按
  §17.6 清单执行；本轮未触碰任何凭据，工作区遗留的后端适配器改动仍保持
  未提交状态，未纳入本次提交。

## 20. 2026-09-05 会话补充（七）：流式回退超时修复与回归

### 20.1 改动范围

- `backend/internal/server/llmbff_provider_adapters.go`：动态网关 `Stream` 的
  auto 回退链增加单次尝试超时与整链预算；无正文且遇到尝试级 deadline 时允许切换
  preferred 候选，已输出正文后不重复作答；保留 retry 进度帧与结构化日志。
- `backend/internal/server/llmbff_provider_adapters_test.go`：新增两组测试，覆盖
  “首候选流式挂死后超时回退成功”与“正文已输出后超时不得回退”。测试将超时预算
  缩短到 100ms，不依赖真实上游或凭据。
- 另发现并修正一处测试辅助日志 goroutine 生命周期问题：请求成功时传入的
  `context.Background()` 不会结束，原实现会永久等待并造成 goroutine 泄漏；已移除
  该非必要 goroutine，不影响回退链日志或业务语义。

### 20.2 回归记录

- `gofmt -w`：通过；`git diff --check`：通过。
- `go test ./internal/server -run 'TestDynamicGatewayStream|TestIsModelUnavailableError' -count=1`：通过。
- `go test ./...`：通过，所有后端包通过或按既有约定无测试文件。
- 本轮尝试认证读取 `/api/llm-gateway/config` 与复验 `/api/embed`：dev 登录返回
  `invalid credentials`，未获得 token，因此未绕过认证、未猜测密码，也未回显任何凭据。
  provider 恢复与 embedding provider 配置仍属于网关维护侧待办。

### 20.3 提交边界

- 仅纳入上述两个后端源文件、对应测试与本 runbook 记录；`logs/backend-dev.log`
  及其他截图/运行产物保持未提交，不收编。


---

## 21. 2026-09-05 会话补充（八）：auto 回退链三重修复 E2E 收口 + JWT 滑动续期真机验证

> 本节与会话七（§20/764f323）在同一工作区交错进行：764f323 收编的正是本轮
> 工作区里未提交的 deadline 回退 WIP（§20.1「移除诊断 goroutine」即本轮为
> 排查 40s 截断临时加的探针）；本节 7886fcd 在其上叠加剩余三层修复并完成
> 全链路 E2E。零密钥引用。

### 21.1 上游探测结论（任务②定论）

- `glm-5.2`/`minimax-m3`：`POST /api/llm/chat` 均 `no_candidates`（0 candidates，
  ~600ms 快速失败）——**仍未恢复**，preferredModels 首选回归不做，维持部署侧
  观察项（§17.3/§18.4/§19.4 同源）。
- 关键不对称（本轮新发现）：chat 路径快速失败可回退，**stream 路径对无
  provider 的模型会挂死到 20s 尝试超时**——这是 auto 模式整链崩死的入口。
- `kimi-k3` 可用但首 token 时延波动大：直连 SSE 实测 5~12s，App 内双链并发
  时曾 >20s。

### 21.2 三重修复（764f323 + 7886fcd 合并视角）

| # | 缺陷 | 修复 | 文件 |
|---|---|---|---|
| 1 | 尝试级 20s deadline 不触发回退（不属于 isModelUnavailableError），auto 整链死在首位挂死候选 | `streamAttemptFallbackEligible`：deadline 且本次尝试未写出正文 → 视同「候选无货」回退；已写出正文后超时不重试（防同气泡重复作答） | llmbff_provider_adapters.go（764f323） |
| 2 | 回退链成环：fallbackModel 只排除 current，实测 glm-5.2 → minimax-m3 → **glm-5.2**，kimi-k3 永远轮不到 | `nextFallbackModel(wsID, tried)` 以已试集合按 preferred 顺序取下一候选 | llmbff_provider_adapters.go（7886fcd） |
| 3 | **SSE 30s WriteTimeout 豁免静默失效**：loggingMiddleware 的 responseWriter 包装器缺 `Unwrap()`，`http.ResponseController` 穿不透 → longLivedPathMiddleware 的 `SetWriteDeadline(time.Time{})` 失败被 `_` 丢弃 → 30s 写死线仍在。实测：20s 首帧正常送达，40s（30s 后首次写）第二帧写失败、请求 ctx 被 canceled、连接掐断——45s/60s 预算的回退链永远走不完。§14.4/§16.3 的 E2E 全部短于 30s，故此前未暴露 | responseWriter 补 `Unwrap()`；/api/mobile/sessions 等同享 | middleware.go（7886fcd） |
| 4 | 整链预算 45s：两个挂死候选耗 40s 后最终候选仅剩 5s，实测 kimi-k3 来不及出首 token | 60s（2×20s 挂死 + 最终候选完整 20s 窗口；retry 帧使等待可见，非 §14.2 零字节转圈） | llmbff_provider_adapters.go（7886fcd） |
| 5 | 回退行为不可观测 | `[llm-auto]` 日志：每次切换（model、错误、tried）与终止（原因、budget_left） | llmbff_provider_adapters.go（764f323） |

新增测试：`TestDynamicGatewayStreamFallsBackOnAttemptDeadline`、
`TestDynamicGatewayStreamNoFallbackAfterContent`（764f323）、
`TestDynamicGatewayStreamChainVisitsEachCandidateOnce`（7886fcd，三候选链
不重访断言）。超时预算 var 化，测试内缩到 100ms。

### 21.3 E2E 验证（运行中 :8088 + emulator-5554，-no-snapshot 冷启动沿用）

- **curl 全成功路径**：`/api/llm/stream` model=auto → T+20s retry 帧
  `minimax-m3` → T+40s retry 帧 `kimi-k3` → T+45s 正文 `CHAIN-FIXED-OK` +
  usage + `[DONE]`（45.0s，200）。上游双死场景 auto 模型恢复可用。
- **App 内**（`test-evidence/2026-09-05-send-race/08~11`）：auto 连发两条，
  每次回退跳变时气泡内灰字「上游模型不可用，已切换到 kimi-k3 重试…」实时
  渲染（08 捕获跳变瞬间、09/11 捕获终态）；当 kimi-k3 首 token 也超 20s 时
  以红色 `context deadline exceeded` 干净终态（无转圈、无悬空气泡）——
  终态正确性本身即为 §16.6 #1 所需的「流已发出」证据。
- 结论：回退链行为与 UI 提示全部符合设计；剩余失败均为上游时延/无
  provider（部署侧），非代码缺陷。

### 21.4 JWT 滑动续期真机验证（§16.1 #4~7 的运行时收口）

CDP 探针（WebView 页面上下文，§14.5 工具箱）：

- 存量 token（legacy 24h TTL，余 1121min）→ `POST /api/auth/refresh` 200，
  新 token 余 1440min（滑动满额）；
- `maybeRefresh` 门槛语义：余 >5min 时主动续期不触发（`maybeRefreshWouldSkip:
  true`），不会无谓刷新；
- 新 token 写回 `localStorage.pocket_token` 后，页面内带新 token 调
  `/api/llm-gateway/config` → 200，会话无缝延续。
- 流式路径的临期预刷新（7485b8a 的 streamChat maybeRefresh + 401 重放）随
  本轮全部 App 内发送隐式回归：预刷新对 24h TTL 为 no-op，不破坏任何流。
- 说明：`<5min 触发`的真实临期场景需短 TTL 部署（legacy TTL 硬编码 24h），
  运行时无法自然复现；门槛逻辑已由上述探测 + 代码路径覆盖，留待生产
  RedClaw 15min token 环境天然验证。

### 21.5 验证记录

- `go build ./...` + `go test ./...` 全绿（46 包；含新增 3 测试）。
- `npx vue-tsc --noEmit` 通过（§19.2 后无前端改动，本节再次确认）。
- 后端冒烟：login/refresh/llm-gateway config 200；kimi-k3 直连 SSE 正常。

### 21.6 遗留（下一轮续接，更新版）

1. 上游 provider（glm-5.2/minimax-m3/embedding）与 DSN 轮换：仍阻塞在网关
   维护侧/提交者确认（Issue #14），本仓无代码可改。
2. kimi-k3 首 token 时延波动（5~12s，偶发 >20s）：若上游恢复后仍慢，可考虑
   「最终候选不设尝试超时（仅整链预算兜底）」或把尝试超时改为参数化配置。
   当前 60s 预算在「2 死候选 + 1 慢可用候选」场景偏紧，属调优项非缺陷。
3. §16.6 #1 悬空态：防御已落地（7485b8a/§17.5），双通道 E2E 复现 0/2，
   维持「再遇再查」。
4. `/api/embed` 无 provider 同 §16.6 #4。

---

## 22. 2026-09-05 会话补充（九）：Issue #14 复核 + 显式模型三通道存档 + 会议纪要生成真机 E2E + embed 降级验证

> 与 §19~§21 会话并行窗口内的收尾验证轮。工作面互补不重叠：§20/§21 完成
> 流式回退链修复与其 E2E（764f323、7ecf9ed 已推送）；本轮聚焦 ①Issue #14
> 状态复核 ②grep 脱敏复核 ③显式 model 请求的三通道 curl 存档 ④**会议纪要
> 生成的真机 E2E**（§18.3 只落了 UI，生成链路首次真机回归）⑤**embed 应用
> 内降级路径**验证。全程零密钥，响应体存档经 `sk-*`/Bearer 掩码。

### 22.1 Issue #14 状态复核（结论：服务侧动作仍未发生）

- API 复查评论区：**0 条评论**；时间线仅一次 referenced（ef7f072 提交信息
  引用）。halfking 未回告任何"已完成"非敏感标识。
- 因此 §17.6 清单执行前提（halfking 确认）未满足：PG 侧轮换未发生；
  `POCKET_TEST_POSTGRES_DSN` 本地/环境均未设置（符合"轮换后才注入"的既定
  顺序，无新值可注入）；本轮不伪造运维结果。
- 07:33~07:45 实测（§22.3）与 halfking 发 issue 时（2026-09-04）的三个
  上游缺口完全一致，交叉印证网关侧 provider 配置仍未发生。

### 22.2 git grep 脱敏复核结论（工作树全量）

- 本轮新增产物（证据目录、runbook 本节、截图）零密钥。
- 存量命中分类（均非本轮引入）：
  - `internal/opencode/config_writer.go` `DefaultLLMGatewayAPIKey`：b1f9943
    **有意提交**的租户共享 dev/seed key（注释自述生产上线后换 KMS）。注意
    其值与当前 llm.kxpms.cn 线上网关 key 同族（§17.3 掩码 sk-6\*\*），即
    **共享租户 key 长期有效且已入库**——建议 halfking 在 Issue #14 的 DSN
    轮换中一并评估该 key 的轮换/边界。
  - `start-with-postgres.sh`、`third_party/identity-go/README.md`：本地
    dev 样例 DSN（localhost 明文样例口令），低风险，维持现状。
  - 测试 fixture 假值（`user:pass@localhost`、`p4ssw0rd` 类）：不计。
- 历史完整 DSN（§15.4/§16.5）确认仅存在于 git 历史，工作树零引用。

### 22.3 显式 model 三通道验证（curl 存档 `test-evidence/2026-09-05-gateway-wrapup/`）

运行实例：pocketd PID 38324（07:20:08 启动，含 764f323 的 deadline 回退，
不含 7ecf9ed 的防成环/Unwrap/60s 预算——如实注明代码版本对应关系）。

| 通道 | 请求 | 结果 | 存档 |
|---|---|---|---|
| /api/llm/chat | kimi-k3 | **200** `{"content":"pong","model":"kimi-k3"}` | chat-kimi-k3.txt |
| /api/llm/chat | glm-5.2 | **502**（内层上游 503 `model_not_found`/`no_candidates`） | chat-glm-52.txt |
| /api/llm/chat | minimax-m3 | **502**（同上） | chat-minimax-m3.txt |
| /api/embed | text 32B | **502**（内层 503 `no_provider`） | embed-basic.txt |
| /api/llm/stream | kimi-k3 | **200** 6s/16 帧/`finish_reason:stop` | stream-kimi-k3.sse.txt |
| /api/llm/stream | glm-5.2 | **200** 45s：glm 挂 20s → `retry:"kimi-k3"` → `retry:"minimax-m3"` → 规整错误终态 `context deadline exceeded`+`finish_reason:error` | stream-glm-52.sse.txt |
| /api/llm/stream | auto | 38s 连接被掐（curl 18）——按 §21.2 #3 即 SSE 30s WriteTimeout 豁免失效（该实例未含 7ecf9ed），与本表其他帧序互为修复前后对照 | stream-auto.sse.txt |

结论：链路健康（错误结构化、回退带进度帧、终态必达）；三个上游缺口
（glm-5.2/minimax-m3/embedding）与 Issue #14 请求时一致。显式 model 的
外层 502 是 pocketd 对上游失败的包装，内层保留网关原始错误码。

### 22.4 真机 E2E：会议纪要生成（首次生成链路回归）+ 种子数据新方法

环境：emulator-5554，ef7f072 debug APK（§18.3 已装）。App 冷启动停主密码
解锁屏，dev 主密码与 start-dev.sh 默认一致（值不引用），解锁正常。

**种子方法沉淀（§14.5 CDP 探针的进阶）**：会议转写依赖 STT（Groq 未配、
模拟器无 STT），录制链路无法产段。本轮经 CDP `Runtime.evaluate` 在页面
上下文直接调用 `CapacitorSQLite` 原生桥：`createConnection({mode:'secret'})`
+ `open()` **无需知道主密码**——插件 open() 自动从自身 secure store 取
SQLCipher PRAGMA key（App 首启 setEncryptionSecret 已存）。三个坑：
原生桥无 `isConnection`（JS 包装层方法）；批量 execute 首条后静默中止
（schema.ts splitSqlStatements 注释同款坑），必须 JS 循环逐条执行；
programmatic `el.click()` 对部分 Vue 组件链不可靠，用 CDP
`Input.dispatchMouseEvent`（受信任点击）+ `getBoundingClientRect` 定位。
探针脚本归档于证据目录 `tools/`（cdp_eval.py / cdp_click.py / 种子 SQL）。

生成纪要两轮实测（MeetingDetailView，截图 04~11）：
- 生成中态：按钮「纪要生成中…」禁用样式 ✅；
- **retry 提示态**：纪要区「上游模型不可用，已切换到 kimi-k3 重试…」——
  meetings-ai onRetry → 页面回退提示**真机复现**（07/09，与转写同框）✅；
- 终态：两轮均 `context deadline exceeded` 红字终态、按钮恢复、无无限
  转圈 ✅。原因：auto 首选 glm-5.2 挂死 + kimi-k3 对长 prompt（纪要
  system+全文）在 20s 尝试窗内不回（§21.1 记录的时延波动；短 prompt
  独立请求 6s 正常），旧 45s 预算耗尽。§21.2 #4 的 60s 预算 + provider
  恢复后，成功态（retry 提示+纪要正文同框）留待复现，种子/脚本已备。

### 22.5 真机 E2E：ai-chat 成功态 + retry 灰字同框；embed 降级

- ai-chat 快速提问 `reply with one word pong` → auto 链回退后 kimi-k3
  回复 `pong`（≈349 tokens，13/14 截图）；同屏历史气泡（§21.3 的测试
  消息）完整渲染**灰色 retry 提示 + 错误终态**——UI 提示在真实气泡流
  再证 ✅。
- embed 应用内降级：Notes 新建笔记保存成功（15 截图，hash 跳详情页）；
  异步 `embedAndStore` 得 no_provider 被 `.catch` 静默捕获
  （notes-store.ts:99 设计行为）；页面侧验证 `local_note_vectors` 计数
  0、笔记本体完好、App 无崩溃——增强功能降级不阻断主流程 ✅
  （§21.6 #4 的应用内侧收口；上游 provider 仍待配置）。

### 22.6 环境观察

- logs/backend-dev.log fd 外部截断再现（§17.5 同类）：PID 38324 存活期间
  07:30 后写入丢失（mtime 停 07:07），本轮为不干扰并行会话未重启；
  服务侧观测以 curl 存档为准。
- kimi-k3 时延双态：短 prompt 6s 正常、长 prompt（纪要）20s 窗内不回，
  与 §21.1「首 token 5~12s 波动、双链并发 >20s」一致。

### 22.7 遗留（下一轮续接，增量更新 §21.6）

1. Issue #14 三项（DSN 轮换+注入源、glm/minimax provider、embedding
   provider）：仍阻塞在 halfking/网关维护侧；**新增建议**：顺带评估
   §22.2 的共享租户 gateway key 轮换。
2. 纪要生成成功态真机留证：依赖 provider 恢复 + 60s 预算实例重启；
   种子方法与脚本已备（§22.4）。
3. 其余沿用 §21.6（kimi 时延调优项、悬空态维持观察）。

---

## 23. 2026-09-05 会话补充（十）：任务审计（双轴自审）+ 终态帧防重复作答整改

> 应收口要求对 §19~§22 任务做整体审计。触发 session-audit-gate 技能后按其
> 规则确认：仓库无 `.acc-session-policy`（未 opt-in），依技能自身规定降级为
> 轻量双轴自审（Standards + Spec），降级原因记录于此，不静默。

### 23.1 特性需求清单（整理）

| # | 需求 | 状态 |
|---|---|---|
| 1 | 移动端 AI 网关收尾：流式可用、超时兜底、回退提示可见 | 已落地（7485b8a/764f323/7ecf9ed，§21.3 E2E） |
| 2 | auto 回退链：挂死候选不阻塞整链、候选不重访（防成环） | 已落地+测试（764f323/7ecf9ed） |
| 3 | 已作答后不换候选重试（防同气泡重复作答） | 本轮补齐终态帧边界（§23.4） |
| 4 | 会议纪要生成：`summarizeMeeting` 接入 UI + onRetry 提示 | 已落地（ef7f072），真机 E2E（§22.4） |
| 5 | 前端类型/构建回归基线 | vue-tsc + vite build 全绿（§19.3/§21.5） |
| 6 | 上游 provider 恢复（glm-5.2/minimax-m3）+ embedding provider | 阻塞在网关维护侧（§21.1/§22.1，Issue #14 无回复） |
| 7 | `POCKET_TEST_POSTGRES_DSN` 轮换 | gated on halfking 确认（§17.6/§22.1） |

### 23.2 Standards 轴（规范/异味/机械检查）

- `go vet ./...` 通过；两个改动文件 gofmt 干净；`git diff --check` 通过。
- 定向测试 `-race` 通过（7 项，含本轮新增）；`go test ./...` exit 0（46 包，无 FAIL）。
- 已提交区间密钥扫描无命中；`.env` 未被 git 跟踪（仅 `.env.example`）。
- 历史遗留（非本任务，不收编、不改）：
  `internal/server/scheduled_task_integration_test.go`、`server_assistant.go`
  gofmt 未格式化，留后续卫生轮处理。

### 23.3 Spec 轴（忠于需求/无越界）

- 提交信息声明 ↔ 代码落地逐项核对一致（§17.1 方法论沿用）：
  764f323/7ecf9ed/182648e/12d7f3a 内容与 runbook 记载吻合。
- 与并行会话（§21/§22）交错期间零编辑冲突：本轮整改仅触及 attemptFn 判定、
  终止日志字段与 helper 签名，未触碰 7ecf9ed 的 tried/nextFallbackModel
  逻辑；测试插入点独立、无重名。
- §20.2 记载的 dev 登录 `invalid credentials` 为瞬态环境观察：§21.5 同窗
  并行会话 login/refresh/config 200，结论以后者为准，本轮不重复验证。

### 23.4 发现并整改

- 【已修复·代码】终态帧后挂死的重复作答边界：SSE 解析在 finish/usage 帧后
  仍会等 `[DONE]`（llmgateway/stream.go 仅遇 `[DONE]` break），原判定只统计
  正文帧——此时尝试级超时会误判「未作答」→ 换候选在同一气泡重复作答；
  §21.2 #3 修复 SSE 30s 写死线后长流更易落入该窗口。整改为 `answered`
  （正文或终态帧均计入），日志字段同步 `answered=`，新增回归
  `TestDynamicGatewayStreamNoFallbackAfterDoneFrame`（终态帧后挂死：仅 1 次
  上游调用、无 retry 帧、错误原样上抛）。
- 【记录·不改】历史遗留 gofmt 两文件（见 §23.2）。

### 23.5 判定

- Standards 轴：通过；Spec 轴：通过（整改后全量测试复跑 exit 0）。
- GO：允许提交并推送。

---

## 24. 2026-09-05 会话补充（十一）：provider 恢复确认 + preferredModels 首选回归 + 会议纪要成功态真机 E2E（§22.4 留待态首次达成）+ kimi-k3 时延量化（任务②定论）

> 接手时 HEAD=12d7f3a；本轮工作窗内并行会话连续落地 5212806（终态帧整改）/
> 08ff3e6（登出清理）/9a74236（最终候选免尝试超时 + 预算 90s）。本轮全部
> E2E 跑在 14:48 构建的 **12d7f3a 实例**（PID 80722→86426）上，与 9a74236
> 无版本耦合，如实注明。证据：`test-evidence/2026-09-05-budget60-restart/`。

### 24.1 上游恢复确认与首选回归（任务①收口）

- 时间线：14:47 三通道探测仍全灭（§22.3 同态）；14:55 起 glm-5.2 `200`(4.1s)/
  minimax-m3 `200`(10.0s)——Issue #14 发出约一天后网关侧动作发生；embed 仍
  `503 no_provider`（三缺口中至今唯一未恢复项）。
- **preferredModels 首选回归**：chat auto → `{"content":"pong","model":"glm-5.2"}`
  4.3s；stream auto → 3.9s 直达、零 retry 帧；恢复后 0 条新 `[llm-auto]`
  （首选命中不触碰回退链）。
- 60s 预算对照（恢复前 14:50 窗口的唯一实测）：stream 显式 glm 短 prompt →
  T+20s retry minimax → T+40s retry kimi → T+60s 规整错误终态，http=200
  全程存活——**7ecf9ed Unwrap 修复的实例级对照验证**（§22.3 旧实例 30s 即被
  WriteTimeout 掐断）；[llm-auto] 三行（tried 防成环 + `budget_left=0s`
  终止）因日志 fd 事后被外部截断，自会话转录重建存档（README 注明）。

### 24.2 kimi-k3 时延量化与任务②定论

- 采样（/api/llm/stream 显式 kimi-k3）：短 prompt ×6 TTFT **4.5~15.2s**
  （中位 ~12.7s，4/6 >10s，较 §21.1 的 5~12s 整体劣化）；长 prompt ×2
  TTFT **27.1/32.9s**（2/2 超 20s 尝试窗）。
- 决策推演（与 9a74236 提交理由收敛）：60s 预算下两死候选耗 40s 后最终候选
  窗口恰为剩余 20s、与尝试窗重合——「最终候选免尝试超时」单改在最坏场景
  零增益；解锁纪要长文须与预算放宽组合。
- **定论：不改代码（本轮）**。9a74236（15:04）已落地「最终候选免尝试超时 +
  预算 90s」：最终候选窗 50s ≥ 实测长文上限 33s，取值与上述量化吻合；
  预算/尝试窗均为 var，上游再度劣化时可调。

### 24.3 真机 E2E：会议纪要成功态（§22.7 #2 收口）+ ai-chat 直达成功

- 冷启动（force-stop+重启，解锁屏主密码解锁）→ CDP SQLite secret 桥种子
  会议。§22.4 三坑再现：原生桥无 `isConnection`、批量 execute 须逐条、
  受信任点击；另发现归档种子脚本头部为 `--` SQL 注释须剥离后执行。
- **gen#1（15:05）**：灰字「已切换到 kimi-k3 重试…」与纪要正文**同框**——
  §22.4 留待复现的成功态首次达成。glm 在真实纪要长 prompt 下超 20s 尝试窗
  触发回退，kimi 在 60s 预算内收尾（持久化 summary 411B，内容忠实种子转写）。
  注：当时实例为 60s/20s 版本，kimi 能收尾含运气成分（minimax 疑似快速失败
  或 kimi 该次 TTFT <20s）；9a74236 的 50s 最终窗将此场景从「碰巧成功」变为
  「确定性成功」。
- **gen#2**：glm 直达成功、无 retry 提示——恢复后首选路径应用内确认；两轮
  合计覆盖「回退链成功」与「首选直达」双路径。
- ai-chat：`PREFERRED-DIRECT-OK` 直达成功、无灰字；历史气泡保留恢复前
  「retry + deadline 错误终态」同框对照。
- embed：API 层仍 503（存档）；应用内降级 §22.5 已收口，不重复验证。

### 24.4 环境观察（外部干预加重，档案自愈）

- 本轮证据目录 15:04→15:07 间被外部整体删除（疑似并行会话 git clean 类
  操作），全部档案自会话转录/重跑重建，README 逐份注明来源。
- logs/backend-dev.log 截断后 fd 写入丢失（15:05 起停 1034B，[llm-auto]
  新行不落盘）——§22.6 同类加重；收尾重启（PID 86426，仍 12d7f3a 二进制）
  恢复日志通道；**9a74236 未重启部署**，实例级验证留给对方验证窗口。
- 纪要正文 markdown 源码以纯文本渲染（#/**/表格线原样可见）：功能可用，
  渲染升级留产品侧。

### 24.5 遗留（下一轮续接，增量更新）

1. embed provider：上游三缺口仅剩此项；恢复后 /api/embed 冒烟 + 应用内
   向量入库复验（§16.6 #4/§22.5）。
2. DSN 轮换 + 共享租户 gateway key 评估：仍待 halfking（Issue #14 至
   15:30 仍 0 评论）。
3. JWT <5min 自然临期：仍待 RedClaw/生产 15min token 环境（§21.4 决策
   不变；本地 TTL 硬编码 24h，不为验证改认证代码）。
4. 9a74236 的实例级验证（90s 链 curl/真机存档）归并行会话验证窗口。
5. §16.6 #1 悬空态：维持「再遇再查」。会议纪要 markdown 渲染（低优）。

---

## 25. 2026-09-05 会话补充（十二）：§21.6 #2 调优实现与挂死测试修复（与 §23/§24 并行交错）

> 本会话与 §23（会话十）、§24（会话十一）在同一工作区并行交错。本会话
> 产出的核心代码改动（最终候选免尝试超时 + 整链预算 90s + 两个新测试）
> 经并行会话收编，提交为 9a74236；08ff3e6/5212806/18293ae 为并行会话
> 自身改动。本节补记本会话视角的实现、调试与遗留缺口。零密钥。

### 25.1 环境核对（15:00 前后）

- `git fetch` 后 HEAD = origin/main = 12d7f3a，无分叉；轮末重查已前移
  至 9a74236（并行会话推送）。
- Issue #14 复查：仍 **0 评论**，halfking 无动作（§22.1 结论不变）。
- 上游探测：glm-5.2/minimax-m3 `no_candidates` 之外，**kimi-k3 也转为
  no_candidates**（§22.3 时还可用）——15:00 探测时点上游零可用 provider
  （§24.1 记载 14:55 起 glm/minimax 已恢复，与本探测窗口交错）；
  `/api/embed` 仍 `no_provider`；轮末一次探测回 429 rate_limit_exceeded
  （网关侧状态持续变动，部署侧观察项）。
- 运行实例核对：:8088 为 PID 80722（14:48 并行会话以 HEAD 二进制启动）；
  另有 :8090 PID 67860（连 PostgreSQL，并行会话的 PG 形态）。均不干扰。

### 25.2 本会话代码改动（§21.6 #2 调优项落地，已随 9a74236 在库）

1. **Stream 回退链最终候选免尝试级超时**：最终候选（其后无 fallback
   可退）窗口放宽为整链剩余预算——它在链上只有一次机会，20s 窗会误杀
   慢而可用的候选（kimi-k3 长 prompt 首 token >20s，§22.4 纪要生成因此
   终态失败）。非最终候选 20s 尝试窗不变（尽快淘汰挂死候选）。
2. **整链预算 60s→90s**：按「2×20s 死候选 + 最终候选分到 50s」取值
   （60s 时最终候选恰剩 20s、与尝试窗重合，放宽形同虚设）；最坏 90s
   仍低于前端 120s 流级看门狗（§14.3 fix1）。
3. 新增测试 `TestDynamicGatewayStreamFinalCandidateGetsFullRemainingBudget`
   （首候选挂死耗掉尝试窗 → 慢候选 500ms > 尝试窗 100ms、在剩余预算内
   出正文 → 整流成功；旧逻辑此处必败）与
   `TestDynamicGatewayStreamFinalCandidateStillBoundedByChainBudget`
   （唯一候选无限挂死 → 错误恰在整链预算处浮出，放宽不破坏兜底）。

### 25.3 调试插曲与关键修复（本会话独有，待提交）

- **挂死根因（产品代码无辜）**：Bounded 测试首版把测试进程挂死 600s
  （吃满 `-test.timeout`）。定位链：`sample` 近零 CPU → SIGQUIT 全栈 →
  客户端侧全部返回、服务端 handler 卡 `<-r.Context().Done()`、
  `srv.Close()`（WaitGroup.Wait）永久阻塞 → 15s 短超时重跑拿到关键日志
  `[llm-auto] stop fallback chain ... budget_left=0s`——整链预算处
  DeadlineExceeded 准点返回，放宽语义正确。
- **根因**：Go `net/http` 服务端要等 request body 读到 EOF 后才启动
  background read 感知客户端断连；handler 不读 body 直接挂死 → 客户端
  ctx 超时断开后服务端永远不知道 → `r.Context()` 永不触发。同文件既有
  挂死式 handler 都先 `json.NewDecoder(r.Body).Decode(&req)` 消费 body
  （故此前从未暴露），本会话新写时漏了。
- **重要**：并行会话收编（9a74236）时尚未包含本修复，**HEAD 上该测试
  为挂死版**（跑 `go test ./...` 会对该包挂满超时后 FAIL）。工作区已备
  8 行修复（handler 先消费 body + `errors.Is(err, context.DeadlineExceeded)`
  断言加强），随本节同批提交。教训已写入测试注释：**httptest 挂死式
  handler 必须先消费 request body**。

### 25.4 回归记录

- 回退链定向测试 9 个全绿（含新增 2 个 + §23 的 DoneFrame 测试；
  存档 `fallback-chain-tests.txt`）。
- `go test ./...`（混合工作区）：46 包 ok；唯一失败
  `TestPiAdapter_InterruptSession` 为负载偶发（失败窗口与另一项目 nbjl
  集成测试并发期重叠，单跑 ×3 全过，与本轮改动无关）。
- `gofmt`：本轮文件干净；`server_assistant.go` /
  `scheduled_task_integration_test.go` 为 HEAD 存量未格式化（§23.2 同
  记录），未收编。

### 25.5 冒烟与重启边界

- 冒烟对象 PID 80722（12d7f3a 代码）：healthz ok、login 200、config 200
  （key 掩码）、chat auto 结构化错误终态（存档 `smoke-pid80722.txt`，
  已脱敏）。
- **本会话未重启服务**：并行会话 14:48 刚重启 :8088、15:08 另起 :8090
  PG 实例，活跃窗口内不干扰（沿 §17.5/§22.6 先例）。90s 预算 + 最终
  候选放宽运行时生效待下次重启（编译已由全量测试隐含验证）。

### 25.6 遗留（下一轮续接，增量更新 §22.7/§23/§24）

1. **随本节提交**：挂死测试 8 行修复（HEAD 上该测试当前为挂死版，
   见 §25.3，合入后 `go test ./...` 恢复正常时长）。
2. 重启 :8088 dev 实例，带入 90s 预算/最终候选放宽（并行窗口结束后；
   §21.2 #4 的 60s 注记届时由 §25.2 取代）。
3. 纪要生成成功态真机留证：**已由 §24.3 收口**（gen#1 灰字+正文同框、
   gen#2 直达，跑在 60s/20s 旧实例上）；剩余为在 9a74236（90s 预算）
   实例上复验——与遗留 #2 重启合并执行。
4. Issue #14 三项 + 共享租户 gateway key 评估（§22.7 #1 沿用）：仍
   阻塞在 halfking/网关维护侧（本轮复查仍 0 评论）。
5. 沿用观察项：kimi 时延（若 90s 预算下仍紧，再议「尝试超时参数化」）、
   §16.6 #1 悬空态、embed/DSN 外部项（§21.6）。

---

## 26. 2026-09-05 会话补充（十三）：start-dev.sh 端口化修复 + 90s 预算实例重启 + 网关新错误形状回退缺口修复

> 本轮执行 §25.6 遗留 #2（重启带入 90s 预算）。过程中发现并修复两个
> 独立缺陷：启动脚本的姊妹仓误杀，与网关改版后的回退失配。零密钥。

### 26.1 start-dev.sh 两缺陷修复（重启前置）

| # | 缺陷 | 修复 |
|---|---|---|
| 1 | `killall pocketd` 按名全杀——本机 :8090 常驻**姊妹仓** `ai-native-tools/openpocket` 的同名 pocketd（本轮实测确认，§24 曾误记为"本仓 PG 形态"），重启本仓会误伤它 | 停止/验证/停止提示三处全部改为按 `POCKET_HTTP_PORT` 用 lsof 精确定位 |
| 2 | 启动验证固定 `sleep 2`——16:18 现场实测从拉起到 listening 耗 8s+，检查必然失败、误报「Backend 启动失败」（进程实际正常） | 轮询 healthz（最多 30s），PID 按端口取 |

修复后从仓库根执行全套通过：按端口停旧 → 新实例 → 健康检查 → 登录
冒烟 → 实例列表；:8090 姊妹仓进程全程无恙。`bash -n` 通过。

### 26.2 回退缺口：网关新错误形状不触发回退（已修复）

重启后线上复验发现：显式 `kimi-k3`（无 provider）`/api/llm/chat` 直接
503 透传、**未按设计回退**到 preferred 下一候选。根因：网关对无 provider
模型的错误形状已改版——`code` 从 `no_candidate` 改为 **`model_not_found`**、
`kind` 用复数 **`no_candidates`**（修复前实测体存档 §22.3 显式 glm 502
同形状，当时未对照回退设计），而 `isModelUnavailableError` 只认
`code=="no_candidate"`/`"invalid_model"`。

修复：匹配扩展为 `code∈{no_candidate(经 isNoCandidateError), invalid_model,
model_not_found} ∪ kind=="no_candidates"`。新增测试
`TestIsModelUnavailableErrorGatewayShapes`（四个真实形状用例，含反例
rate_limit_exceeded 不得误判）与
`TestDynamicGatewayChatFallsBackOnModelNotFound`（显式无货 model → Chat
回退到 preferred 下一候选成功）。

### 26.3 重启与运行时复验（PID 16346：90s 预算 + 全部修复）

- **显式 kimi-k3（无 provider）→ 回退 glm-5.2 成功**：`{"content":"pong",
  "model":"glm-5.2"}` 200（修复前此处 503）——缺口修复线上验证 ✅；
- chat auto → glm 直达 200；stream auto → SSE 正常（content 帧 + stop +
  usage + `[DONE]`），**零 retry 帧**（preferredModels 首选命中不触碰
  回退链），`[llm-auto]` 日志零条，行为符合设计 ✅；
- `/api/embed` 上游仍 `no_provider`（三缺口中唯一未恢复项，部署侧）。
- 存档：`test-evidence/2026-09-05-restart-and-fallback-shapes/`。

### 26.4 上游状态观察（日内反复横跳）

14:47 三通道全灭（§24.1）→ 14:55 glm/minimax 恢复（§24.1）→ 15:00
全灭（§25.1）→ 16:14 glm 空回复 30s + kimi no_candidates → 16:20 glm
恢复、kimi 仍 no_candidates。**kimi-k3 provider 至今未恢复**（§17.3
时可用），网关侧 provider 配置稳定性属部署侧观察项；本仓链路对各形态
（快速失败/挂死/空回复）均以结构化错误或回退收敛，无悬挂。

### 26.5 回归记录

- 定向：回退链全家（含新增 2 测试）全绿（2.3s）。
- `go test ./...`：全绿零失败（`full-suite.txt` 存档）。
- `bash -n backend/start-dev.sh` 通过；重启后 healthz/login/config/
  chat/stream 全通。

### 26.6 遗留（下一轮续接，增量更新 §25.6）

1. embed provider：唯一剩余上游缺口（部署侧，Issue #14 范围）。
2. kimi-k3 provider 恢复：网关侧待配（§26.4）；恢复后 auto 链第三候选
   重新可用。
3. Issue #14（DSN 轮换 + 注入源 + 共享租户 gateway key 评估）：仍阻塞
   在 halfking（本轮未复查，§22.1/§25.1 结论沿用）。
4. 90s 预算下「2 死候选 + 慢最终候选」的确定性成功场景：glm 恢复后
   auto 直达、回退链少被自然触发，留观；§22.4 种子方法已备可随时复验。
5. 沿用：§16.6 #1 悬空态观察；kimi 时延参数化议题（§25.6 #5）。

---

## 27. 2026-09-05 会话补充（十四）：9a74236 实例级验证——最终候选免尝试窗 + 90s 预算准点收敛（§26.6 #4 收口）

> 接手时 HEAD=c45e825；本轮工作窗内并行会话（§26 作者）落地 8240062
> （model_not_found/no_candidates 回退失配修复 + start-dev.sh 端口化）并
> 重启 :8088 至 90s 预算实例（PID 16346，16:18）。本轮**零代码改动、零重启**，
> 承接 §26.6 #4 在该实例上完成 9a74236 运行时验证。零密钥。

### 27.1 接手核对与并行交错

- `git status` 干净（仅 logs/backend-dev.log 存量修改）；工作中途 HEAD
  前移至 8240062 并已推送——以「接手先核对提交归属」规约确认其归属并行
  会话，本轮不触碰其文件（含 runbook §26 与 start-dev.sh）。
- 上游探测（16:13~16:22，存档 `test-evidence/2026-09-05-budget90-restart/`）：
  glm-5.2 恢复（3s pong）；minimax/kimi 一度 `503 model_not_found` 0s 快速
  失败（**独立在先**佐证 8240062 所修缺口，16:22 三模型全恢复）；一次
  9 模型并发扫描触发上游 429 限流——探测须串行（新经验）。embed 仍
  `503 no_provider`（任务①维持待上游，不伪造）。
- 日志通道确认可写（对方重启后 fd 正常，本轮全部请求落盘——§22.6/§24.4
  的截断问题本轮未复现）。

### 27.2 实例级验证设计：用挂死候选代替时延赌博

glm 当日时延 2.8s~16s 反复横跳（§26.4 同观察），prompt 加大到 5652 tokens
仍 6~15s 完成——**赌时延相位无法确定性触发 20s 窗**。改用结构化方法：
前置探测确认 kimi-k3 上游无 provider 且 stream 路径**挂死**（§22.3 形态
延续），以 kimi 作挂死候选即可确定性构造「非最终候选被 20s 窗杀」与
「最终候选免窗、由 90s 预算兜底」两场景。网关配置经 POST 三次临时变更、
逐次还原、终态逐字段核验与原状一致。

| # | 场景 | 实测 | 结论 |
|---|---|---|---|
| P | 显式 kimi（preferred=[glm]，kimi 非最终） | T+20s 窗杀 → 首帧 `retry:"glm-5.2"` → glm 收尾，全程 22.0s；`[llm-auto] kimi-k3 -> fallback glm-5.2 (context deadline exceeded)` | 非最终候选 20s 尝试窗实例级 ✅ |
| R4 | preferred=[kimi-k3] 单候选（kimi 即最终候选） | 错误规整终态 @**T+89.98s**；`[SLOW] 1m30.0s`；`stop fallback chain ... budget_left=-1ms`（旧代码 ~20s 即死，死亡时间即判据）；SSE 90s 存活（7ecf9ed 写死线豁免） | **最终候选免窗 + 90s 预算兜底实例级 ✅** |
| R3' | preferred=[假模型, glm]，auto | invalid_model 0.37s 快速失败跳过 → retry 帧 → glm 15.4s 收尾 | §16.2 回归 + 新形状修复链上生效 ✅ |
| R1b | preferred=[glm]，显式 glm 长 prompt（5652 tokens） | T+14.9s 直达成功（快相） | 直达路径 + 配置变更链路 ✅ |

retry 帧序全符合设计：`retry → content* → done/stop → usage → [DONE]`。

### 27.3 会议纪要应用路径的覆盖口径（§25.6 #3 API 级）

应用纪要生成走 `meetings-ai.ts streamChat` → 同一 `/api/llm/stream`；
本节 P/R4/R1b 已覆盖该端点直达/回退/预算兜底三态，纪要级 prompt 形状
差异不改变服务端链路语义。真机 UI 复验（§22.4 种子方法已备）留待上游
慢相窗口——当前 glm 快相下真机 gen 预期为直达成功（§24.3 gen#2 同态），
retry 路径真机态依赖 kimi 恢复或 glm 慢相，属上游时序非代码项。

### 27.4 任务清单收口状态（接续 §24.5/§25.6/§26.6）

| 事项 | 状态 |
|---|---|
| ① embed provider 恢复 + 向量入库复验 | **仍待上游**（16:14 探测 `no_provider`，唯一未恢复缺口；应用内侧降级 §22.5 已收口） |
| ② 9a74236 实例级验证 | **本轮收口**（§27.2；重启+冒烟部分由 §26.3 先行完成） |
| ③ JWT 临期自然场景 | 维持待 RedClaw/生产 15min token 环境，不为验证改认证代码（§21.4 决策不变） |
| ④ DSN 轮换 + 共享租户 gateway key | 维持待 halfking（Issue #14，本轮未复查） |
| ⑤ 纪要 markdown 渲染 | 维持产品侧低优（§24.4 记录纯文本渲染可用） |

### 27.5 遗留（下一轮续接，增量更新 §26.6）

1. embed provider：恢复后 `/api/embed` 冒烟 + 应用内笔记向量入库复验
   （`local_note_vectors` 计数 >0，§16.6 #4 全链收口）。
2. kimi-k3 provider 恢复观察：恢复后重跑 R4 型场景可升级为「慢最终候选
   确定性成功」（存活版 R4）；§26.6 #4 的「2 死候选+慢最终候选」自然
   触发亦依赖此前提。
3. 会议纪要真机复验（90s 实例 + 上游慢相窗口）：种子/CDP 工具就绪
   （`2026-09-05-budget90-restart/tools/` 增补 ts_sse.py 时戳器）。
4. 沿用：§16.6 #1 悬空态、JWT 临期（③）、Issue #14 三项（④）、纪要
   markdown 渲染（⑤）、kimi 时延参数化议题（§25.6 #5）。
5. 新经验：上游网关对并发突发敏感（429），探测/验证一律串行小间隔；
   SSE 存档管道勿接 head/tail（SIGPIPE 连锁掐断 curl，先落盘后查看）。

---

## 28. 2026-09-05 会话补充（十五）：上游缺口复探 + 真机空流发现「空流透传不回退」缺口并修复

> 接手时 HEAD=bd4156c（与远端一致，无并行新提交）。本轮前三件事（①embed
> 复探 ②kimi 恢复观察 ③纪要真机复验）因上游相位连续落空，但在 ③ 过程中
> **真机复现并修复一个新回退缺口：上游「200+零帧空流」被当成功透传**。
> 运行实例经 start-dev.sh 按端口重启至修复版（PID 40182，17:19），:8090
> 姊妹仓进程全程无恙。零密钥。

### 28.1 上游复探（串行小间隔，存档 `test-evidence/2026-09-05-recovery-probes/`）

| 时刻 | 探测 | 结果 |
|---|---|---|
| 16:54 | /api/embed 小文本 | `503 no_provider` 0.34s——①维持待上游 |
| 16:55 | 显式 kimi-k3 chat | 200 3.8s 但 `model:"glm-5.2"`——kimi 仍无 provider，经 8240062 回退链落 glm（缺口修复持续在线生效） |
| 16:52 / 16:53 | 显式 glm 长 prompt ×2（5652 tokens，stream） | TTFT 8.13s / 总 8.42s、总 9.04s——glm 稳定快相，**慢最终候选存活版 R4 两次未触成**（存档 r1surv-glm-final.sse.ts.txt） |
| 17:09 | 会议同形请求（auto，system+转写） | 200 40.0s：`retry:"minimax-m3"` → minimax 流式出正文 20s 后被尝试窗杀 → T+40s 规整错误终态（glm 窗 20s + minimax 窗 20s，非 90s 预算耗尽） |
| 17:20 / 17:31 | chat auto | `502 no_candidate` 1.1s——上游进入全灭相（14:47/15:00 同款），随后真机验证全程维持 |

结论：①②前提依旧不满足（embed/kimi 未恢复 + glm 快相），且 17:06 起上游
进入当日第三轮全灭窗。相位横跳节奏同 §26.4。

### 28.2 ③ 的执行与并行争用（改独立模拟器）

- 运行实例确认：:8088 = PID 16346（§27 同实例），healthz ok；网关配置
  终态与 §27 一致（glm 首选，逐字段核对）。
- 模拟器 emulator-5554 上的 App 于 16:55:17 被**并行会话更新安装**（HEAD
  含 a48fcf4），本轮种子/生成期间页面状态反复漂移（哈希自行回 #/ai、
  保险库二次锁定）——判定并行会话正在同一模拟器上活跃操作，**不与其
  争用**，改在独立 AVD `pocket_test2`（android-36.1 arm64，:5556，
  -no-snapshot 冷启动）上以 HEAD=bd4156c 现场构建 APK 复验（17:27 构建）。
- CDP 种子三坑（§22.4/§24.3）本轮完整重踩并沉淀为可执行脚本
  （`2026-09-05-recovery-probes/tools/`）：原生桥无 `isConnection`（直调
  createConnection，「already exists」即复用）；批量 execute 静默中止
  （逐条 `run`）；`--` 头注释须剥离。种子会议
  `meeting-e2e-budget90-1788599084177`（4 段转写）入库核验一致。

### 28.3 真机空流复现 → 新缺口：空流透传不回退（已修复）

17:06 在 emulator-5554 上首次点击「生成纪要」：≤3s 即终态红字
**「模型未返回内容（空流）」**，无 retry 帧、无网络重试痕迹。同形请求
curl 复核（17:09）链路行为正常（有 retry 帧有正文）。对照代码定位：

- `llmgateway/stream.go` parseSSEStream：上游只回 `[DONE]`/keepalive
  （或非 SSE 体）时**零回调 + err=nil**（§26.4「glm 空回复」的流式形态）；
- `llmbff_provider_adapters.go` Stream 回退链 `err == nil` 即返回成功
  ——空流被当**成功**透传，客户端拿到「200+零帧」，前端空流兜底报错，
  回退链完全没用上。

**修复**（最小改动，同文件）：

1. attemptFn 计上游帧数 `attemptFrames`；`err==nil && attemptFrames==0`
   时合成 `errEmptyStreamAttempt`（新哨兵错误）；
2. `streamAttemptFallbackEligible` 增列空流为回退资格（必然未作答，无
   重复作答风险）——非最终候选换候选重试并出 retry 帧；最终候选以
   errEmptyStreamAttempt 返回，由 handler 落**结构化错误终态帧**（不再
   静默空透传）。

测试：新增 `TestDynamicGatewayStreamFallsBackOnEmptyStream`（首位候选
[DONE]-only → 回退 model-b、首帧 retry、正文+终态正常）与
`TestDynamicGatewayStreamFinalCandidateEmptyStreamIsError`（单候选空流 →
errEmptyStreamAttempt、不多打上游、零 delta）。`go build` + 定向回退链
全家全绿（2.4s）+ `go test ./...` **46 包全绿零失败**（full-suite 存档
/tmp/full-suite-empty-stream.txt）。

### 28.4 修复版实例重启与真机复验（pocket_clone，:5556）

- 17:19 经 `backend/start-dev.sh` 按端口重启 :8088（新 PID 40182，含
  空流修复）：healthz/登录冒烟通过；日志通道随重启恢复写入（§27.1 的
  fd 截断问题同步消除）；:8090 姊妹仓进程无恙。
- 独立模拟器落地过程：avdmanager（SDK XML v4 不兼容告警）创建的 AVD
  两次首启挂死（30min 不出 boot）——**克隆法**可用：复制正常 AVD 的
  config.ini + `.ini` 指针（`pocket_clone`），30s 上线。APK 以 HEAD
  bd4156c 现场构建（17:27）。App 全新安装：登录 admin → 首启创建主密码
  （值同 dev 约定不引用）→ CDP 种子会议（同 §28.2 脚本）→ 列表重挂载
  后可见（种子后须离开/重进 #/meetings 才重查 DB）。
- **配置漂移记录**：本轮期间（16:59 后）并行方把 preferredModels 改为
  `[]`（17:59 备份存档 config-before-recovery-round.json）——auto 解析
  落到 catalog 首个 abab5.5-chat，即席最终候选。本轮未 POST 过配置、
  不代为恢复（归属并行方）。

### 28.5 真机结果（§28.3 修复版实例上）

| 场景 | 实测 | 结论 |
|---|---|---|
| 全灭相（chat auto 502 no_candidate 持续）下点「生成纪要」 | SSE 存活 **90.0018s** 准点收敛：`[llm-auto] stop fallback chain: model=abab5.5-chat err=context deadline exceeded answered=false budget_left=-1ms`；真机红字 `context deadline exceeded` 终态、按钮恢复、无悬挂；`local_meetings.summary` 保持 NULL（无幻影写入） | §27 R4「最终候选免窗+90s 预算兜底」经 **App 路径**意外实证 ✅（截图 07） |
| 空流→回退成功路径 | 确定性复验被 **SSRF 守卫阻断**：`validateOutboundURL` 拒绝 loopback/私网 baseURL（ssrf.go:144，设计如此）——配置切换到本地假上游的方案不可行；假上游工具已归档（tools/fake_upstream.py）。空流修复以 **双单测**（回退+最终候选两态）+ 实例部署为验证口径；自然复现（glm 空流形态再现时）应表现为「已切换到 X 重试…」灰字而非空流红字——列入下轮观察 | 单测级 ✅ / 实例级留观 |

### 28.6 遗留（下一轮续接，增量更新 §27.5）

1. **空流修复的实例级留观**：上游恢复后，凡遇 glm 空流形态（§26.4），
   纪要/ai-chat 应出现回退灰字并成功收尾，而非「模型未返回内容（空流）」
   红字；若再现空流红字即修复失效，优先复查。
2. embed provider（①）：仍待上游（18:05 复探仍 `no_provider`）；恢复后
   /api/embed 冒烟 + `local_note_vectors` 计数 >0（§16.6 #4 全链收口）。
3. 慢最终候选存活版 R4（②）：仍待 kimi 恢复或 glm 慢相；本节 abab5.5-chat
   挂死 90s 收敛已从「失败侧」实证同一条免窗预算语义。
4. 纪要真机成功态（快相直达 / retry 灰字+正文同框）：种子/克隆模拟器
   方法已备（pocket_clone + tools/），待上游恢复窗。
5. 沿用：JWT 临期（RedClaw/生产 15min 环境）、Issue #14 三项（halfking）、
   纪要 markdown 渲染（产品侧）、§16.6 #1 悬空态、kimi 时延参数化。
6. 环境注记：并行方本轮将 preferredModels 清空（§28.4）——下一轮接手
   先 GET /api/llm-gateway/config 核对当前值再设计场景；avdmanager 建新
   AVD 在本机不可用（XML v4），用克隆法（§28.4）。


---

## 29. 2026-09-05 会话补充（十六）：全灭长窗守候留观 + 环境整备（任务①②③④前提全程未开）

> 接手时 HEAD=bd44c74（与远端一致，无并行新提交）。本轮为 §28.6 #1
> 空流修复实例级留观轮：上游全灭相自 17:06（§28.1）持续至收口（23:10），
> 守候 3 小时+ 任一模型未恢复，①②③④前提全程未开，均维持待上游。
> 零代码改动、零配置 POST；因日志通道再遭外部截断按端口重启一次
> （19:46 起 PID 95713，同一修复版二进制）。零密钥。

### 29.1 接手核对与运行实例

- git status / log 与起手快照一致；GET /api/llm-gateway/config 逐字段
  核对：`preferredModels=[]`（§28.4 并行方终态）维持不代改，baseURL/
  format/apiKeySet 与 §28 一致，apiKey 掩码 sk-h****3QIv。
- 运行实例 :8088 = PID 40182（§28 修复版）；:8090 姊妹仓无恙。
- **日志通道外部截断再发**（§17.5/§22.6/§24.4 同款，18:11 发生）：
  mtime 停 18:11、`lsof` fd 偏移 17170 > 文件 8437 字节——19:02 起的
  探测一度不可落盘。19:46 经 `backend/start-dev.sh` 按端口重启（仅杀
  :8088 监听者），新 PID 95713，`backend/pocketd`（mtime 17:19）未重建，
  空流修复与 90s 预算语义不变；重启后 fd 偏移=文件大小，通道恢复，
  DB 配置加载核对一致（baseURL 不变）。此为 §28.4 同法的第三次应用。

### 29.2 串行探测与守候（存档 `test-evidence/2026-09-05-empty-stream-watch/`）

| 时刻 | 探测 | 结果 |
|---|---|---|
| 19:02 | embed / glm / kimi / auto（串行） | `no_provider` / `no_candidates`×2（0.4~0.6s 快速失败）/ auto `no_candidate`（abab5.5-chat） |
| 19:43~19:45 | 复探同上 | 仍全灭 |
| 19:47~20:19 | 守候 8 轮：glm+auto 双发/轮、4.5min 间隔（单连接串行规避 429，§27.1 新经验） | 16 发全灭 |
| 20:03 | embed | `no_provider` |
| 23:09~23:10 | 终态快照 glm/kimi/minimax/auto/embed（串行） | 全灭（auto 同窗口已解析为 claude-sonnet-4.5，见 29.3） |

全灭相下「200+零帧空流」形态无由产生（无任何候选可接请求），§28.6 #1
的留观在相位上不可能触发——非修复失效信号，判定留观窗口未开。

### 29.3 新观察：auto 即席最终候选随上游 catalog 漂移

同配置（`preferredModels=[]`）下，auto 的即席最终候选 19:02~20:19 为
catalog 首个 `abab5.5-chat`，23:10 终态快照变为 `claude-sonnet-4.5`
（网关 POST 终态逐字段未变）——上游 `/models` 目录顺序漂移所致。含义：
「空首选」场景的最终候选不稳定，下一轮设计 auto 场景与判读 no_candidate
错误消息时，须以当次 GET config + 实际探测解析为准，勿沿用上轮候选名。

### 29.4 环境整备（下轮零预热）

- **pocket_clone（:5556）**：-no-snapshot 冷启动 20s 上线（克隆法 §28.4
  再证可用）；App 解锁（主密码 start-dev.sh 默认同值约定，经变量注入
  不引用）；§28 种子会议在列且详情完好（4 段转写、纪要空缺、按钮就位，
  截图 01-clone-meeting-detail.png）。
- **CDP 基线固定**（桥法 §22.4/§28.2：`window.Capacitor.Plugins.
  CapacitorSQLite`，库 `lobster`；cdp-baseline-counts.txt）：
  `local_note_vectors=0, local_notes=0, meetings_with_summary=0`。
  下轮 ② 恢复后应用内新建/编辑笔记即触发 `embedAndStore`
  （`notes-store.ts:99/156` → /api/embed → `vectorIndex.add`），vec 应
  >0；④ 成功后 meetings_with_summary 应转 1。
- **守候工具沉淀**：`probe_watch.sh`（glm+auto 双发/轮串行守候，任一
  200 即退出码 0 供无人值守挂起）——注意 auto 随 catalog 漂移（29.3），
  恢复判定以 glm 发为主。
- **前端新合入提示**：收口前远端新增 b39714d（frontend API base 空注
  入双重加固，纯前端 9 文件）并经 4bb9f99 与 bd44c74 汇合。clone 上
  现装 APK 为 17:27 bd4156c 前端构建——下轮 App 路径验证前须以最新
  HEAD 现场重建 APK（§28.4 法），后端无涉、修复实例继续有效。

### 29.5 任务清单收口状态（接续 §28.6）

| 事项 | 状态 |
|---|---|
| ① 空流修复实例级留观 | **窗口未开**（全灭相 17:06 起持续，无空流形态由头）；修复双单测+实例部署口径不变 |
| ② embed 恢复 + 向量入库 | 仍待上游（19:02/20:03/23:10 三次 `no_provider`）；基线已固定（29.4） |
| ③ 存活版 R4 | 仍待 kimi 恢复或 glm 慢相（glm/kimi/minimax 终态全 `no_candidates` 快速失败） |
| ④ 纪要真机成功态 | 仍待任一模型恢复；真机/种子/基线已就绪（29.4） |
| ⑤⑥ 沿用 | JWT 临期（RedClaw/生产）、Issue #14（halfking）——未动 |

### 29.6 遗留（下一轮续接，增量更新 §28.6）

1. 沿用 §28.6 #1~#5 全部事项，前提判定一律先跑 `probe_watch.sh` 或
   串行快照（glm 为主，auto 仅参考——catalog 漂移，29.3）。
2. 下轮 App 路径验证前置：以含 b39714d 的最新 HEAD 现场重建 APK 装入
   pocket_clone（旧 APK 为 17:27 前端构建）。
3. auto 即席候选漂移议题：`preferredModels=[]` 下最终候选不受本仓控制，
   建议并行方/halfking 评估是否恢复显式 preferredModels（§28.4 归属
   并行方，本轮未代改）。
4. 环境注记：日志通道外部截断已三次发生（18:11 / §24.4 / §22.6），
   观测前先 `ls -la` + `lsof` fd 偏移比对，异常即按端口重启恢复。

## 30. 2026-09-06 会话补充（十七）：glm 恢复窗观测 + ④结构性不可达判明 + APK 构建纠偏（守候命中一次）

> 接手时 HEAD=f339be4（与远端一致）。本轮以守候+快照覆盖了上游
> 「全灭 → glm 独活（00:34~01:43+，≥69min）→ 再灭」完整漂移；
> 判明 ④ 纪要真机成功态在当前相位组合下结构性不可达（非 App 缺陷）；
> 修正 APK 构建方法误用。零配置 POST，preferredModels=[] 全程未动；
> 日志通道外部截断第五次发生（累计五次），两次按端口重启均恢复。

### 30.1 接手核对与运行实例

- git status / log 与起手快照一致；GET /api/llm-gateway/config 逐字段
  核对：`preferredModels=[]`（并行方终态）维持不代改，baseURL
  https://llm.kxpms.cn/v1 / format openai-chat / 掩码 sk-h****3QIv
  与 §29 一致。
- **日志通道外部截断第四次**（§29.6 同款，接手即见）：fd 偏移
  25745 > 文件 22118、mtime 停 09-05 23:23——00:07 按端口重启
  （新 PID 75266），通道恢复、DB 配置加载核对一致。
- 本轮中段 **第五次截断**（01:41 发现：fd 12577 > 文件 1034、mtime 停
  00:20，纪要请求日志全程未落盘）——01:41 再按端口重启（PID 56956）。
  累计五次（§22.6/§24.4/§29.1×3 + 本轮×2），按端口重启法四次应用全部有效。

### 30.2 相位全程（存档 `test-evidence/2026-09-06-phase-probe/`）

| 时刻 | 探测 | 结果 |
|---|---|---|
| 00:08~00:10 | glm/kimi/embed/auto 串行 | 全灭（glm `no_candidates` 0 候选；kimi `invalid_model`；embed `no_provider`；auto→claude-sonnet-4.5 `no_candidate`，§29.3 漂移延续） |
| 00:11 | 挂 `probe_watch.sh`（glm+auto 双发/轮 ×8） | — |
| **00:34:11** | **守候 try#6 命中：glm-5.2 恢复 200** `{"content":"Pong"}` | auto 仍灭；probe-watch.txt |
| 00:39 / 01:43 | glm 复探 | 200×2（恢复窗内稳定） |
| 02:0x | SSE glm `/api/llm/stream`（App 同链路） | 0.19s 快速失败：上游 nginx 502 终态正常收敛 |
| 10:43~10:45 | 终态快照 glm×2/auto/embed | **全灭**，形态变化：502 Bad Gateway（nginx 层，有候选被路由、上游入口挂）替代此前 503 no_candidates |

- glm 恢复窗 ≥69 分钟（00:34~01:43+），随会话长暂停后（10:43 视角）已灭。
  「空首选场景最终候选随 catalog 漂移」（§29.3）再证：恢复判定以 glm 为主的
  守候法在本轮命中一次，沉淀有效。

### 30.3 ④ 纪要真机成功态：结构性不可达判明（本轮主要结论）

- 前置达成：pocket_clone（emulator-5556，在线）装入**最新 HEAD 正确构建
  APK**（01:21，含 b39714d），解锁（tools/cdp_unlock.py 沉淀：口令经
  start-dev.sh 缺省约定间接提取注入，不经命令行/落盘明文）、种子会议在列。
- **APK 构建纠偏**：`npm run build`（mode=production）会把 `.env.production`
  占位域名 `https://pocket.example.com` 打进 bundle → App 全部 API
  `Failed to fetch`（01:15 首轮踩中）。正法：
  `node scripts/build-mobile.mjs android dev`（mode=android-dev →
  `10.0.2.2:8088`，脚本含 sanity check 校验 bundle 含正确 base）——
  b39714d 注释早已写明此约束，构建命令必须走 build-mobile.mjs。
- App 纪要路径（MeetingDetailView → summarizeMeeting → streamChat
  kind=meeting_summary → POST /api/llm/stream **无 model 字段**）：BFF 端
  空 model → preferred 首选 → `[]` → auto → claude-sonnet-4.5（灭）→
  90s 预算耗尽。三轮实测均 `context deadline exceeded` 红字终态、按钮
  恢复、无悬挂；`meetings_with_summary=0` 无幻影写入（CDP 桥法核对）。
- **`POCKET_LLM_MODEL` 注入实验（02:02，PID 70783）**：只影响后端内部
  一次性 chat（llmChatOnce，server_meeting.go API 级路径），不影响 BFF
  流式路径——App 纪要仍 90s 超时；02:11 回滚标准 env（PID 83845）。
- **结论**：④ 成功态在「preferredModels=[] + auto 灭 + 仅 glm 独活」相位
  组合下结构性不可达，非 App 缺陷。达成条件二选一：auto 链候选恢复；
  或并行方恢复显式 preferredModels 含可用模型（归属并行方，§28.4/§29.6 #3）。

### 30.4 ① 空流修复留观（窗口未开，修复未现失效信号）

- 恢复窗内 glm 全部正常 200（pong ×3），未现空流形态；灭相期 chat/SSE
  均为快速失败（no_candidates / invalid_model / 502，0.2~0.6s），「200+零帧」
  由头未现。修复继续以双单测+实例部署为口径，自然复现应现「已切换到 X
  重试…」灰字——列入下轮，恢复窗内于 App ai-chat（可显式选 glm-5.2，
  aiChatStore modelByModality）与纪要双路径观察。

### 30.5 任务清单收口状态（接续 §29.5）

| 事项 | 状态 |
|---|---|
| ① 空流修复留观 | 窗口未开（恢复窗内 glm 正常无空流形态；修复未现失效信号） |
| ② embed 恢复+向量入库 | 仍待上游（no_provider → 终态 502）；基线 0/0/0 维持 |
| ③ 存活版 R4 | 仍待 kimi 恢复或 glm 慢相（恢复窗内 glm 快速 200，未构成慢相） |
| ④ 纪要真机成功态 | **结构性不可达判明**（30.3）——待 auto 链恢复或 preferredModels 显式化；App 链路自身 90s 收敛/无幻影再次实证 |
| ⑤⑥ 沿用 | JWT 临期、Issue #14（halfking）——未动 |
| 附：APK 构建规约 | `npm run build` 禁用于移动构建（占位域名入包）；一律 `build-mobile.mjs android dev`（30.3） |

### 30.6 遗留（下一轮续接，增量更新 §29.6）

1. 前提判定一律先跑 `probe_watch.sh` 或串行快照（glm 为主）；本轮守候法
   首次自然命中（try#6），恢复窗内按 30.4 双路径观察空流形态。
2. 上游 502 形态学：本轮末段全灭呈 nginx 502（有候选被路由）而非
   503 no_candidates（无候选）——判读时注意两类「灭」的层位差异。
3. ④ 达成依赖（auto 链恢复 / preferredModels 显式化）+ APK 构建规约
   （30.3/30.5 附）；App 解锁可零预热（cdp_unlock.py 沉淀）。
4. 日志通道外部截断已五次；观测前 `ls -la` + `lsof` fd 偏移比对不变，
   异常即按端口重启。

---

## §31 2026-09-06 午后收尾轮：gpt-5.6-terra 短恢复窗 + Catalog 漂移形态学 + ①App ai-chat 全链路对照（90s 兜底意外实证）

> 接手时 HEAD=7444874（与远端一致）。本轮窗口 13:44~14:35。判明网关
> catalog 大漂移（glm 同代 alternatives 目录上线、kimi-k2 退役、terra
> 短窗存活 ≈7min）；①留观于 App ai-chat 做成首个「存活模型」正向对照，
> 意外获得「最终候选免窗+90s 预算兜底」在 ai-chat 路径的实证。14:20:51
> 并行方重启接管 ：8088（认证变更），BFF 探测通道自此不可用，本轮提前
> 收口。零配置 POST，preferredModels=[] 全程未动。

### 31.1 接手核对与运行实例

- git / 远端一致；GET /api/llm-gateway/config 逐字段与 §30 一致：
  baseURL https://llm.kxpms.cn/v1 / format openai-chat / 掩码 sk-h****3QIv /
  **preferredModels=[]**（并行方终态）维持。
- **日志通道第六次外部截断**（接手即见：fd 偏移 59973 > 文件 46797、
  mtime 停 11:06）——13:45 按端口重启（start-dev.sh，新 PID 95890），
  通道恢复（fd 偏移==文件大小复核）。累计六次，按端口重启法第五次应用有效。
- 另：14:20:51 并行方重启 ：8088 时以 `>` 覆写 logs/backend-dev.log，
  我方实例 14:01:41 关键日志行被冲掉——已按读数时点逐字补录
  （2026-09-06-wrapup-watch/server-log-excerpt-140141.txt）。通道累计
  异常按七次计（第六次截断 + 本次重启覆写）。

### 31.2 相位全程（存档 `test-evidence/2026-09-06-wrapup-watch/`）

| 时刻 | 探测 | 结果 |
|---|---|---|
| 13:47 | 串行 glm/auto/embed/kimi | 全灭：glm 503 **新形态**；auto→claude-sonnet-4.5 no_candidate；embed no_provider；kimi-k2-turbo-preview invalid_model（**已退役**） |
| 13:48 | alternatives 目录复核 + 新候选逐一 pong | kimi-k3、claude-sonnet-5 **在册但 model_not_found（列表≠可路由）**；**gpt-5.6-terra 200 pong** |
| 13:54/13:55 | terra 复探 ×2 + SSE 短/长 | 200×2；SSE 短 1.96s、长（7122 tok）2.43s 干净收尾——快相 |
| 13:59~14:02 | App ai-chat 显式 terra 发消息 | 上游挂相：单尝试挂满 90s 预算，14:01:41 准点收敛（31.3） |
| 14:03 | terra 直探 | 快速失败 503——**存活窗 ≈7min（13:48~13:55）关闭** |
| 13:54~14:17 | 守候 v2（glm+auto）×4 → v3（glm+auto+terra）×4 | 全灭（try#5 起 401 为 JWT 临期，无效轮） |
| 14:17:24 | 最后一轮有效 BFF 终态快照 | glm/auto/terra 全灭（503 alternatives 形态） |
| 14:20:51 | — | **并行方重启 :8088（PID 73100，同一 17:19 二进制）**：admin/Veritrans&9527 → invalid credentials；根 .env 网关变量 14:20 被改指本地。疑似 RedClaw 认证/本地 mock 上游试验（对应 ⑤ 方向），按规约未干预 |

- **Catalog 漂移形态学（本轮新知）**：① glm 503 错误体现在携带
  `alternatives` 目录（requested_model + featured 候选清单）——错误体即
  目录快照，新候选可从中直接发现（terra 即由此发现）；② 模型生命周期
  三态：`invalid_model`（退役）→ `model_not_found`（在册未路由）→
  200/挂相/503（可路由三态）；③ 存活窗可短至分钟级（terra ≈7min vs
  glm 00:34 窗 ≥69min），高漂移相下守候判据应 glm+新目录候选双发
  （v3 脚本沉淀）。
- **App 模型解析路径再确认（代码走读）**：minutes 无 model 请求 →
  `resolveChatModel`：preferred[0] → 缓存 Models[0] → "auto"；缓存
  Models 仅由设置页 test 端点 `updateModels` 填充（`/api/llm/models`
  BFF 端点只透传不写缓存），重启后为空 → minutes 落 auto → 网关侧
  claude-sonnet-4.5（灭）。§30.3 结论机制层核实。

### 31.3 ① 空流修复留观：App ai-chat 全链路正向对照 + 90s 兜底意外实证

- 前置：pocket_clone（emulator-5556）在线零预热复用（§30 已装正确构建
  APK、已解锁）；CDP 桥（webview_devtools_remote_6946 → tcp:9223）。
- **模型选择 UI 全链路首次走通**：ai-chat 模型 sheet 列出实时目录
  **679 个模型**（preferredModels=[] 不过滤），DOM 事件勾选
  gpt-5.6-terra → 确定 → chip 生效（app-state-1.png）。
- 发送「用一句话介绍你自己」：流式正常启动（光标 + stop 按钮）。
  发送后 ~20s 上游 terra 进入挂相（连接挂起无响应），BFF 侧最终候选
  免窗单尝试挂满整链预算：`[llm-auto] stop fallback chain:
  model=gpt-5.6-terra err=context deadline exceeded answered=false
  fallback="" ctx_err=<nil> budget_left=0s`、`POST /api/llm/stream -
  200 (1m30.000416458s)`——**90.0004s 准点收敛**。
- App 终态：红字 `context deadline exceeded`、按钮恢复（send 回归）、
  无悬挂、无幻影正文（app-aichat-terra-deadline.png）。**「最终候选
  免窗+90s 预算兜底」（§27 R4）首次在 App ai-chat 路径实证**——此前
  仅 §28.7 纪要路径单一路径证据；候选切换灰字未触发属语义正确
  （单候选无 fallback 可切，fn(Retry) 不应发）。
- 空流形态（200+零帧）全程未现（挂相≠空流），修复无失效信号，留观
  续期。注意：aiChatStore 模态选择落 `pocket:ai-chat:settings:v2:<scope>`
  localStorage，App 侧残留 text=gpt-5.6-terra，下轮需显式改选。

### 31.4 ②③④ 本轮状态

- **② embed**：13:47 / 14:04 两探均 `no_provider`；CDP 桥基线复确认
  `local_note_vectors=0, local_notes=0, meetings_with_summary=0`。
  前提未开，待上游上架 embedding provider。
- **③ 存活版 R4**：kimi 未恢复（k2 退役、k3 在册未路由），glm 直接灭
  非慢相——前提未开。terra 存活窗内为快相（长 prompt 2.43s），不构成
  慢相；其挂相窗口恰被 App 请求撞上（31.3），90s 兜底已顺势实证。
- **④ 纪要真机成功态**：前提（auto 链候选恢复 / preferredModels 显式
  含可用模型）仍未满足，§30.3 结构性不可达结论沿用，本轮未再触发。

### 31.5 任务清单收口状态（接续 §30.5）

| 事项 | 状态 |
|---|---|
| ① 空流修复留观 | 恢复窗（terra）做成 App ai-chat 正向对照；空流形态未现、修复无失效信号；**90s 兜底 ai-chat 路径实证**（31.3） |
| ② embed 恢复+向量入库 | 仍待上游（no_provider）；基线 0/0/0 维持 |
| ③ 存活版 R4 | 前提未开（kimi 退役/未路由、glm 灭非慢相）；terra 快相画像已存档 |
| ④ 纪要真机成功态 | 前提仍未满足，§30.3 结论沿用 |
| ⑤⑥ 沿用 | JWT 临期（dev 生命周期 ≈32min 本轮实测）；Issue #14——未动 |
| 新：catalog 漂移形态学 | 31.2 三态模型 + alternatives 错误体即目录快照 |
| 新：:8088 归属 | 14:20:51 起并行方实例（认证变更）——下轮接手先核 |

### 31.6 遗留（下一轮续接，增量更新 §30.6）

1. **:8088 现归并行方**（PID 73100，14:20:51 起，同一 17:19 二进制、
   认证非 admin/Veritrans&9527、根 .env 网关变量被改指本地）：接手先
   `lsof -nP -tiTCP:8088` 核 PID + 登录探测凭据语义，勿假定缺省值；
   若并行方交还，按 start-dev.sh 重启并核对网关配置逐字段。
2. 上游高漂移相：catalog 三态生命周期（invalid_model / model_not_found /
   可路由）+ 分钟级存活窗；守候建议 v3 三发（glm 为主判据 + auto 参考
   + 新目录候选），候选发现看 503 alternatives 错误体。
3. ①留观续期：App text 模态残留 gpt-5.6-terra，恢复窗内显式改选
   glm-5.2（aiChatStore modelByModality / 模型 sheet）；空流修复无失效
   信号，自然复现应现「已切换到 X 重试…」灰字。
4. dev JWT 生命周期 ≈32min（13:45 签发 → 14:18~14:21 间失效）实测：
   长守候脚本需轮内重登录刷新 token（现有脚本启动时读一次 .token，需改进）。
5. ④ 达成依赖 + APK 构建规约（§30.3/30.5 附）不变；⑤⑥ 沿用。
