/**
 * approvals.spec.ts — 审批（human-in-the-loop）E2E（web 端）。
 *
 * 覆盖点：
 *   1. 页面可达性：登录后进入「指挥中心 /ai」（TasksView，路由 meta：
 *      requiresAuth 且不 requiresLobster，浏览器可直接进入；标题「AI 工具」）。
 *      /ai 是审批的移动端聚合入口（设计方案 v2 §4.2-3，见
 *      frontend/src/features/tasks/useInstanceApprovals.ts 头注释），断言页面骨架：
 *        - AppLayout 头部标题 h1.title ← route.meta.title = 'AI 工具'
 *        - L0 分诊条按钮（triage-pill，aria-label = '全部正常' | '需要你介入'）
 *        - 「运行中」区块 + 「+ 新任务」入口
 *   2. 数据驱动：用 GET /api/mobile/approvals（frontend/src/api/approvals.ts
 *      真实路径）探测后端待审批（permissions + questions）：
 *        - 无数据：断言审批空态（无 .attention-card.approval 审批卡）——
 *          空态本身就是一条合法通过路径，测试不挂；
 *        - 有数据：跑「完整审批流程」用例（批准一条权限请求 / 回答一条问答
 *          请求），种子缺失时该用例通过 test.skip(!hasData, reason) 显式跳过。
 *
 * 选择器来源（全部出自组件源码，非猜测）：
 *   - frontend/src/app/AppLayout.vue：`<h1 class="title">{{ title }}</h1>`，title 取 route.meta.title
 *   - frontend/src/features/tasks/TasksView.vue：triage-pill（aria-label）、.triage-card、
 *     .attention-card（:class="card.type" → 'approval' | 'stalled'）、.attn-kind
 *     （'等审批' | '提问'）、按钮「✓ 批准」「✕ 拒绝」「详情」、问答候选 chips
 *   - frontend/src/api/approvals.ts：GET /api/mobile/approvals、POST …/permission/{id}/reply
 *
 * 降级策略（无种子数据 / 环境受限时绝不 flaky）：
 *   - 后端探测失败（approvals 路由未部署 / 后端不可达）→ test.skip 留注释；
 *   - 待审批为空 → 完整流程用例 test.skip，空态用例改断言空态；
 *   - 审批 UI 依赖「当前选中实例」（useInstanceApprovals.refresh() 对空
 *     instanceId 直接 no-op），有种子时用 GET /api/instances 逐实例反查
 *     所属实例并预种 localStorage（frontend/src/config/selected-instance.ts
 *     的键名），定位不到则 skip。
 *
 * 已知边界（TODO，暂不纳入 web 冒烟）：
 *   - 会话内审批面板 frontend/src/features/sessions/ApprovalPanel.vue（真实文案：
 *     「待处理审批」「权限请求」「问答请求」，按钮「批准 / 拒绝 / 回答 / 跳过」，
 *     附言 placeholder「附言（可选）」）挂在 /sessions/:id 下，该路由
 *     requiresLobster 且需真实实例 + 会话 + WS 推送，待移动端种子环境就绪后补；
 *   - 「✓ 批准 / 问答 chips」会消费一条种子审批（服务端 confirmed 后前端
 *     removeLocal 移除卡片）；种子未补充的重复运行会自动落入 skip 分支。
 */
import { expect, test } from '@playwright/test'
import {
  apiLoginToken,
  fetchInstances,
  fetchPendingApprovals,
  login,
  seedSelectedInstance,
} from '../helpers/session'

test.describe('审批（human-in-the-loop）', () => {
  // ---------------------------------------------------------------------
  // a. 页面可达性：审批聚合页（/ai 指挥中心）骨架渲染
  // ---------------------------------------------------------------------
  test('登录后进入 /ai 指挥中心，审批聚合页骨架渲染', async ({ page }) => {
    // 复用 helpers/auth.ts 的 login（密码登录 + 首次创建主密码分支，最终落达 /#/ai）
    await login(page)

    // 头部标题（AppLayout h1.title ← route.meta.title，router-mobile.ts /ai → 'AI 工具'）
    await expect(page.locator('h1.title')).toHaveText('AI 工具')

    // L0 分诊条：aria-label 二选一（TasksView :aria-label="hasAttention ? '需要你介入' : '全部正常'"）
    await expect(page.getByRole('button', { name: /^(需要你介入|全部正常)$/ })).toBeVisible({
      timeout: 15_000,
    })

    // 「运行中」区块骨架（TasksView section.running-section + 「+ 新任务」入口恒渲染）
    await expect(page.locator('.running-section .section-header h2')).toContainText('运行中')
    await expect(page.getByRole('button', { name: '+ 新任务' })).toBeVisible()
  })

  // ---------------------------------------------------------------------
  // b-1. 数据驱动 · 空态：无待审批数据时不应出现审批卡
  // ---------------------------------------------------------------------
  test('无待审批数据时，审批空态成立（无审批卡）', async ({ request, page }) => {
    const token = await apiLoginToken(request)
    const pending = await fetchPendingApprovals(request, token)
    if (!pending) {
      test.skip(true, '无法探测 GET /api/mobile/approvals（后端不可达或审批路由未部署）')
    }
    if (pending!.total > 0) {
      test.skip(true, `存在 ${pending!.total} 条待审批种子数据，空态断言不适用（由「完整审批流程」用例覆盖）`)
    }

    await login(page)

    // 空态断言（选择器出自 TasksView.vue）：
    //  - 审批卡（.attention-card.approval，L1 需介入列表）不应渲染；
    //  - 分诊条可见；文案通常为「全部正常」，但若存在「疑似卡死」任务
    //    （triage.hasAttention 的另一来源）会显示「需要你介入」，与审批无关，
    //    故此处只断言卡片缺席 + 分诊条存在，不断言具体 label。
    await expect(page.locator('.attention-card.approval')).toHaveCount(0)
    await expect(page.getByRole('button', { name: /^(需要你介入|全部正常)$/ })).toBeVisible()
  })

  // ---------------------------------------------------------------------
  // b-2. 数据驱动 · 完整审批流程：需要种子数据，缺失即显式 skip
  // ---------------------------------------------------------------------
  test('完整审批流程：处理一条待审批（权限批准 / 问答回答）', async ({ request, page }) => {
    const token = await apiLoginToken(request)
    const pending = await fetchPendingApprovals(request, token)
    // 「需要种子数据才能跑完整审批」的降级闸门：后端必须存在待审批的
    // 权限/问答请求（OpenCode permission/question pending），否则跳过并留注释。
    test.skip(
      !pending || pending.total === 0,
      '需要种子数据：GET /api/mobile/approvals 为空。请先在 OpenCode 实例中触发一个待审批的权限/问答请求（permission/question pending），再重跑本用例',
    )
    const workspacePending = pending!

    // /ai 的审批视图按实例过滤（useInstanceApprovals.refresh 的 instance_id 参数），
    // 且 UI 只读 localStorage 的选中实例（config/selected-instance.ts）。
    // 通过逐实例探测 GET /api/mobile/approvals?instance_id=… 反查种子所属实例。
    const instances = await fetchInstances(request, token)
    let seed: { id: string; displayName: string; pending: NonNullable<typeof pending> } | null = null
    if (instances.length > 0) {
      // 最多探测前 10 个实例，避免环境异常时的组合爆炸
      for (const inst of instances.slice(0, 10)) {
        const p = await fetchPendingApprovals(request, token, inst.id)
        if (p && p.total > 0) {
          seed = { id: inst.id, displayName: inst.displayName, pending: p }
          break
        }
      }
    }
    test.skip(
      !seed,
      `工作区存在 ${workspacePending.total} 条待审批，但无法通过 GET /api/instances + instance_id 过滤定位所属实例（/ai 审批视图需要选中实例才能展示），跳过 UI 流程`,
    )

    // 预种选中实例（必须在 login 之前 addInitScript），再登录进入 /ai
    await seedSelectedInstance(page, seed!.id, seed!.displayName)
    await login(page)

    // 分诊条进入「需要你介入」（triage.needsInput ≥ 实例待审批数），L1 卡片自动展开
    // （TasksView watch(triage.hasAttention) → showTriage = true）
    await expect(page.getByRole('button', { name: '需要你介入' })).toBeVisible({ timeout: 15_000 })
    await expect(page.locator('.triage-card')).toBeVisible()

    const approvalCard = page.locator('.attention-card.approval').first()
    await expect(approvalCard).toBeVisible({ timeout: 15_000 })

    if (seed!.pending.permissions > 0) {
      // ---- 权限请求：卡片文案「等审批」，内联按钮 ✓ 批准 / ✕ 拒绝 / 详情 ----
      await expect(approvalCard.locator('.attn-kind')).toHaveText('等审批')
      const approveBtn = approvalCard.getByRole('button', { name: '✓ 批准' })
      const denyBtn = approvalCard.getByRole('button', { name: '✕ 拒绝' })
      await expect(approveBtn).toBeEnabled()
      await expect(denyBtn).toBeEnabled()
      await expect(approvalCard.getByRole('button', { name: '详情' })).toBeVisible()

      // 完整流程：批准（POST /api/mobile/approvals/permission/{id}/reply，
      // confirmed 后前端 removeLocal 移除卡片——注意会消费这条种子审批）
      await approveBtn.click()
      await expect(approvalCard).toBeHidden({ timeout: 15_000 })
      return
    }

    // ---- 问答请求：卡片文案「提问」，内联候选 chips（首个选项 label 来自所属实例的探测结果）----
    await expect(approvalCard.locator('.attn-kind')).toHaveText('提问')
    const optionLabel = seed!.pending.firstQuestionOption
    test.skip(
      !optionLabel,
      '待审批只有无选项的问答请求（无法内联回答），跳过回答动作；可改在会话页 ApprovalPanel 用「跳过」处理（TODO）',
    )
    const chip = approvalCard.getByRole('button', { name: optionLabel!, exact: true })
    await expect(chip).toBeVisible({ timeout: 15_000 })
    await chip.click()
    // 回答成功（replyQuestion confirmed / 409 冲突）后卡片同样被移除
    await expect(approvalCard).toBeHidden({ timeout: 15_000 })
  })
})
