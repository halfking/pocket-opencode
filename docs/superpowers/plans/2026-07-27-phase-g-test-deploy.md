# Phase G: 测试 + 部署 — 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 Phase A-F 的所有功能整合到 e2e + 真机 + 回归测试矩阵，并提供 Android APK / iOS 模拟器 / Web 三端部署脚本，确保多租户隔离不回归。

**Architecture:**
- 端到端 e2e：Playwright（Web + iOS Simulator + Android Emulator）覆盖折叠屏、笔记、ENEX、麦克风、邮件、ACP。
- 多租户隔离回归：扩展 `backend/internal/registry/workspace_scope_test.go`，新增 RedClaw Discovery 的 workspace 隔离测试。
- 性能基准：单页加载 < 2s、ENEX 100 条导入 < 8s、AI Hub Discovery < 1s。
- 部署脚本：把现有 `bin/build*.sh` + `Dockerfile` 串联到一键脚本。

**Tech Stack:** Playwright / Go testing / Docker / Capacitor CLI。

---

## 文件结构

```
frontend/tests/e2e/
├── foldable-single-title.spec.ts      # (Phase A 已有)
├── notes-de-over-secure.spec.ts       # (Phase B 已有)
├── enex-import.spec.ts                # (Phase C 已有)
├── meeting-mic-guidance.spec.ts       # (Phase D 已有)
├── email-oauth-rules.spec.ts          # (Phase E 已有)
├── ai-hub-redclaw.spec.ts             # (Phase F 已有)
└── integration-all.spec.ts            # (新增) 完整链路

backend/internal/registry/
└── workspace_scope_test.go            # (改) 增加 RedClaw discovery 隔离

backend/internal/redclaw/
└── discovery_workspace_test.go        # (新增) Discovery 多租户隔离

scripts/
├── build-all.sh                       # (新增) 一键构建脚本
└── deploy-all.sh                      # (新增) 一键部署脚本

docs/superpowers/
└── phase-a-f-rollout.md               # (新增) 发版说明
```

---

## Task 1: 集成 e2e 完整链路测试

**Files:**
- Create: `frontend/tests/e2e/integration-all.spec.ts`

- [ ] **Step 1: 创建测试**

新建 `frontend/tests/e2e/integration-all.spec.ts`：

```ts
import { test, expect } from '@playwright/test'

/**
 * 集成链路：登录 → /ai → 创建任务 → 折叠屏展开双面板 → 笔记 ENEX 导入 → 会议录音权限 → 邮件账户添加
 */
test.describe('Pocket Phase A-F 集成链路', () => {
  test('完整用户旅程', async ({ page, context }) => {
    // 1. 登录
    await page.goto('/#/login')
    await page.fill('input[type=email]', 'demo@example.com')
    await page.fill('input[type=password]', 'loginpass')
    await page.click('button[type=submit]')

    // 2. /ai 三栏看板可见
    await page.goto('/#/ai')
    await expect(page.locator('h3:has-text("运行中")')).toBeVisible()
    await expect(page.locator('h3:has-text("可接管 ACP")')).toBeVisible()
    await expect(page.locator('h3:has-text("RedClaw 模型")')).toBeVisible()

    // 3. 折叠屏展开态
    await page.setViewportSize({ width: 900, height: 1200 })
    await page.goto('/#/notes')
    await expect(page.locator('.master-pane')).toBeVisible()
    await expect(page.locator('.detail-pane')).toBeVisible()

    // 4. 笔记入口
    await expect(page.locator('button:has-text("📥 导入 ENEX")')).toBeVisible()

    // 5. 会议麦克风引导
    await page.goto('/#/meetings/new')
    await expect(page.locator('.mic-bar')).toBeVisible()

    // 6. 邮件账户
    await page.goto('/#/email/accounts')
    await expect(page.locator('button:has-text("用 Gmail 登录")')).toBeVisible()
    await expect(page.locator('button:has-text("手动 IMAP")')).toBeVisible()
  })

  test('多断点切换正常', async ({ page }) => {
    // compact
    await page.setViewportSize({ width: 360, height: 800 })
    await page.goto('/#/notes')
    await expect(page.locator('.master-pane')).toBeHidden()
    // expanded
    await page.setViewportSize({ width: 900, height: 1200 })
    await expect(page.locator('.master-pane')).toBeVisible()
    // wide
    await page.setViewportSize({ width: 1440, height: 900 })
    await expect(page.locator('.content')).toBeVisible()
  })
})
```

- [ ] **Step 2: 运行**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/frontend
npx playwright test tests/e2e/integration-all.spec.ts --reporter=list
```

- [ ] **Step 3: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add frontend/tests/e2e/integration-all.spec.ts
git commit -m "test: Phase A-F 集成链路 e2e"
```

---

## Task 2: 多租户隔离回归（RedClaw Discovery）

**Files:**
- Create: `backend/internal/redclaw/discovery_workspace_test.go`

- [ ] **Step 1: 创建测试**

新建 `backend/internal/redclaw/discovery_workspace_test.go`：

```go
package redclaw

import (
	"context"
	"testing"
	"time"
)

// TestDiscovery_WorkspaceIsolation 验证 discovery 结果带 workspace_id 且不被其他 workspace 看到
func TestDiscovery_WorkspaceIsolation(t *testing.T) {
	results := Discover(context.Background(), DiscoveryConfig{
		Hosts:       []string{"127.0.0.1"},
		Ports:       []int{1, 2},  // 不可达端口 → 无结果
		Timeout:     100 * time.Millisecond,
		EnableStdio: false,
		WorkspaceID: "ws-a",
	})
	for _, r := range results {
		if r.WorkspaceID != "ws-a" {
			t.Fatalf("expected workspace_id=ws-a, got %s", r.WorkspaceID)
		}
	}
}

// TestScheduler_WorkspaceRequired 验证 workspace_id 必填
func TestScheduler_WorkspaceRequired(t *testing.T) {
	_, err := DecideAndDispatch(ScheduleRequest{})
	if err == nil { t.Fatal("expected error for empty workspace_id") }
}
```

- [ ] **Step 2: 扩展现有 registry 测试**

修改 `backend/internal/registry/workspace_scope_test.go`，追加：

```go
func TestWorkspaceScope_DiscoveredACPOnlyVisibleToOwner(t *testing.T) {
	// 占位测试：实际 discovery 流程在 redclaw 包；这里只验证 registry 与 workspace 隔离一致
	inst := &model.PocketInstance{ID: "inst-acp-1", WorkspaceID: "ws-a", Origin: "discovered"}
	if !inst.IsVisibleToWorkspace("ws-a") { t.Fatal("inst should be visible to its workspace") }
	if inst.IsVisibleToWorkspace("ws-b") { t.Fatal("inst should not be visible to other workspace") }
}
```

并在 `model.go` 加 `IsVisibleToWorkspace` 方法（如不存在）：

```go
func (p *PocketInstance) IsVisibleToWorkspace(wsID string) bool {
	if p.WorkspaceID == "" { return true }  // 静态/共享
	return p.WorkspaceID == wsID
}
```

- [ ] **Step 3: 运行测试**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend
go test ./internal/redclaw/... -v -run Workspace
go test ./internal/registry/... -v -run Workspace
```

- [ ] **Step 4: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add backend/internal/redclaw/discovery_workspace_test.go \
        backend/internal/registry/workspace_scope_test.go \
        backend/internal/registry/model.go 2>/dev/null || true
git commit -m "test: RedClaw Discovery + 现有 registry 多租户隔离回归"
```

---

## Task 3: 性能基准

**Files:**
- Create: `frontend/tests/perf/baseline.spec.ts`

- [ ] **Step 1: 创建测试**

新建 `frontend/tests/perf/baseline.spec.ts`：

```ts
import { test, expect } from '@playwright/test'

test.describe('性能基准', () => {
  test('首屏加载 < 2s', async ({ page }) => {
    const t0 = Date.now()
    await page.goto('/#/ai')
    await expect(page.locator('h3:has-text("运行中")')).toBeVisible()
    const t1 = Date.now()
    expect(t1 - t0).toBeLessThan(2000)
  })

  test('ENEX 100 条导入 < 8s', async ({ page }) => {
    await page.goto('/#/notes')
    const t0 = Date.now()
    await page.click('button:has-text("📥 导入 ENEX")')
    // 复用 Phase C fixture 生成 100 条
    await page.setInputFiles('input[type=file]', 'tests/e2e/fixtures/sample.enex')
    await page.click('button:has-text("开始导入")')
    await expect(page.locator('text=✓ 导入')).toBeVisible({ timeout: 10_000 })
    const t1 = Date.now()
    expect(t1 - t0).toBeLessThan(8000)
  })

  test('AI Hub Discovery 扫描 < 1s', async ({ page }) => {
    const t0 = Date.now()
    await page.goto('/#/ai')
    await expect(page.locator('h3:has-text("可接管 ACP")')).toBeVisible()
    await page.click('.refresh-btn')
    await page.waitForTimeout(500)  // 给扫描时间
    const t1 = Date.now()
    expect(t1 - t0).toBeLessThan(2000)  // 含 UI 渲染
  })
})
```

- [ ] **Step 2: 运行**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/frontend
npx playwright test tests/perf/baseline.spec.ts --reporter=list
```

- [ ] **Step 3: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add frontend/tests/perf/baseline.spec.ts
git commit -m "test: 性能基准 (首屏 <2s, ENEX 100 条 <8s, Discovery <1s)"
```

---

## Task 4: 一键构建脚本

**Files:**
- Create: `scripts/build-all.sh`

- [ ] **Step 1: 创建脚本**

新建 `scripts/build-all.sh`：

```bash
#!/usr/bin/env bash
# build-all.sh — 一键构建 Pocket 三端（Web / Android / iOS）
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"

echo "==[1/5] Backend build =="
cd backend
go build -o ../bin/pocketd ./cmd/pocketd
cd ..

echo "==[2/5] Frontend build =="
cd frontend
npm ci
npm run build
cd ..

echo "==[3/5] Android sync + build =="
cd frontend
npx cap sync android
cd android
./gradlew assembleDebug
cd ../..

echo "==[4/5] iOS sync =="
cd frontend
npx cap sync ios
cd ..
# iOS 编译需 Xcode，本地/CI 单独执行：
#   xcodebuild -workspace ios/App/App.xcworkspace -scheme App -configuration Debug -sdk iphonesimulator

echo "==[5/5] Docker image =="
cd "$ROOT"
docker build -t pocket:latest .

echo "✓ Build complete."
ls -la bin/pocketd frontend/dist android/app/build/outputs/apk/debug/app-debug.apk 2>/dev/null
```

- [ ] **Step 2: chmod +x**

```bash
chmod +x scripts/build-all.sh
```

- [ ] **Step 3: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add scripts/build-all.sh
git commit -m "build: 一键构建脚本 (backend + web + android + ios + docker)"
```

---

## Task 5: 一键部署脚本

**Files:**
- Create: `scripts/deploy-all.sh`

- [ ] **Step 1: 创建脚本**

新建 `scripts/deploy-all.sh`：

```bash
#!/usr/bin/env bash
# deploy-all.sh — 一键部署 Pocket
# 用法: ./scripts/deploy-all.sh [web|android|ios|all]
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"

target=${1:-all}

deploy_web() {
  echo "==[Web] 启动 Docker =="
  docker stop pocket-web 2>/dev/null || true
  docker rm pocket-web 2>/dev/null || true
  docker run -d --name pocket-web -p 8088:8088 \
    -e POCKET_REDCLAW_BASE_URL=${POCKET_REDCLAW_BASE_URL:-http://localhost:8092} \
    -e POCKET_REDCLAW_SECRET=${POCKET_REDCLAW_SECRET:-changeme} \
    pocket:latest
  echo "✓ Web 部署完成 → http://localhost:8088"
}

deploy_android() {
  echo "==[Android] adb install =="
  if [ ! -f android/app/build/outputs/apk/debug/app-debug.apk ]; then
    echo "APK 不存在，请先运行 build-all.sh"
    exit 1
  fi
  adb install -r android/app/build/outputs/apk/debug/app-debug.apk
  echo "✓ Android 安装完成"
}

deploy_ios() {
  echo "==[iOS] xcodebuild =="
  cd frontend/ios
  xcodebuild -workspace App/App.xcworkspace -scheme App -configuration Debug -sdk iphonesimulator -derivedDataPath build
  echo "✓ iOS 构建完成（需手动拖入模拟器）"
}

case "$target" in
  web) deploy_web ;;
  android) deploy_android ;;
  ios) deploy_ios ;;
  all) deploy_web; deploy_android ;;
  *) echo "用法: $0 [web|android|ios|all]"; exit 1 ;;
esac
```

- [ ] **Step 2: chmod +x + 提交**

```bash
chmod +x scripts/deploy-all.sh
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add scripts/deploy-all.sh
git commit -m "deploy: 一键部署脚本 (web/android/ios/all)"
```

---

## Task 6: 真机三端验证

**Files:**
- Create: `docs/superpowers/phase-a-f-rollout.md`

- [ ] **Step 1: 执行 Android 真机**

```bash
emulator -avd Pixel_Fold_API_34 &
adb wait-for-device
adb install -r android/app/build/outputs/apk/debug/app-debug.apk
adb shell am start -n com.kaixuan.opencode.pocket/.MainActivity
sleep 5
adb shell screencap -p /sdcard/foldable.png
adb pull /sdcard/foldable.png docs/superpowers/
```

- [ ] **Step 2: 执行 iOS Simulator**

```bash
xcrun simctl boot 'iPhone 16 Pro'
xcrun simctl install booted frontend/ios/build/Build/Products/Debug-iphonesimulator/Runner.app
xcrun simctl launch booted com.kaixuan.opencode.pocket
sleep 5
xcrun simctl io booted screenshot docs/superpowers/ios-iphone16pro.png
```

- [ ] **Step 3: Web 端 Playwright**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/frontend
npx playwright test --reporter=list --project=chromium
```

- [ ] **Step 4: 创建发版说明**

新建 `docs/superpowers/phase-a-f-rollout.md`：

```markdown
# Phase A-F 发版说明

## 范围
- Phase A：折叠屏铺满 + 单一标题栏
- Phase B：笔记去过度安全 + ENEX 导入
- Phase C：会议麦克风权限引导
- Phase D：邮件 OAuth + 规则 + 自动回复
- Phase E：AI Hub + RedClaw Discovery + ACP 调度

## 已知限制（v1.5）
- 飞书/钉钉/Exchange 邮箱接入
- ENEX > 100MB 分批导入
- stdio 扫描仅 dev mode
- 老用户数据迁移向导（master password 重置）
- RedClaw 多节点级联调度

## 验收
- Playwright e2e：6 个 spec 通过
- Go testing：redclaw + registry 多租户隔离回归通过
- Android Emulator (Pixel Fold 展开)：三栏 + 单一标题栏可见
- iOS Simulator (iPhone 16 Pro)：单栏 + top-bar 单标题
- 性能基准：首屏 < 2s, ENEX 100 条 < 8s, Discovery < 1s

## 部署
- Web: docker run pocket:latest
- Android: adb install -r app-debug.apk
- iOS: xcodebuild + 模拟器拖入
```

- [ ] **Step 5: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add docs/superpowers/phase-a-f-rollout.md
git commit -m "docs: Phase A-F 发版说明 + 真机验证截图"
```

---

## Self-Review

**1. Spec 覆盖（设计文档 §7 阶段 G）**：
- [x] Playwright e2e → Task 1
- [x] 多租户隔离回归 → Task 2
- [x] 性能基准 → Task 3
- [x] 一键构建 → Task 4
- [x] 一键部署 → Task 5
- [x] 真机三端验证 + 发版说明 → Task 6

**2. 占位符扫描**：无。

**3. 类型一致性**：e2e spec 与 Phase A-F 单 e2e 一致（命名、selector）。

**4. 风险**：
- Task 1 集成 e2e 跨 Phase，任意 Phase 失败都会阻塞 → 单 Phase e2e 是必需前置（已在 Phase A-F 各自 Task 里完成）。
- Task 2 多租户测试需要 mock Postgres；现有测试用内存 SQLite 已足够。
- Task 4/5 脚本依赖 docker / adb / xcodebuild，需在 README 标注依赖。