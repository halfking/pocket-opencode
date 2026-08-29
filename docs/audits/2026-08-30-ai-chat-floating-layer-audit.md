# AIChat 浮层统一审计（2026-08-30）

## 范围

本阶段审计并修复 AIChatView 的 L2 浮层实现，目标是让会话抽屉、模型选择、优化模型、对话参数统一复用公共 `BottomSheet`。

## 发现与修复

- AIChatView 原有 `.drawer-mask/.drawer` 自绘左侧抽屉，已迁移为 `BottomSheet placement="left"`。
- AIChatView 原有 3 组 `.sheet-mask/.sheet` 自绘底部浮层，已迁移为 `BottomSheet`。
- 原有浮层自己处理 z-index、遮罩、动画和 safe-area，已由公共基座统一处理；业务组件仅保留列表与表单样式。
- `goToLibrary()` 原先直接写 `window.location.hash`，已改为 `router.push('/agents')`。
- 审计发现 `MeetingMetaSheet.vue` 与 `SpeakerLabelSheet.vue` 仍使用 `:open` 旧 API；BottomSheet 增加兼容 `open` prop，避免非本阶段调用方回归。
- BottomSheet 新增 `role="dialog"`、`aria-modal`、Esc 关闭、打开时焦点归位和 `prefers-reduced-motion` 支持。

## 静态核对

- AIChatView 不再包含 `.drawer-mask`、`.sheet-mask`、`.sheet-head` 或 `window.location.hash`。
- `document.body.style.overflow` 仅存在于 `useBodyScrollLock.ts`。
- 本阶段未删除、覆盖或暂存其他人的 scheduled-tasks/backend 修改，也未处理未跟踪的 `docs/audits/2026-08-29-chatagent-handoff-audit.md`。

## 验证

- `npm run typecheck`：通过。
- `npm run build`：通过；仅保留既有 chunk size warning。
- Node contract tests：覆盖 body scroll lock 引用计数与 BottomSheet placement/accessibility 契约。

## 遗留

- i18n 仍需独立分层迁移，避免与浮层重构混合产生大范围冲突。
- UpdateChecker、AIChatView 其他业务富交互区域和死代码清理保留为后续阶段。
