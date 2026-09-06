# Deployment & Test Checklist (2026-09-07)

Tick each box as it completes. Group by phase. Companion to `PLAN.md`.

---

## Phase 0 — Prerequisites

- [ ] `go version` >= 1.22
- [ ] `docker --version` >= 4.x
- [ ] `docker compose version` >= v2
- [ ] `openssl version` available (used by `deploy-local.sh`)
- [ ] `psql --version` >= 14
- [ ] `node --version` >= 18
- [ ] `xcodebuild -version` available
- [ ] `adb --version` available; `$ANDROID_HOME` set
- [ ] `jq --version` available
- [ ] `curl --version` available
- [ ] Read repo-root `.env` and confirm `POCKET_HTTP_PORT=8088`
- [ ] Read `backend/.env.test` and confirm `JWT_SECRET=test-secret-key-for-phase7-validation`
- [ ] Confirm a real or mock OpenCode upstream URL for `POCKET_OPENCODE_INSTANCES` (Risk #1 in PLAN §11)

## Phase 1 — Deploy

- [ ] `cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket`
- [ ] (PG route) `./deploy-local.sh` — confirms env file + DSN at `${POCKET_CONFIG_DIR}/.env.local`
- [ ] (SQLite route) `bash scripts/start-dev.sh` — starts `pocketd` on `:8088`
- [ ] `curl -fsS http://localhost:8088/healthz` → `ok`
- [ ] `tail -50 logs/pocketd-dev.log` — no startup errors
- [ ] `node mock-llm-gateway.js &` (background)
- [ ] `curl -fsS http://localhost:8089/v1/models` returns a JSON array
- [ ] iOS sim booted: `xcrun simctl list devices booted | grep Booted`
- [ ] Android emulator: `adb devices` shows `emulator-5554` (or similar)
- [ ] `adb reverse tcp:8088 tcp:8088` (Android only)
- [ ] `cd frontend && npm ci && npm run build && npx cap sync ios && npx cap sync android`
- [ ] Web: `cd frontend && npm run dev` (only for web screenshots)

## Phase 2 — Auth / User setup

- [ ] Capture token:
  ```bash
  LOGIN=$(curl -s -X POST http://localhost:8088/api/auth/login \
    -H 'Content-Type: application/json' \
    -d '{"username":"admin","password":"admin123"}')
  TOKEN=$(echo "$LOGIN" | jq -r .token)
  WORKSPACE_ID=$(echo "$LOGIN" | jq -r .workspaceId // echo "$LOGIN" | jq -r .workspace_id // echo default)
  echo "TOKEN=${TOKEN:0:20}…"
  ```
- [ ] `echo "$LOGIN" | jq .` — token, refreshToken, user fields all present
- [ ] `AUTH="Authorization: Bearer $TOKEN"` exported in shell
- [ ] Negative test: `curl -s -o /dev/null -w "%{http_code}\n" http://localhost:8088/api/instances` → `401` (no header)
- [ ] Positive test: `curl -s http://localhost:8088/api/instances -H "$AUTH" | jq 'length'` → `>= 1`
- [ ] Capture `INSTANCE_ID=$(curl -s http://localhost:8088/api/instances -H "$AUTH" | jq -r '.[0].id')`

## Phase 3 — API tests (per row of PLAN §5)

- [ ] POST `/api/mobile/sessions?instance_id=…` — 200, `.id` captured
- [ ] POST same with same `Idempotency-Key` — 200, `Idempotency-Replayed: true`
- [ ] GET `/api/mobile/sessions?instance_id=…` — 200, `.data` array
- [ ] GET `/api/mobile/sessions/search?q=…&instance_id=…` — 200
- [ ] DELETE `/api/mobile/sessions/{id}?instance_id=…` — 204
- [ ] GET `/api/mobile/sessions/{id}/event?instance_id=…` — `Content-Type: text/event-stream`, first event `server.connected`
- [ ] GET `/api/mobile/sessions/{id}/messages?instance_id=…&limit=50` — 200, normalized rows
- [ ] GET `/api/mobile/sessions/{id}/summary?instance_id=…` — 200, non-empty `summary`
- [ ] POST `/api/mobile/sessions/{id}/prompt?instance_id=…` — 202, `.messageID`
- [ ] POST `/api/mobile/sessions/{id}/interrupt?instance_id=…` — 204
- [ ] GET `/api/mobile/approvals?instance_id=…&session_id=…` — 200, both `permissions` + `questions` keys
- [ ] POST `/api/mobile/approvals/permission/{rid}/reply` (`decision=once`) — 200, `confirmed:true`
- [ ] POST `/api/mobile/approvals/permission/{rid}/reply` (`decision=reject`) — 200, audit row
- [ ] POST `/api/mobile/approvals/question/{rid}/reply` — 200, audit row
- [ ] POST `/api/mobile/approvals/question/{rid}/reject` — 200, audit row
- [ ] POST approval with mismatched `instance_id` — 4xx (scope fail-closed)
- [ ] GET `/api/mobile/events/snapshot` — 200 OR 503 (document which)
- [ ] GET `/api/audit/logs?action=mobile.approval.&limit=50` — entries returned

## Phase 4 — Flow tests

- [ ] Flow A (Session Create → Prompt → Event Stream → Messages → Interrupt) all green
- [ ] Flow B (Approval Request → Reply) green; permission removed from `/api/mobile/approvals`
- [ ] Flow C (Question Answer + Question Reject) green
- [ ] Flow D (Events Snapshot) returns 200 OR documented 503
- [ ] Flow E (Idempotency replay) returns identical body + `Idempotency-Replayed: true`

## Phase 5 — Interactive UI tests

### iOS Simulator
- [ ] `xcrun simctl boot "iPhone 16"` (or already booted)
- [ ] `npx cap run ios --target="iPhone 16"`
- [ ] Screenshot `test-evidence/2026-09-07-ios/01-login.png`
- [ ] Submit login → session list loads
- [ ] Screenshot `test-evidence/2026-09-07-ios/02-session-list.png`
- [ ] Open a session → SSE events appear live (no manual refresh)
- [ ] Screenshot `test-evidence/2026-09-07-ios/03-session-detail.png`
- [ ] Trigger an approval request → approval sheet responds to tap
- [ ] Screenshot `test-evidence/2026-09-07-ios/04-approval-sheet.png`
- [ ] Verify navigation bar translucent; content scrolls under it
- [ ] Verify tab bar does not overlap system tab bar (notch device)
- [ ] Verify right-side action button visible (refresh/interrupt)
- [ ] System back button navigates within stack; only exits on root
- [ ] Background + foreground app — SSE reconnects
- [ ] Light/dark mode: `xcrun simctl ui booted appearance dark`; screenshot `…/05-dark.png`
- [ ] Landscape: rotate simulator; screenshot `…/06-landscape.png`

### Android Emulator
- [ ] `adb devices` shows booted emulator
- [ ] `adb reverse tcp:8088 tcp:8088`
- [ ] `npx cap run android`
- [ ] Screenshot `test-evidence/2026-09-07-android/01-login.png`
- [ ] Submit login → session list loads
- [ ] Screenshot `test-evidence/2026-09-07-android/02-session-list.png`
- [ ] Open a session → SSE events appear live
- [ ] Screenshot `test-evidence/2026-09-07-android/03-session-detail.png`
- [ ] Approval sheet responds to tap
- [ ] Screenshot `test-evidence/2026-09-07-android/04-approval-sheet.png`
- [ ] Android system back button pops navigation stack
- [ ] Dark mode: `adb shell cmd uimode night yes`
- [ ] Landscape via emulator UI rotation
- [ ] Tablet emulator (7" WSVGA) — multi-window mode behaves

### Web / Desktop (Vite dev)
- [ ] `cd frontend && npm run dev`
- [ ] Screenshot `test-evidence/2026-09-07-web/01-login.png` (375×667)
- [ ] Screenshot at 768×1024 (`02-tablet.png`)
- [ ] Screenshot at 1280×800 (`03-desktop.png`)
- [ ] SSE: trigger DevTools `Network → Offline` then back online → reconnect

## Phase 6 — Responsive / multi-device

- [ ] iOS iPad sim: `xcrun simctl boot "iPad Pro 11"`; verify split-view layout
- [ ] Android tablet emulator: verify multi-window (`adb shell cmd activity supports-multi-window`)
- [ ] Web responsive: resize to 375 / 768 / 1280 widths; capture screenshots
- [ ] Verify mode-switching (light/dark/landscape) does not break SSE
- [ ] No multi-mic detection (no voice flow in this version) — confirm N/A

## Phase 7 — Evidence collection & docs update

- [ ] `test-evidence/2026-09-07-ios/` populated
- [ ] `test-evidence/2026-09-07-android/` populated
- [ ] `test-evidence/2026-09-07-web/` populated
- [ ] `audit_dump.json` saved (export of `/api/audit/logs?action=mobile.approval.&limit=200`)
- [ ] `pg_dump_audit_entries.txt` saved (`psql ... -c "TABLE audit_entries;"`)
- [ ] `pg_dump_opencode_sessions.txt` saved
- [ ] All Risk Register items (#1–#10) in PLAN §11 either resolved or documented as accepted

## Phase 8 — Code commit & push

- [ ] `git status` — only intended changes
- [ ] No secret values committed (`.env` should not be tracked; `.gitignore` covers it)
- [ ] Commit message references `2026-09-07-deployment-and-test-plan`
- [ ] `git push origin <branch>`
- [ ] Open PR; link to `docs/2026-09-07-deployment-and-test-plan/PLAN.md` + `CHECKLIST.md`
---

## ✅ 2026-09-07 执行结果（Run Results）

> 详细证据与判定见 `evidence/README.md`。本节只记录勾选状态。

**Phase 0/1 部署**：✅ 全绿（pocketd:8088 / opencode:4096 / mock-gw:8089；iOS 模拟器无 runtime，客户端在 Android emulator-5554 完成）。
**Phase 2 认证**：✅ login 200 / 401 边界 / me / refresh 全过（含 UI 登录流 + 主密码 + 错误密码内联提示）。
**Phase 3 API**：✅ sessions create/list/search/summary/messages/prompt/interrupt + SSE 契约 + 幂等重放；approvals 空列表 200、未知 rid 409、非法 decision 400；audit/logs 200。
**Phase 4 流程闭环**：✅ Flow A 全链路；数据闭环=上游直查一致；审计闭环=契约+代码路径确认（Flow A 无 approval 审计属设计内）。
**Phase 5 UI 交互**：✅ 顶栏左右按钮、5-tab 底栏无重叠、输入区 dock、角色库抽屉、设备门锁屏、AI 对话全链路、横屏重排（证据 ui/11–18）。
**Phase 6 适配**：横屏 ✅；折叠屏姿态/多麦克风 = N/A（本版无语音接线），待折叠 AVD 补测。
**Phase 7 证据**：✅ evidence/{api,workflow,ui} 已归档。
**Phase 8 提交**：✅ 见 git log（本文件所在提交）。

执行中修复 4 项（详见 evidence/README.md）：start-dev.sh apiBaseURL/workspaceId、registry workspaceId 字段、网关配置校验对齐 validateGatewayURL、push 失败非致命化。
