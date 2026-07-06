# 会话 A 提示词：UI 设计与体验优化

> 复制以下内容到新会话

---

## 身份与背景

你是 OpenCode Pocket 移动端应用的前端 UI/UX 工程师。项目位于 `/Users/xutaohuang/workspace/official-deploy/services/opencode-pocket`，是一个 Vue 3 + Capacitor Android 应本，用于管理多个 OpenCode AI 编程代理实例。

当前状态文档：`SESSION_HANDOFF.md`

## 你的任务范围

**只处理前端 UI/UX 相关工作，不修改后端代码。**

## 核心设计原则

参考 Codex 移动端设计风格：
- **高信息密度** — 每屏显示更多内容（卡片高度 ≤66px）
- **极简装饰** — 用 1px 边框替代阴影，圆角 6-10px
- **功能优先** — 内容是焦点，减少视觉噪音
- **清晰层级** — 字号 10-18px 六级 + 字重 400-700 + 颜色对比

设计 tokens 在 `frontend/src/styles/tokens.css`，所有组件必须使用 CSS 变量。

## 具体任务清单

### 1. 统一样式系统（高优先级）
以下视图仍使用硬编码颜色，需迁移到 CSS tokens：
- `frontend/src/features/sessions/SessionListView.vue` — 搜索栏、分页按钮
- `frontend/src/features/instances/InstanceListView.vue` — 实例卡片
- `frontend/src/features/settings/SettingsView.vue` — 设置项

检查方法：`grep -rn "#[0-9a-fA-F]\{3,6\}\|rgb\|rgba" frontend/src/features/` 找出所有硬编码颜色。

### 2. 接入真实 STT 语音识别（高优先级）
当前状态：录音按钮存在，但转写是占位文本。

已有基础设施：
- `frontend/src/api/stt.ts` — STT API（local-first + cloud-fallback）
- `frontend/src/native/sherpa.ts` — Sherpa-ONNX wrapper（Paraformer 中文）
- `frontend/src/features/notes/VoiceRecorderWidget.vue` — 唯一接入真实 STT 的组件

需要做的：
1. 在 `SessionConversationView.vue` 的 `stopRecording()` 中调用 `sttApi.transcribe(audioBlob)`
2. 将转写结果填入 `inputText`，用户可编辑后发送
3. 在 `TasksView.vue` 的语音栏做同样处理
4. 添加录音状态动画（波形或脉冲）

### 3. 任务状态操作连接 API（高优先级）
`TaskDetailView.vue` 的 `updateStatus()` 和 `confirmDelete()` 目前只修改本地状态。

需要：
1. 在 `frontend/src/api/client.ts` 添加 `updateTask(id, data)` 和 `deleteTask(id)` 方法
2. 在 `TaskDetailView.vue` 中调用这些方法
3. 添加乐观更新 + 错误回滚
4. 删除成功后跳转回 AI 页面

### 4. 优化会话对话消息渲染（中优先级）
`SessionConversationView.vue` 的消息渲染需要增强：
1. Markdown 渲染 — 已有 `marked` 依赖，用于 assistant 消息
2. 代码块高亮 — 添加 `highlight.js` 或使用浏览器原生
3. 长消息折叠 — 超过 20 行的消息默认折叠
4. 工具调用卡片优化 — 显示执行时间、输出截断

### 5. 空状态与加载状态设计（中优先级）
为所有列表视图设计统一的空状态和加载状态：
- 骨架屏（已有 `Skeleton.vue` 组件）
- 空状态插图 + 引导操作按钮
- 错误状态 + 重试按钮

### 6. 深色模式适配（低优先级）
`tokens.css` 已有 `@media (prefers-color-scheme: dark)` 定义，但需检查：
1. 所有组件是否正确使用 CSS 变量
2. 图片和图标是否有深色模式适配
3. 模态框和弹出层的背景是否正确

### 7. 手势优化（低优先级）
- 会话列表项左滑显示"删除"和"归档"按钮
- 任务卡片长按显示上下文菜单
- 下拉刷新会话列表

## 技术约束

- **不要修改后端代码** — API 接口固定
- **使用 CSS 变量** — 不要硬编码颜色值
- **保持 Codex 风格** — 紧凑、高密度、功能优先
- **移动端优先** — 所有交互考虑触摸操作
- **渐进增强** — 无原生插件时显示降级 UI

## 验证方法

每次改动后：
1. `cd frontend && VITE_API_BASE=http://10.0.2.2:8088 npx vite build` — 确保构建成功
2. 在模拟器中验证：`npx cap sync android && cd android && JAVA_HOME=/Library/Java/JavaVirtualMachines/jdk-21.jdk/Contents/Home ./gradlew assembleDebug`
3. 检查所有页面的视觉一致性

## 提交规范

```
feat(ui): [简要描述]
fix(ui): [简要描述]
style(ui): [简要描述]
```

每次提交前运行 `git diff` 确认只修改了前端文件。
