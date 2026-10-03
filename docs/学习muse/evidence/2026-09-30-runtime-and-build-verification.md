# 运行时与构建产物验证（此前从未做过的一类）

> 日期：2026-09-30　范围：`frontend/`
> 状态：**已验证**。这一类此前被笼统归入「端到端未做」，实际是可以拆开做的——
> 本文记录拆开后的边界：**哪些做了、哪些仍然做不到，以及为什么。**

## 为什么要做

此前的验证栈是：`vue-tsc --noEmit`（类型）+ 若干 `node scripts/*.mjs`（静态门禁）。
两者都**不运行代码**。留下的空白有三块：

1. SFC 模板能否真正编译（vue-tsc 不做完整模板编译）；
2. 打包能否成功、产物里到底装了什么；
3. 产物在浏览器里能否启动。

这三块都不需要数据库、不需要手机，此前却和「真库端到端」混在一起被标成「未做」。

## 1. Dev server 启动

`npx vite --port 4174` 起来后打开根路径：

- 应用正常启动，**自动重定向到 `#/login?returnTo=/ai`**（鉴权守卫按预期生效）；
- **console 只有 1 条 error**：`Failed to check update`
  （`UpdateChecker.vue:18` → `utils/version.ts:24`）。
  这是**没有后端**导致的预期失败，不是渲染期崩溃。
- 无 Vue warning、无未捕获异常。

> 没有后端 ⇒ 登录无从谈起。**没有尝试任何凭据**。
> 即便有凭据也没有可登录的后端（PG 不可用），所以这一条止步于登录墙。

## 2. 生产构建

构建是**故意 fail-closed** 的：没有 `VITE_API_BASE` 就直接拒绝，理由写在配置里
（移动端 bundle 缺它会静默回落到 WebView 同源，`/api` 全部返回 index.html 而非 JSON）。
这设计是对的，不该绕。仓库文档给了 Web 同源部署的正式开关
`MOBILE_ALLOW_EMPTY_API_BASE=1`，用它做纯编译验证：

```
✓ built in 21.50s          build_exit=0
```

产物覆盖了全部路由 chunk，其中与本轮交付直接相关的：

| 产物 | 大小 |
|---|---|
| `StudyHubView-*.js` | 10.3 KB |
| `TaskDetailView-*.js` | 18.0 KB（协作面板在此） |
| `LocalAgentView-*.js` | 42.7 KB（ToolCallCard 在此） |
| `SessionConversationView-*.js` | 72.1 KB（SessionStatusBar 在此） |

## 3. 动态图标名确实进了生产 bundle

这是本轮最有价值的一条。`notifications_active`、`progress_activity`、`handyman`、
`brightness_auto` —— 这些名字**任何静态扫描器都追不到**（它们藏在 computed / map 表 /
数据表里），此前也从没人验证过它们是否真的活到了产物中。

在 `dist/assets/*.js` 里逐个搜：4 个全部命中。

即：`constants/icons.ts` 注册表经过 tree-shaking 与 minify 之后，
**动态引用的图标名一个都没丢**。这补上了「注册表只是文档」这个风险。

## 4. 字体在产物里可用

| 检查 | 结果 |
|---|---|
| 产物字体 SHA256 vs 源字体 | **完全一致** |
| 产物字体 HTTP 可达（`vite preview`） | **200** |

链路闭合：源字体（已被 harfbuzz 门禁与浏览器逐格渲染双重验证）
→ `dist` 产物（字节相同）→ 实际可取回。

**一处未达成**：在浏览器里查 network 时**没有任何 font 请求**。
原因是登录页不含 `material-symbols` 元素，浏览器按需加载字体，所以没触发。
这是正确行为，但也意味着「浏览器实际取用产物字体」这一步没能在真实应用里观察到——
它被登录墙挡住了。上面用 SHA256 + HTTP 200 作为等价的间接证据。

## 验证记录

| 项 | 方法 | 结果 |
|---|---|---|
| 应用启动 | `vite` dev server + 浏览器 | ✅ 正常启动并重定向到登录 |
| 启动期报错 | console query | ✅ 仅 1 条预期错误（无后端导致） |
| 生产构建 | `vite build`（正式开关） | ✅ 21.5s，exit 0 |
| 动态图标名入产物 | `dist/assets/*.js` 搜索 | ✅ 4/4 命中 |
| 字体入产物 | SHA256 比对 | ✅ 一致 |
| 字体可取回 | HTTP HEAD | ✅ 200 |
| 产物可启动 | `vite preview` + 浏览器 | ✅ 正常启动 |
| 浏览器实际取用产物字体 | network query | ❌ **未达成**（登录页无图标元素，字体按需加载未触发） |

验证完关闭了 dev/preview server，端口已释放；`dist/` 与 `.icon-harness/` 均在
`.gitignore` 内，工作区未被本轮验证污染。

## 仍然未验证（不要被上面的绿灯误导）

- **学习中心 / 任务详情 / 协作面板的真实渲染**：全部在登录墙之后。
  编译通过 ≠ 运行时渲染正确。`dueRows` 之类的 computed 在真实数据下的分支、
  `TaskCollaborationPanel` 的 `available=false` 隐藏分支，**一行都没跑过**。
- **浏览器实际取用产物字体**（见上表最后一行）。
- **Android 真机观感**、**真实 Postgres**、**APNs/FCM**。

这一轮的结论是：**编译期与产物期的空白补上了，运行时渲染的空白没有。**
后者需要后端，而后者需要 Postgres。
