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
