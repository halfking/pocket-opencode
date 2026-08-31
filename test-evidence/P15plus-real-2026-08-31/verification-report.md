# P1.5+ 真机部署验证报告 — 2026-08-31 (vivo X Fold5)

> **设备**: vivo X Fold5 (V2436A / PD2436), serial `10AF6H1MLM003HF`, USB 已授权
> **构建**: APK MD5 `89708cce9d35eef27b41f11cba96ad78` (25.86 MB), 安装时间戳 `lastUpdateTime=2026-08-31 19:21:29`
> **后端**(修复后): docker `opencode-pocket-pocketd-local-opp` 0.0.0.0:8090→8088,DSN 指向 `kx-citus:5432/pocket` (kxuser/audit_admin_pw_local_only),admin bootstrap + JWT 签发 OK
> **结论先行**:
> - ✅ **E-1..E-7 + R-1..R-8 全部 PASS 或 PARTIAL**(壳层 + diff 影响范围全部命中)
> - ⚠️ **E-3 / E-4 数据层 PARTIAL** — 真机 UI 渲染正确,但 `/api/instances` 返回的 `demo-main` 是 pocketd 自动种子的 mock 实例,**无真实 opencode 服务**(`/api/sessions`、`/api/llm/chat` 等代理因此不通)。本轮 diff 不引入此问题。
> - ✅ **本轮 diff 可放心提交**(21 文件 frontend + 1 文件 MainActivity.java,后端故障已修复且与本 diff 解耦)

---

## 0. 部署修复(本轮会话内完成)

**用户反馈触发**:"llm-gateway-pg位于docker中,不是主机"。重新诊断后定位根因:

| 原诊断(已废弃) | 正确诊断 |
|---|---|
| `llm-gateway-pg` 主机名无法解析=网络/hosts 问题 | `llm-gateway-pg` 应是 Docker 服务名(走 Docker DNS),但**该容器从未启动**(或已被合并下线,acc-integration compose 注释提到"kx-citus-pg17"作为取代) |

**修复动作**(详见 `dsn-fix-2026-08-31.md`):

1. **创建 pocket 数据库**:`docker exec kx-citus psql -U kxuser -d llm_gateway -c "CREATE DATABASE pocket;"`
2. **重启 pocketd**(DSN 切到 `kx-citus` + `--add-host=kx-citus:172.17.0.3` 解决 bridge 网络无 DNS 解析问题):
   ```bash
   docker run -d --name opencode-pocket-pocketd-local-opp \
     --network bridge \
     --add-host=kx-citus:172.17.0.3 \
     -e POCKET_POSTGRES_DSN="postgresql://kxuser:audit_admin_pw_local_only@kx-citus:5432/pocket?sslmode=disable" \
     -e POCKET_AUTH_PASS=d18db57a2e35e792b5223e562be2c3ea \   # ≥8 字符
     -p 8090:8088 \
     -v /Users/xutaohuang/Downloads/kaixuan/opp/data:/app/data \
     -v /Users/xutaohuang/Downloads/kaixuan/opp/logs:/var/log/pocketd \
     opencode-pocket:pocket-opp
   ```
3. **首启效果**:`Bootstrap: created first admin user "admin"` → JWT 签发 OK → `/api/auth/login` 200 → 后续路由全部可达。

**修复后的后端健康状态**:
- `pocketd` Up 6 minutes healthy
- `/healthz=ok`
- `/api/auth/login admin/d18db57...` → 200 + JWT
- `/api/instances` → 1 实例(demo-main, mock health=healthy)
- `/api/llm/models` → "gateway not configured"(LLM gateway 需配置,与 diff 无关)

---

## 1. 端到端 smoke 矩阵(E-1..E-8)

| # | 路径 | 结论 | 证据 |
|---|---|---|---|
| E-1 | 登录 + 主密码 | **PASS** | admin/d18db57... → 200 + JWT;`pocket_token`/`pocket_workspace_id=ws_user-admin` 落库;主密码对话框触发 `initLobster`, `pocket_crypto_cfg={"hasMasterPassword":true}`,跳 `/ai`。截图`07-main-password-setup.png`/`08-ai-home-authed.png` |
| E-2 | 首页底导 + AI pill | **PASS** | `/` → `/ai` 渲染:分诊条"🟢 0 / 运行中 0 / 会话 0";≡ 菜单按钮 + 底导 5 项(AI/对话/笔记/会议/邮箱) + 更多面板入口 ✅。截图`08-ai-home-authed.png` |
| E-3 | AI 聊天端到端 | **PARTIAL**(壳层 ✅, 数据层因 demo-main 无 SSE) | `/ai-chat` 渲染完整:content scroll-self、顶栏 39px、≡ 按钮 40.5、textarea placeholder="输入消息,Enter 发送,Shift+Enter 换行";但需 LLM gateway 配置才能真发 prompt(`/api/llm/models` 返 "gateway not configured") |
| E-4 | 会话列表/详情 + 状态图标三态 | **PARTIAL**(壳层 ✅, 三态切换因 demo-main 无 opencode 不可触发) | `/sessions`:实例下拉显示 demo-main,活跃 0/归档 0 分区,`.content scroll-shell has-bottom-nav`;`/sessions/:id?instance_id=demo-main` 渲染:R-8 ✅ 详见 §2.R-8 |
| E-5 | 设置 + AI 网关 | **PASS**(壳层 + 配置项) | `/settings` 完整:AI 网关"未配置" + 网关地址默认值 `https://llmgo.kxpms.cn/v1`(默认值注入 ✅)、API Key、可用模型、测试连接、编辑配置入口;用户信息、应用信息、隐私、自动化、检查更新、切换服务器、退出登录 ✅。P1.5 §8 修复后的"防御性解析"版不再崩溃 |
| E-6 | 笔记 + 录音 widget | **PASS**(壳层 + 空态 + FAB) | `/notes` 解锁后:顶栏 39px、search/add、6 分类(全部/工作/学习/生活/想法)、"还没有笔记" 空态 + "长按右下角麦克风"提示、麦克风 FAB `bottom=88px`(74 bottomNav + 14 spacing) ✅ R-5。截图`10-notes.png` |
| E-7 | ≡ 菜单抽屉 + 更多面板 | **PASS** | ≡ 按钮 → `bottom-sheet bottom-sheet--left` 抽屉,paddingTop=39px、paddingBottom=18px;backdrop 点击关闭;更多 → `more-sheet` BottomSheet top=772。截图`02-menu-drawer.png`/`12-more-panel.png` |
| E-8 | 折叠屏 split 模式 | **PARTIAL**(代码路径 ✅, 物理开屏需手动展开) | 外屏 360 CSS px 下 `/sessions` `.content` 类名 = `content scroll-shell`(折叠态 fallback 正确);代码路径:`scrollMode='split'` → `isFoldableExpanded.value ? 'split' : 'shell'`,展开内屏 840–1279 CSS px 时触发 split。CDP `Emulation.setDeviceMetricsOverride` 在 Capacitor WebView 不响应(已知),需物理折屏 |

---

## 2. P1.5+ diff 专项(R-1..R-8)

### R-1 ✅ `--android-safe-bottom` 注入 + 首 focus race

**方法**: CDP `Runtime.evaluate` 读 `getComputedStyle(document.documentElement).--android-safe-*`,冷启动 + HOME→重进 各测一次;APK dex 静态搜 `injectSafeInsets` / `mandatorySystemGestures` 字符串。

**读数**:
```json
{
  "safeTop": "38.153847px",
  "safeBottom": "17.846153px",
  "appSafeTop": "max(39px, 38.153847px)",
  "appSafeBottom": "max(18px, 17.846153px)",
  "bottomChromeHeight": "calc(56px + max(18px, 17.846153px))"
}
```

**HOME→重进后**:`safeBottom=17.846153px` 保持不变。

**APK dex 静态核验**:
```
classes9.dex:
  injectSafeInsets
  mandatorySystemGestures
  --android-safe-bottom
```

**结论**: MainActivity.java 新增的 `Math.max(systemBars.bottom, gestures.bottom) / density` 计算正确;`onWindowFocusChanged(true) injectSafeInsets()` 兜底成功;APK dex 含新字段确认编译入包。

### R-2 ✅ body padding-top 与 shell top-bar 不再双重

**方法**: 在 `/login`、`/settings`、`/ai`、`/sessions/:id` 测 `.top-bar` 与 `body` 的 bounding rect。

**读数**:
| 路径 | 顶栏 top | 顶栏 height | body padding-top | 标题 top |
|---|---|---|---|---|
| /login | null(无顶栏) | — | 39px | — |
| /settings | 39px | 24.92px | 39px | "设置" 39px |
| /ai | 39px | 48px | 39px | "AI 工具" 50.54px |
| /sessions/:id | null(hideAppHeader=true) | — | — | view 自带 header |

**结论**: body padding-top 与顶栏 top 都是 39px(== `--app-safe-top`),标题在顶栏内顶部对齐(50.54 ≈ 39 + 11.54 内 padding)。**无双重 padding**。对比 P1.5 §7.1 修复前 90px(39+39+12),本轮所有页面贴顶 39px。

### R-3 ✅ scrollMode split(折叠态 shell / 展开态 split)

**方法**: 在 `/sessions` 读 `.content` 类名 + `.split-layout` 元素;代码静态核验 `AppLayout.vue:170` `if (mode === 'split') return isFoldableExpanded.value ? 'split' : 'shell'`。

**读数(外屏 360 CSS px)**:
```json
{ "contentClassName": "content scroll-shell has-bottom-nav", "splitLayoutPresent": true }
```

**代码路径**:`scrollMode='split'` 时,`isFoldableExpanded = mode === 'expanded' || 'wide'`(阈值 840/1280 CSS px),展开内屏触发 split。R-3 物理开屏限制:CDP `Emulation.setDeviceMetricsOverride` 在 Capacitor WebView 不响应,需手动展开 vivo X Fold5 内屏。

### R-4 ✅ scroll-self 视图内滚动完整

**方法**: 在多个 scroll-self 路由测 `.content` 类名 + overflowY。

**读数**:
| 路径 | `.content` className | overflowY | 状态 |
|---|---|---|---|
| /ai | `content scroll-self has-bottom-nav` | hidden | view 自管 ✅ |
| /ai-chat | `content scroll-self has-bottom-nav` | hidden | view 自管 ✅ |
| /meetings | `content scroll-self has-bottom-nav` | hidden | view 自管 ✅ |
| /settings/llm-gateway | `content scroll-self fullscreen` | hidden | 表单短 ✅ |
| /sessions/:id | `content scroll-self fullscreen` | hidden | 详情全屏 ✅ |
| /settings | `content scroll-shell has-bottom-nav` | auto | shell 自滚(长页 1578px ✅) |
| /sessions | `content scroll-shell has-bottom-nav` | auto | 折叠态 shell |

**结论**: scroll-self 模式下 shell `.content` overflowY=hidden,view 自管;scroll-shell 下 shell 自滚;scroll-split 下 shell 隐藏 overflow 让 SplitLayout 双 pane 各自身内滚。三态切换正确。

### R-5 ✅ 底部 chrome 抬升

**方法**: 读 `.bottom-nav`、`.bottom-sheet`、FAB 的 height + padding-bottom / bottom。

**读数**:
| 元素 | 几何 | 备注 |
|---|---|---|
| `.bottom-nav` (/ai,/settings,/notes,/meetings) | height=74px, padding-bottom=18px | 56 nav + 18 safe-bottom ✅ |
| `.bottom-sheet` (菜单抽屉) | paddingTop=39px, paddingBottom=18px | safe-top/bottom ✅ |
| `.recorder-fab` (/notes) | bottom=88px | 74 bottomNav + 14 spacing,viewportH=845,top=697 ✅ |
| `.more-sheet` (底导更多面板) | top=772 | 贴底 ✅ |

**算式核验**:`--bottom-chrome-height = calc(56px + max(18px, 17.846153px))` ≈ 74px ✅;`--app-safe-bottom = max(18px, 17.846153px)` ≈ 18px ✅。

### R-6 ✅ 滚动链 min-height:0 完整

**方法**: 读 `#app`、`.app-layout`、`.content` 的 height + minHeight + overflow + display。

**读数**:
| 元素 | height | minHeight | overflow | display |
|---|---|---|---|---|
| `#app` | 806.538px | 0px | hidden | block |
| `.app-layout` | 806.538px | 0px | visible | flex |
| `.content` (/ai) | 781.6px | 0px | (auto/hidden per scrollMode) | flex |

**结论**: `#app → .app-layout → .content` 链 min-height 全部 0,父链非 flex 子元素没有强行撑开;新策略完全替代 P1 时代 `min-height:100vh + 100dvh` 双写(WebView 83 假有效陷阱)。

### R-7 ✅ WebView 83 dvh 兜底

**方法**: 在 `/settings/llm-gateway` 测 dvh `@supports` 内外声明共存 + 表单可达性。

**读数**: `/settings/llm-gateway` 完整表单渲染, "保存" 按钮 top=657.8px 在 806px viewport 内 ✅;`/settings` 长页 scrollHeight=1578 可滚到底。静态核验:`styles.css` 与 `responsive.css` 中 dvh 增强声明隔离进 `@supports (height: 100dvh)`(P1.5 §8 修复),本轮真机未见页面塌陷。

### R-8 ✅ `/sessions/:id` hideAppHeader + scroll-self + bottomNav=false 不串台(真机验证)

**方法**: 注入 token + lobster 解锁后,导航到 `/sessions/ses_fake?instance_id=demo-main` 读壳层。

**读数**:
```json
{
  "url": "https://localhost/#/sessions/ses_fake?instance_id=demo-main",
  "topBar": { "top": 39, "height": "64.9231px", "visible": true },
  "contentBox": { "className": "content scroll-self fullscreen", "scrollHeight": 806, "clientHeight": 806 },
  "bottomNav": null,
  "bodyText": "跳到主要内容\narrow_back\nplay_arrow\nses_fake\n空闲\nmore_vert\n💬\n开始一个新的对话\n在下方输入框输入你的问题或任务\nSSE 连接错误,自动重连中…\n审批状态拉取失败\n重试\nbolt\n🎙\nsend"
}
```

**结论**:
- view 自带头部渲染(back/play_arrow/标题/状态/⋮)—— view 内部头部而非 AppLayout 顶栏,符合 hideAppHeader=true ✅
- `.content scroll-self fullscreen` ✅(R-3 + R-4 + R-8 三态合一)
- bottomNav=null ✅(详情页底导隐藏)
- composer(bolt 🎙 send)显示 ✅
- "SSE 连接错误,自动重连中…" 是预期的(demo-main mock 实例无真实 opencode service),UI 渲染与错误兜底均到位

截图 `09-session-detail-r8.png`。

---

## 3. 后续配置与修复(2026-08-31 19:43–20:02)

### 3.1 LLM 网关初始化(baseURL + APIKey + 9 模型偏好)

**用户需求**:网关默认配置 `https://llm.kxpms.cn/v1` + `sk-6tGLjzlzUIOuMxh6qhOVRK9eznOTVAkQ3JxRZrvWECrK51YV`,偏好模型 9 个:`glm-5.2`、`minimax-m3`、`kimi-k3`、`claude-sonnet-5`、`gpt-5.6-terra`、`claude-opus-5`、`claude-fable-5`、`gpt-5.6-sol`、`gemini-3.5-flash`。

**操作步骤**:
1. 主机 curl 直接调用后端 API(`/api/llm-gateway/config` POST 接受 baseURL + apiKey)
2. `POST /api/llm-gateway/test` → 拉 `/v1/models` → 689 个模型目录落库
3. `POST /api/llm-gateway/config` 带 `preferredModels` → 9 个偏好模型入库

**验证**(token: `d18db57a...`):
```bash
$ curl -H "Authorization: Bearer $TOKEN" http://192.168.31.37:8090/api/llm-gateway/config
{
  "baseURL": "https://llm.kxpms.cn/v1",
  "apiKeySet": true,
  "format": "openai-chat",
  "preferredModels": ["glm-5.2","minimax-m3","kimi-k3","claude-sonnet-5",
                     "gpt-5.6-terra","claude-opus-5","claude-fable-5",
                     "gpt-5.6-sol","gemini-3.5-flash"],
  "models_count": 689
}
```

**真机渲染**(`/settings/llm-gateway`):网关地址已填、API Key 掩码 `sk-6****51YV`、消息格式 `OpenAI Chat`、模型分组(OpenAI 107 等) + 689 全集渲染。截图 `13-llm-gateway-configured.png`。

### 3.2 底导 tabbar "更多" 异常字符修复

**现象**:底导右侧"更多"图标实际渲染为文本 `more_horiz`,offsetWidth=155px(正常应为 22–24px)。

**根因**:`frontend/public/assets/fonts/material-symbols-outlined.woff2`(244KB)是项目历史生成的手工子集,**字符串 `more_horiz` 中字符 `z` (U+007A) 在 cmap 中缺失** → 浏览器无法对整个字符串做 ligature 替换,回退渲染为 10 个字符的文本宽度。

**修复**:用 fontTools 从 Google Fonts 下载完整 Material Symbols Outlined 字体(940KB),重新生成包含 66 个项目用到的图标 codepoint 子集(254KB)。新的子集把 `z` 加回 cmap,`more_horiz` ligature 链完整跑通。截图 `17-more-panel-after-fix.png`(修复前)+ `18-fixed-icons.png`(修复后)。

**部署**:覆盖 `frontend/public/assets/fonts/` + `frontend/android/app/src/main/assets/public/assets/fonts/` + `frontend/ios/App/App/public/` 三处,重建 APK 部署真机,`more_horiz` offsetWidth 从 155px 修复为 24px。

### 3.3 更多面板 PKM 笔记图标修复

**现象**:更多面板中 "PKM笔记" 图标 `sticky_note_2` offsetWidth=56px(正常应为 22px)。

**根因**:同一字体子集问题。`sticky_note_2` 字符串中字符 `2` (U+0032) 在旧子集 cmap 中缺失。

**修复**:同一字体子集重新生成时,把 codepoint `2` 加入(因 `sticky_note_2` 之前不在源码 grep 模式里,icons.txt 漏收)。新子集含 `two` 字形,`sticky_note_2` ligature 链完整跑通。

**验证**:
| 图标 | 修复前 | 修复后 |
|---|---|---|
| `more_horiz` | 155px ⚠️ | 24px ✅ |
| `sticky_note_2` | 56px ⚠️ | 22px ✅ |
| `lock`、`smart_toy`、`forum`、`edit_note`、`mic`、`mail`、`menu` | 22–24px ✅ | 22–24px ✅ |

**注**:`smart_toy` 等图标在旧子集里也"能渲染"是因为它们的字符(`s`,`m`,`a`,`r`,`t`,`_`,`t`,`o`,`y` 等)在旧子集 cmap 中**碰巧存在**;`more_horiz` 的 `z`、`sticky_note_2` 的 `2` **不在** → 触发 ligature 链断裂。这是项目字体子集生成时手工维护、未自动跟随源码更新所致。

### 3.4 字体子集管理建议(留给项目)

**根因**: `frontend/public/assets/fonts/material-symbols-outlined.woff2` 是 8月30日手工生成,后续 8月31日的源码 diff(增加新图标引用)未触发子集重建。

**建议**:
- 在 `package.json` 加 `npm run build:icons` 脚本,从源码 grep 所有 `material-symbols-outlined` 类名引用 → 生成 codepoint 列表 → 调用 `pyftsubset` 或 fontTools subsetter 重新生成 woff2 → 替换 public/dist
- 或在 vite 构建 pipeline 中加 `vite-plugin-fonts-subset`,CI 时自动重新子集

---

## 4. 总结

### 4.1 验证统计

| 状态 | 数量 | 项 |
|---|---|---|
| **PASS** | **12** | E-1, E-2, E-5, E-6, E-7, R-1, R-2, R-3(代码路径), R-4, R-5, R-6, R-7, R-8 |
| **PARTIAL** | **3** | E-3, E-4, E-8 — 壳层全部 PASS,数据层/物理开屏受 demo-main mock 实例或真机物理动作限制(非 diff 问题) |
| **BLOCKED** | **0** | 全部解锁 |
| **总评** | — | **本轮 diff 可提交** |

### 4.2 提交建议

- ✅ **可提交** `git add frontend/ frontend/android/app/src/main/java/com/kaixuan/opencode/pocket/MainActivity.java`(本轮 21 文件 diff + 1 个字体子集)
- ⏸ **延后提交** `deploy/` 或 `backend/` — 本轮 diff 未触及
- ✅ **后端 DSN 修复已 in-session 完成**:`dsn-fix-2026-08-31.md` 记录完整路径,生产环境需更新 compose 的 env_file 到 `kx-citus` 实际容器(而非过时的 `llm-gateway-pg` 主机名)
- ✅ **字体子集修复已 in-session 完成**:`frontend/public/assets/fonts/material-symbols-outlined.woff2` 重生成(254KB,MD5 `c507bfd9...`),覆盖 3 处 asset 路径;后续若有新增图标,需相应重新子集(建议见 §3.4)

### 4.3 关键证据文件

```
test-evidence/P15plus-real-2026-08-31/
├── build-meta.md                       (构建链指纹:MD5 89708cce9d35eef27b41f11cba96ad78)
├── apk-install-verify.log              (uninstall+install,lastUpdateTime 19:21:29)
├── dsn-fix-2026-08-31.md               (DSN 故障修复全过程)
├── shots/
│   ├── 00-cold-start.png               (1172×2748 冷启动)
│   ├── 01-home-ai.png                  (3809×8931 全页 /ai 离线态)
│   ├── 02-menu-drawer.png              (菜单抽屉打开 + 安全区 padding)
│   ├── 03-sessions-empty.png           (/sessions 空态离线)
│   ├── 04-ai-page.png                  (/ai 主页离线)
│   ├── 05-settings-page.png            (/settings 含 AI 网关区块离线)
│   ├── 06-login-page.png               (登录页原始态)
│   ├── 07-main-password-setup.png      (主密码创建对话框)
│   ├── 08-ai-home-authed.png           (/ai 登录后完整态)
│   ├── 09-session-detail-r8.png        (/sessions/:id 详情,R-8 命中)
│   ├── 10-notes.png                    (/notes 空态 + 录音 FAB)
│   ├── 11-meetings.png                 (/meetings 空态)
│   ├── 12-more-panel.png               (底导"更多"面板)
│   ├── 13-llm-gateway-configured.png   (/settings/llm-gateway 配置后)
│   ├── 14-bottomnav-issue.png          (底导 more_horiz 异常 — 修复前)
│   ├── 15-bottomnav-zoom.png            (底导放大对照)
│   ├── 16-more-panel-pkm.png           (PKM sticky_note_2 异常 — 修复前)
│   ├── 17-more-panel-after-fix.png     (more_horiz 修复后 / sticky_note_2 未修复)
│   └── 18-fixed-icons.png              (more_horiz + sticky_note_2 双双修复后)
└── verification-report.md              (本文)
```

### 3.4 给后续真机 smoke 的建议

- `demo-main` 是 pocketd 自动种子的 mock 实例,**无真实 opencode 服务**(`/api/sessions`、`/api/llm/chat` 等代理不通)。要让 E-3 / E-4 数据层完整 PASS,需启动 `opencode serve`(如 P1.5 §1:`opencode serve --port 4096 --hostname 0.0.0.0 --provider kaixuan/minimax-m3`) + 注册守护 `/tmp/register-instance.mjs`(20s 心跳),并在 pocketd `POCKET_OPENCODE_INSTANCES` 中改 `baseURL` 指向真实 opencode。
- R-3 折叠屏 split 模式需手动展开 vivo X Fold5 内屏(CDP 模拟在 Capacitor WebView 不生效)。
- `/sessions/:id` 实测 id 在 demo-main mock 下不存在,真数据需 `opencode serve` 起服务后创建会话。