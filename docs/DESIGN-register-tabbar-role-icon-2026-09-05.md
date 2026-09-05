# 设计 — 注册流程独立页 · TabBar 收敛到 ≡ 抽屉 · 角色 chip 专家图标

> 日期：2026-09-05
> 范围：仅前端（`frontend/`）。注册核心流程（验证码签发 / 邮件 / 建号）由 RedClaw 后端实现，openpocket 前端只调用既有 `/api/auth/*` 契约；pocketd 本地实现 → RedClaw 代理的切换是后端既定路线（见 `backend/internal/server/server_auth_extended.go` 头注释），不在本设计内。

## 1. 注册流程独立页

### 现状与问题
- 登录页第 3 个 Tab「注册」与「验证码登录」Tab 共享 `codeSent / codeValue / codeCooldown` 状态，切 Tab 串状态；注册表单挤在卡片里，无分步引导。
- 后端契约已稳定：`POST /api/auth/send-code`（`purpose=register`）+ `POST /api/auth/register`（成功即返回 `{token,user,user_id,workspace_id}`，注册即登录）。`api/auth.ts` 的 `sendCode / registerUser` 已封装，不改。

### 设计
- 新增 `features/auth/RegisterView.vue`，路由 `/register`（匿名可访问，meta 同 `/forgot-password`：`bottomNav:false, showTopBar:false, canGoBack:true`）。
- 分步流程（复用 ForgotPasswordView 的 steps 视觉语言）：
  1. **验证邮箱**：邮箱输入 → 发送验证码（60s 冷却、`debug_code` 调试回显提示）→ 自动进入第 2 步；
  2. **设置账号**：验证码（6 位数字）+ 用户名（3-32 位，`[A-Za-z0-9_.-]`）+ 密码（≥8 位，含字母与数字）+ 确认密码 → 「注册并登录」；
  3. 成功后走与登录一致的 `completeAuth` 链：`setAuthWithWorkspace` → `connectWs` → 无主密码时弹 `MasterPasswordDialog` → 跳 `/ai`。
- 客户端校验规则与后端 `validateUsername / validatePassword` 一致，错误前置提示；服务端错误（409 邮箱/用户名已注册等）原样展示。
- **LoginView 收敛**：3-Tab → 2-Tab（密码登录 / 验证码登录），删除注册 Tab 及其状态；底部链接行改为「注册新账号 →」（左）与「忘记密码？」（右）。

## 2. TabBar 收敛：「更多」入 ≡ 抽屉

### 设计
- `BottomNav.vue` 只保留 5 个一级 Tab：AI / 对话 / 笔记 / 会议 / 邮箱。删除「更多」按钮、`more-sheet` 面板、下滑关闭手势及相关样式（滚动联动 chrome 上报逻辑不变）。
- `SettingsMenuDrawer.vue`（顶栏左 ≡）在「设置」与「运维与高级」之间新增「**更多功能**」组，承接原「更多」面板入口：

  | 入口 | 路由 | 图标 |
  |---|---|---|
  | PKM笔记 | `/pkm/today` | `sticky_note_2` |
  | 密码箱 | `/vault` | `lock` |
  | 技能市场 | `/marketplace/skills` | `extension` |
  | 智能体市场 | `/marketplace/agents` | `smart_toy` |
  | 工作搭子 | `/marketplace/workbuddies` | `handshake` |

- 分组标题复用既有 i18n key `nav.moreFeatures`（9 个语言文件均已存在），不新增 key。
- 各路由本体保留（深链可达），仅入口从 TabBar 收敛到抽屉；用户卡 / 设置组 / 运维组顺序不变。

## 3. 角色 chip → 专家人员图标

### 设计
- `UnifiedComposer.vue`（移动 + 宽屏两处）角色 chip：未选角色时的 `👤` emoji 换为 Material Symbols **`support_agent`**（专家人员）；已选角色时仍显示该角色的 emoji 头像（per-agent 头像语义不变）。
- `support_agent` 已确认存在于自托管字体子集（`public/assets/fonts/material-symbols-outlined.woff2`，GSUB 连字表核验）。
- 与 `AIChatView` 信息行 chip（已是 Material Symbols 图标语言）统一，消除 emoji 跨机型字形不一致问题。

## 验证
- `npm run typecheck` + `npm run build:fast`；
- dev server 渲染 `/login`（2-Tab + 注册入口）、`/register`（分步注册）截图核验；TabBar 与抽屉经 ComponentDemo/代码路径核验。
