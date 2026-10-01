# 设置页「字体不对、显示错误」：根因已修但从未上线，且护栏从未真正跑过

日期：2026-10-02
用户报告：*「在设置页中，有字体不对，显示错误」*

## 结论（一句话）

**代码里的根因早在 2026-09-30 就修好了（`a5ce54c`），但一直没送到你手机上**；
与此同时，那条防止它复发的回归测试**从来没有被接进 gates**，等于没有护栏。
本轮把护栏接进 gates 并做了负控验证。

---

## 1. 根因是什么

`frontend/src/styles/tokens.css` 的等宽字体令牌：

```css
--font-mono:
  "JetBrains Mono", "Fira Code", ui-monospace, SFMono-Regular,
  Menlo, Consolas, "Roboto Mono",
  monospace,
  var(--font-sans);      /* ← 关键：收尾回落到 CJK 无衬线 */
```

Android WebView 把泛型 `monospace` 解析成 **Droid Sans Mono**——一个**没有任何
中文字形**的纯拉丁等宽字体。本项目设置页到处都是中文（网关地址旁边的「AI 网关」
标签、模型名、会话标题、邮件主题、工具名），中文一旦落进等宽样式，浏览器只能
逐字回退到另一套中文字体。拉丁字母和中文来自两套 x-height / 基线度量不同的字体，
于是同一行里出现高低不齐、字重不一致的「一半对一半错」字形——**这就是用户看到的
显示错误**。

把 sans 栈接在 `monospace` 之后，等宽只作用于它真正有字形的拉丁/数字，中文回落到
与全站正文同一套字体，行内度量重新对齐。

`a5ce54c` 的 diff 证实这就是那次修复：把 `monospace;` 改成
`monospace,\n    var(--font-sans);`，并补了 `"Roboto Mono"`。

## 2. 为什么你今天还能看到

`a5ce54c` 是 **2026-09-30**。而你手机上跑的是：

- 8088 上的**旧二进制**（我这一轮开工会话时 8088 甚至没有监听）
- 装在手机上的**旧 APK**

也就是说：**修复在仓库里，从来没有被打包进你正在用的那个 APK。**
这和「授权替换 8088 旧二进制 + 重装 APK」这一条待办是同一件事。

> 说明：我无法在真机上确认这一条（设备 adbd 无响应，见下）。
> 「修复已进仓库但未上线」是根据提交时间与你的设备状态推断的，
> 不是在设备上比对过 APK 产物得出的。

## 3. 护栏写了，但从没跑过

`frontend/src/styles/__tests__/css-vars.test.mjs`（`a5ce54c` 同时新增，6 个用例）
恰好覆盖这条：

- 扫描全仓有没有绕过 token 硬编码 `monospace / ui-monospace / Menlo / Consolas`
- 断言 `code/kbd/samp/pre` 被显式设成 `var(--font-mono)`
  （注释里直接点名「真机 /settings 的模型名就是这样」）
- 断言 `--font-mono` 仍以 `var(--font-sans)` 收尾

**但是**，`package.json` 的 `gates` 链里只有 `test:native` / `test:stores`，
`css-vars` 和 `teleport-deep` 都不在其中。测试存在、6/6 通过，**却从来不会在
gates 里执行**——一个不会跑的护栏等于没有护栏。

本轮把它接进去：

```json
"test:styles": "node --test src/styles/__tests__/*.test.mjs",
"gates": "... && npm run test:auth && npm run test:styles && npm run check:dueclock && ..."
```

## 4. 负控对照

光看「6/6 通过」不能说明护栏有用。做法是把 `--font-mono` 末尾的
`var(--font-sans)` 删掉（即精确还原用户报告的那个 bug），再跑：

```
not ok 2 - --font-mono 仍以 var(--font-sans) 收尾（CJK 回退不能被这次改动弄丢）
# tests 6   # pass 5   # fail 1
```

精确转红，且只有该转红的那一条转红。随后恢复原状。

## 5. 排查过程中排除的两个假阳性

留档，避免以后重复走：

1. **「`--font-mono` 未定义」** —— 假。`tokens.css:131` 有定义，
   `63c489e`（2026-09-22）补的。注释里说「此前 Button/Input/Textarea 引用过却
   从未定义」，那是**已经修完的旧事**，不是现状。
2. **「设置页的 `<select>`/`<input>` 用了 `font-family: inherit`，会继承到图标字体」**
   —— 假。`SettingsLLMGateway.vue:598/632` 的 `.format-select` / `.model-search`
   确实写了 `font-family: inherit`，但它们所在的 `.form-section` 祖先**没有**
   图标字体类，不会继承到 `Material Symbols Outlined`。

顺带确认：`styles.css:145` 的图标字体规则只回落到 `sans-serif`，
`material-symbols.css:7-8` 记录过这类 bug 在真机上出现过
（「LIGHT_MODE」「ACY」「ENS」直接露馅），但**本次没有在设置页复现到泄漏**。

## 6. 仍然没做到的

- **真机没验证**。真机 `192.168.31.19` 自 16:40 起 adbd 无响应。
  本轮已把「卡死」和「不是 adbd」区分开（见
  `docs/handoff/2026-10-02-real-device-adbd-not-speaking.md`）：
  TCP 5555 接受连接、发合法 CNXN 包、**8 秒回 0 字节**，同一局域网、MAC 未变。
- 因此「修复确实没上线到你设备」这一点仍是**推断**，需要重装 APK 后在真机上
  复核才能定论。
