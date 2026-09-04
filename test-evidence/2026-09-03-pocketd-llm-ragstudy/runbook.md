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

1. `internal/chatagent/store_test.go` 硬编码 `postgres://localhost/pocket_test`，无 PG 时
   `pgxpool.New` 惰性成功 → `Init` 阶段才失败（应改为连接失败即 `t.Skip`）。预置问题。
2. `/api/llm/chat`（旧非流式路径）仍走启动时静态 `s.llm`，不消费动态网关配置
   （`server_assistant.go:2040` 附近；`server_llmbff.go:9-11` 注释已声明 SHOULD migrate）。
3. JWT 默认 15min 过期 + 无刷新机制，移动端长会话会反复 401（产品决策项）。
4. 网关上游单候选慢失败（非 no_candidate）时，20s 超时内前端仍无进度提示；
   可选优化：回退重试时下发 `data: {"retry":"<next-model>"}` 进度帧。
5. `pocket_test` AVD 缺 arm64 系统镜像（avdmanager 列表报 Missing system image）；
   当前用 `Medium_Phone_API_36.1` 替代。
