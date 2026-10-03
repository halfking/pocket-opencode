# 图标子集：集中映射表 + 字体级实测门禁

> 日期：2026-09-30　范围：`frontend/`
> 状态：**代码与门禁已验证**；**浏览器目视已验证**；**Android 真机仍未测**（见文末「未验证」）。

## 起点

P2 收口时留了一句注释：

> 扫不到的只剩运行时才决定的动态名（{{ item.icon }} 这类），静态无解，
> 需要集中式图标映射表才能根治。

本轮把这句话做掉了，并且发现它对**两个既有脚本都不成立**——不只是「无解」，
是「解法存在但没人用」。

## 三个扫描盲区（逐个坐实）

`build-material-symbols-subset.mjs` 的采集靠两条正则：模板字面量
`material-symbols-outlined"…>([a-z_]+)<`，以及**同一行内**的插值字面量。
以下三类引用在仓库里真实存在，全部原理上抓不到：

| 盲区 | 实例 | 证据 |
|---|---|---|
| **数据表声明** `icon: 'x'` | `MoreHubView.vue:90` 起 20+ 处 | 模板只写 `{{ item.icon }}` |
| **computed / 函数早返回** | `SessionStatusBar.vue:81-83` → `notifications_active` / `progress_activity`；`ToolCallCard.vue:44-54` 整张 map 表 | 连 `icon:` 都没有，是 `const iconName = computed(() => { if (…) return '…' })` |
| **跨行插值** | `AIChatView.vue:59-61`：`{{` 与 `}}` 不在同一行 | 两个脚本都按行匹配，整条漏掉 |

`StudyHubView.vue:292` 的 `sourceIcon()` 是 `switch` + `return`，同样抓不到。

## 关键事实：现在**没有**活着的缺字

这一点必须说清楚，否则会被误读成「我又发现了 8 个豆腐块」。

写了临时探针，对**已提交的字体文件**用 harfbuzz 做 ligature 成形实测
（名字在字体里 → 整串合成 1 个字形；不在 → 退化成 N 个单字符字形，正是真机上
显示 `LIGHT_MODE` 的那个形态）：

```
真实缺字 0 个
```

`notifications_active`、`progress_activity`、`calculate`、`public`、`draft`、
`edit_document`、`folder_open`、`handyman` 全部能正常合成。

**原因**：这个「子集」几乎没裁。

| 产物 | 体积 |
|---|---|
| 上游 `material-symbols-outlined.woff2` | 3.80 MB |
| 子集产物（工作区） | 3.52 MB |
| 子集产物（HEAD） | 3.78 MB |

**只削掉 7.3%**。material-symbols 的连字由基础字形合成，121 个图标名覆盖了
a–z、0–9 和常用标点，harfbuzz 的 layout closure 会把它们全拉进来，基座削不动。

所以构建脚本文件头那句「Output: … (≈4-12 KB)」**与实际差约 300 倍**，本轮已更正。
这个脚本的价值从来不是减体积，而是**控制哪些连字存在**。

这也意味着：上面三个盲区现在是**靠巧合安全的**。字体几乎完整，所以扫不到的名字
也照样在；一旦子集真的裁小，或者有人清理 FALLBACK 里的冗余项，它们立刻变豆腐块，
而**现有两个脚本都不会报警**。

## 做了什么

### 1. `src/constants/icons.ts` —— 动态图标的唯一权威来源

57 个动态名，按来源文件分组注释。用带 key 的对象而不是裸数组，是为了拿到
`IconName` 联合类型——数据表写错名字会在 `vue-tsc` 阶段就报错，而不是等到真机。

已接入的三个组件（原先名字藏在 computed / map / switch 里）：

- `SessionStatusBar.vue` — 三态状态信号
- `ToolCallCard.vue` — 工具名 → 图标 map 表
- `StudyHubView.vue` — `sourceIcon()` 与 dueRows 数据表

构建脚本改为读这个注册表，`FALLBACK` 里那段手工维护的「仅通过数据表」名单
整段删除。**两份会漂移的清单合并成一份。**

### 2. `scripts/check-icon-font.mjs` —— 新门禁，字体级实测

这是本轮真正的牙齿。`check-icon-subset.mjs` 比对的是**名字集合**，它从没打开过
字体，所以「源码加了图标、字体没重建」这类**产物陈旧**它原理上抓不到。

新脚本打开 `material-symbols-outlined.woff2`，对每个名字做真实 ligature 成形。

两个设计要点，都是踩出来的：

**(a) 假阳性必须先滤掉。** 收集规则必然捞到非图标名——实测捞到 `starred`
（`item.status === 'starred'` 是状态比较值）和 `name`（跨行插值里的普通变量名）。
判据不能是「子集里合不出来」，得是**上游完整字体里也合不出来**才算出非图标。
上游字体是「算不算图标名」的判据，子集字体是「我们裁进去的合不合成得出来」的判据，
两件事分开。

**(b) 对照必须校验成形器，不能校验子集内容。** 早先版本把对照写死在子集字体上
（`light_mode` 必须合字）。注入一个只含 `check` 的字体时，对照先炸掉并以
exit 2 收场——**反而报不出真正的「缺 122 个」**。改成用基准字体验证成形器后，
注入实验才真正跑到了缺字报告。

**两个踩坑记录**（不写下来会重犯）：

- harfbuzz **不解 woff2**，必须先用 `fontverter` 转 sfnt。少了这步字体解析失败，
  每个字符退成 `.notdef`，glyph 数恰好等于字符数——看起来像「全部缺字」的假象。
  我第一版就中了这个招，阳性对照全灭才反应过来。
- HarfBuzz **默认不开 `dlig`**，material-symbols 的连字必须显式
  `['liga','dlig','calt','rlig','ccmp']`，否则一个都合不出来。

### 3. 接线

`fontverter` 补进 `devDependencies`（此前只靠 `subset-font` 的传递依赖，
直接 import 传递依赖是脆的）。新增 `npm run check:icons` / `npm run build:icons`，
并把 `check:icons` 接进 `gates`。门禁耗时 1.8 秒。

### 4. 目视验证页（补上「字形画出来对不对」这一半）

harfbuzz 只能回答「有字形」，回答不了「字形画出来对不对」。所以补了
`scripts/build-icon-harness.mjs`（`npm run check:icons:visual`），
把工程引用到的**全部**图标名各渲染一格，用的是真实的子集字体文件，
产物是自带字体的独立 HTML，浏览器直接打开即可，拷到手机也能看。

**实测（Chromium，会话内置 Browser）**：125 个名字逐格渲染，
**123 个是图形，2 个显示为文本**——而这 2 个恰好就是 harfbuzz 门禁判定的
那 2 个收集规则假阳性：

| 名字 | 渲染结果 | 真相 |
|---|---|---|
| `name` | 字面文本 `NAME` | 跨行插值捞到的普通变量名，不是图标 |
| `starred` | 星形 + 残留 `RED` | `item.status === 'starred'` 的比较值，连字匹配到 `star` 就停了 |

**两个独立方法（harfbuzz 成形 vs 浏览器真实排版）给出完全一致的结论。**
这比任何单一检查都强，也顺带说明了为什么「非图标误报」这一类必须滤掉——
否则它们会以「缺字」的形式混进报告里，稀释真正的问题。

产物目录已进 `.gitignore`（含 3.5 MB 字体拷贝，不该进版本库）。

## 验证记录

| 验证项 | 方法 | 结果 |
|---|---|---|
| 注册表解析生效 | `build:icons` 输出 `ICON 注册表并入 57 个动态图标名` | ✅ |
| 端到端 | 注册表 → 重建字体到临时文件 → 对新字体跑门禁 | ✅ 136 图标 / 3477.7 KB，门禁 exit 0 |
| 门禁有牙齿 | 注入只含 1 个图标的字体（`POCKET_ICON_FONT`） | ✅ 报 106 个缺字，exit 1 |
| fail-closed | 破坏注册表语法后跑 `build:icons` | ✅ exit 2，未产出字体 |
| 类型承重 | `npx vue-tsc --noEmit` | ✅ exit 0 |
| 现有门禁未回归 | `check-icon-subset.mjs` | ✅ exit 0 |
| 已提交字体仍合规 | `check-icon-font.mjs` | ✅ 123 个真实图标名全部可合成 |
| **目视渲染** | Chromium 打开 harness 页逐格核对 | ✅ 123 图形 / 2 文本，与 harfbuzz 判定一致 |

> 两个 `POCKET_ICON_*` 环境变量（`POCKET_ICON_FONT` 指定被检字体、
> `POCKET_ICON_FONT_OUT` 指定产物路径）就是为上面的注入实验加的，
> 留着以后排障和回归用。

## 未验证

- **Android 真机仍未测。** 本轮的目视验证是在 **Chromium**（桌面 Electron）里做的。
  需要说清楚哪些结论可以外推、哪些不能：
  - ✅ **可以外推**：连字**存不存在**是字体文件的属性，与渲染平台无关。
    123 个图形在手机上同样会是图形——这一点由 harfbuzz 的字体级实测独立保证。
  - ❌ **不能外推**：实际观感。字号、抗锯齿、深色模式下的对比度、
    在真实页面布局里的对齐，这些只有设备能确认。
  - 20 个此前补回的图标仍建议在**手机**上看一眼设置页主题三选项。
    `npm run check:icons:visual` 生成的自包含 HTML 拷到手机即可直接核对。
- 未改 `dist/` 产物（那要跑真实 vite build）。
- `check-icon-font.mjs` 依赖 `node_modules/material-symbols` 作为基准；
  该包若被移除，门禁会以 exit 2 拒绝下结论而不是误报通过。
