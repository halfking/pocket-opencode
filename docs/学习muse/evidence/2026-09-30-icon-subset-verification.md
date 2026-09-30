# 图标子集缺字：根因收口（2026-09-30，续）

**范围**：P2 记下的「7 处动态图标名靠巧合进子集」——本轮查证后发现**不是 7 处的隐患，
是 20 处正在发生的缺字**，并且其中三个正是子集脚本当初要修的那个 bug 的复发。

---

## 1. 查证结果：这是个正在生效的 bug，不是隐患

P2 的结论是「这些图标当前能正常显示，说明它们的字形此前已通过别的字面量位置进入子集；
但这是巧合而非保证」。本轮把那个「巧合」实际验了一遍——**巧合不成立**。

新写的 `scripts/check-icon-subset.mjs` 扫出全仓 36 个 `icon: 'x'` 形式的声明式图标，
与「字面量 + 原兜底」求差集，结果 **20 个不在子集内**：

| 文件 | 缺失图标 |
|---|---|
| `features/settings/SettingsView.vue:249-251` | `light_mode`、`dark_mode`、`brightness_auto` |
| `components/base/SettingsMenuDrawer.vue:99,101` | `model_training`、`privacy_tip` |
| `features/flashcards/FlashcardEditView.vue:264-265` | `compare_arrows`、`auto_awesome_motion` |
| `features/more/MoreHubView.vue:102-105,124,127` | `extension`、`smart_toy`、`memory`、`handshake`、`payments`、`import_export` |
| `components/BottomNav.vue:88` | `apps` |
| `features/sessions/useSessionDrafts.ts:52-59` | `play_arrow`、`merge`、`sports_score`、`subject`、`science`、`fast_forward` |

### 为什么会漏：修的是两种形态里的一种

`build-material-symbols-subset.mjs` 的字面量正则要求图标名**直接出现在模板里**：

```html
<span class="material-symbols-outlined">check</span>     <!-- 扫得到 -->
```

P2 那轮我补了「同一行 `{{ 'a' : 'b' }}` 插值」这一种形态。可是这些图标是**另一种形态**——
写在数据表里，模板只负责渲染字段：

```js
{ value: 'light', label: t('settings.themeLight'), icon: 'light_mode' }   // 扫不到
```
```html
<span class="material-symbols-outlined">{{ opt.icon }}</span>            // 字段名，扫不到
```

**`light_mode` / `dark_mode` / `brightness_auto` 恰恰是本脚本存在的理由。**
脚本头注释里写着原始证据：真机 redmi 上设置页主题三选项直接显示成 `LIGHT_MODE` 文本。
那个 bug 回来了——不是没修过，是图标从模板字面量搬进了数据表，而扫描规则没跟着搬。

---

## 2. 修法：白名单 + 机器强制的不变量

### 2.1 把 20 个名字显式加进 FALLBACK，并在注释里说明来历

关键不是「加进去」，是**写清楚为什么加、以及不许删**：

> 这一组曾经真的丢过：light_mode / dark_mode / brightness_auto 是本脚本当初要修的
> 那个 bug（真机显示成 'LIGHT_MODE' 文本），它回来了——因为图标从模板字面量搬进了
> 数据表。**不要因为「看着没被用到」就删这里的名字**，删之前先跑 `check-icon-subset.mjs`。

### 2.2 把「巧合」变成不变量

`scripts/check-icon-subset.mjs` 扫 `icon: 'x'` 声明，与子集求差集，有差集就**退出码 1**。
这是本次唯一真正的新增保障——白名单是静态的，声明式图标是持续增长的，
只有机器检查能在第一个人加新图标时就拦住他。

### 2.3 两份清单不重复维护

检查脚本**从构建脚本里正则解析 FALLBACK**，而不是抄一份。抄两份的必然漂移，
而漂移的后果恰恰是本脚本要消灭的那类静默不一致（构建以为有、检查以为没有）。
解析失败时退出码 **2**（区别于「有缺失」的 1），避免名单改名后检查静默放行。

---

## 3. 验证

| 命令 | 结果 |
|---|---|
| `node scripts/check-icon-subset.mjs`（补白名单**前**，用临时副本） | ✅ 正确报出 20 个缺失，退出码 1 |
| `node scripts/check-icon-subset.mjs`（补白名单**后**） | ✅ `所有声明式图标都在字体子集内`，退出码 0 |
| `node scripts/build-material-symbols-subset.mjs` | ✅ 109 → **129** 个图标，3605.0 KB |
| `npx.cmd vue-tsc --noEmit` | ✅ 退出码 0（上一轮） |
| `node scripts/build-gate.mjs` | ✅ `✓ built in 19.43s` |
| `node scripts/check-viewmodel-gaps.mjs` | ✅ 命中 0 = 阈值 |
| `node scripts/report-locale-gaps.mjs` | ✅ 缺 0 / 多 0 |

字体体积：3,438.3 KB → **3,605.0 KB**（+167 KB，换 20 个字形的真机可读性）。

### 这个检查抓不抓得住？抓得住

上一步的「补白名单前」就是**注入漂移后的实测**：临时副本去掉动态白名单，
检查如实报出 20 个缺失并以 1 退出；补回后转为 0。
不是靠「脚本自己报的计数变小了」判断通过——这正是 P2 那个字体正则假绿的教训。

---

## 4. 明确未验证的部分

| 项 | 原因 |
|---|---|
| **真机目视** | 20 个图标**没有在设备上看过**。**后续已补上浏览器目视验证**（Chromium 逐格渲染全部 125 个名字，123 个是图形，2 个是收集规则假阳性），但 **Android 真机仍未测**——字号、抗锯齿、深色模式对比度、真实页面里的对齐只有设备能确认。见 [icon-registry-and-font-gate](2026-09-30-icon-registry-and-font-gate.md) |
| 字体体积 | +167 KB（3.44 → 3.61 MB）。**已由后续轮次更正**：实测原始 3.80 MB → 产物 3.52 MB，**只削掉 7.3%**，脚本注释里「≈4-12 KB」与实际差约 300 倍。见 [icon-registry-and-font-gate](2026-09-30-icon-registry-and-font-gate.md) |
| `useSessionDrafts.ts` 的 6 个图标 | 来自 `.ts` 里的数据表，扫描要靠 `icon:` 声明式。**已收口**：动态图标名统一登记在 `src/constants/icons.ts`，不再依赖正则去捞 |
| 集中式图标映射表（`icons.ts`） | **已做**（后续轮次）。57 个动态名 + `IconName` 联合类型 + 字体级实测门禁。见 [icon-registry-and-font-gate](2026-09-30-icon-registry-and-font-gate.md) |

---

## 5. 教训

**同一类 bug 会有第二种形态。** P2 修了「模板内插值」，没修「数据表声明」，
于是同一个 bug 换个马甲回来了。修 bug 时问的不是「这个位置我修了吗」，
而是「**这类问题的所有形态我都覆盖了吗**」。

**「当前能显示」不是「正确」。** P2 写「它们当前能显示，说明字形已通过别的位置进入子集」
时，我把一个未验证的推断当成了观察记录。写证据时不确定的东西，
下一轮要花代价去查——这次查出来是 20 处缺字，不是 7 处。
