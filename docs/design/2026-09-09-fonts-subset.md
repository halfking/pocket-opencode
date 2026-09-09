# 字体子集 + 去 Google Fonts 死链 + CJK fallback 决策记录

| 字段 | 值 |
|------|----|
| 日期 | 2026-09-09（提交 `aae01a7`，main） |
| 范围 | `frontend/scripts/subset-material-symbols.sh` · `frontend/src/assets/fonts/material-symbols-outlined.woff2` · `frontend/index.html` · `frontend/src/styles.css` · `frontend/src/app/App.vue` |

---

## 1. 背景问题

1. 动态绑定图标（如 `:icon="IconRefresh"`）在图标字体里没有对应字形，渲染 fallback 报错；
   底部导航曾显示原文 `RSS_FEED`。
2. `index.html` 拉取 `fonts.googleapis.com`——国内/离线环境必挂，首屏抖动 + PWA/隐私告警；
   远程声明还会与本地 `@font-face` 同名冲突。
3. CJK 字符缺字形时直接 `.notdef`（□），无显式 fallback 链。

## 2. 决策

| 决策 | 内容 | 理由 |
|------|------|------|
| 去 Google Fonts CDN | 删除 `<link rel="stylesheet" href="https://fonts.googleapis.com/...">`；图标全靠本地 woff2 子集 + inline 预加载 | 本地子集已覆盖全部 UI 字形；离线/弱网首屏稳定；消除远程与本地 `@font-face` 冲突 |
| 子集脚本单一真源 | `scripts/subset-material-symbols.sh` 维护 glyph 白名单；**新增图标必须重跑**（脚本自装 python3 venv + fonttools/brotli） | 子集从 87 KB 减到 ~8.9 KB（97 连字），同时补齐 `icon:'xxx'` / `icon=xxx` 动态绑定扫描盲区 |
| CJK fallback 显式化 | font-family 链：`material-symbols → "PingFang SC", "Noto Sans CJK SC", "Microsoft YaHei", sans-serif`；`@font-face` 加 `font-display: swap` + unicode-range | 不依赖系统隐式回退，三平台中文有确定栈；swap 消除首屏字体闪白 |

## 3. 已知边界（实测事实）

- **iOS 26.3 模拟器中文 tofu = 模拟器 WebKit 运行时缺陷，非应用 bug**（2026-09-10 A/B 定论，
  `aae01a7`）：该模拟器的 per-character fallback 失效，`-apple-system` 命中无 CJK 的 SF Pro 后
  直接 `.notdef`，连显式 `"PingFang SC"` 都不尝试；同一构建在 iOS 18.4 模拟器上完美。
  **UI 验证一律用 iOS 18.4 模拟器**；不要为模拟器把 CJK 字体名挪到栈首位（会改掉拉丁观感）。
- 子集脚本扫描有窗口限制：写在 JS 数据对象深处的图标名若新增仍可能漏——漏了就往白名单加一行
  重跑，不要手改 woff2。

## 4. 验证（2026-09-09/10）

| 检查 | 结果 |
|------|------|
| `grep -c "fonts.googleapis.com" frontend/index.html` | 0（仅注释提及） |
| woff2 大小 | 8,696 → 8,896 bytes（新增 7 个动态绑定 glyph） |
| `styles.css` CJK fallback | 命中 PingFang / Noto Sans CJK / Microsoft YaHei |
| 310 个单元测试 + typecheck | 全绿（`aae01a7` 时点） |
