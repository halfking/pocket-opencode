# UI 视觉技法 3 件套（2026-09-21）

> 三件新增基础原语，纯 Vue + 原生 CSS（无 @vueuse / framer-motion / 任何外部动效库），可立即被任何页面 / 业务组件采纳。

---

## 1. AnimatedNumber — 数字平滑插值

**位置**：`frontend/src/components/base/AnimatedNumber.vue`
**配套**：`frontend/src/composables/useCountUp.ts`（核心 tween，可独立复用）

### 用法

```vue
<AnimatedNumber :value="1234" :duration="800" />
<AnimatedNumber :value="87.5" :decimals="1" :unit="'%'" />
<AnimatedNumber :value="recSeconds" unit="s" :pulse="true" />
```

### 特性

| 选项 | 说明 |
|---|---|
| `value`     | 目标数字，必填 |
| `duration`  | 动画时长 ms，默认 800 |
| `decimals`  | 小数位数，默认 0 |
| `prefix` / `suffix` | 前缀 / 后缀（如 `$` / `次` / `°C`）|
| `unit`      | 单位串（如 `s` / `ms` / `%`），以小字号显示在数字后 |
| `pulse`     | 数字变化时附加 1.18× 微脉冲（缩放 240ms ease-out），用作「刚刚更新」反馈 |

### 技法

- `requestAnimationFrame` 驱动 easeOutCubic，前半段快到达、后半段缓收敛（200/500/700ms 进度分别 ≈58% / ≈98% / ≈100%）
- `prefers-reduced-motion: reduce` → 自动 duration=0 直接跳末值，尊重无障碍设置
- `font-variant-numeric: tabular-nums` 等宽数字，动画期间不水平抖动
- `onScopeDispose` 收尾，不会泄漏 rAF
- `pulse` 是数值变化后 240ms 缩放反馈，配合「刚刚节省了 X 元」「+5 条新任务」这类持续变动的数字极佳

### 适合

- 录音 / 计时器秒数
- 待办 / 已读数等持续变化计数
- 货币 / 进度 / 比率等「前一秒还看不到尾数字」的指标
- AI 流式 chunk 计数 / Token 用量

---

## 2. ProgressRing — 动画 SVG 进度环

**位置**：`frontend/src/components/base/ProgressRing.vue`

### 用法

```vue
<!-- 录音倒计时 60s，环跟着进度走 -->
<ProgressRing :value="recProgress" :size="68" :stroke="6">
  <AnimatedNumber :value="recSecondsLeft" unit="s" />
</ProgressRing>

<!-- 不确定加载（边下载边扫） -->
<ProgressRing :indeterminate="true" :size="44" />

<!-- 自定义品牌色 -->
<ProgressRing :value="75" color="#8b5cf6" />
```

### 特性

| 选项 | 默认 |
|---|---|
| `value`           | 0..100，必填 |
| `size`            | 64 px |
| `stroke`          | 6 px |
| `color`           | `var(--brand-primary)` |
| `linecap`         | round（可改 butt/square）|
| `indeterminate`   | false → true 时切换为 1.4s 扫描动画（无固定值）|
| `duration`        | 600 ms（两次 value 变化间的插值）|

### 技法

- 双 circle 结构：track（背景轨） + bar（进度弧）
- `stroke-dasharray` 与 `stroke-dashoffset` tween，无任何 JS 动画库依赖
- 1.4s 不确定态使用 `currentColor` 友好的 `cubic-bezier(0.65, 0.05, 0.36, 1)` 扫描
- 暗色模式 track 自动调亮（通过 `:global([data-theme='dark'])`）
- `role="progressbar"` + `aria-valuenow` 满足无障碍要求
- slot 默认插槽放中心文本（与 AnimatedNumber 组合天然）

### 适合

- 录音 / 计时器剩余进度环
- 任务完成度 / 学习进度 / 卡路里消耗进度
- 不确定加载占位
- 后续同样可适配「流式 chunk 进度」「磁盘空间使用率」等

---

## 3. StaggerList — 交错条带出现动画

**位置**：`frontend/src/components/base/StaggerList.vue`

### 用法

```vue
<StaggerList :step="60" :distance="14">
  <NoteCard v-for="n in notes" :note="n" />
</StaggerList>
```

或包裹 menu / 列表项：

```vue
<StaggerList tag="ul" :initial="40" :duration="320">
  <li v-for="item in items" :key="item.id">{{ item.title }}</li>
</StaggerList>
```

### 特性

| 选项 | 默认 |
|---|---|
| `tag`            | div（可换 ul/ol/section 等）|
| `step`           | 50 ms / 每项延迟 |
| `initial`        | 60 ms 首项延迟 |
| `duration`       | 380 ms 单项动画时长 |
| `distance`       | 14 px 起点 Y 位移 |
| `whenInView`     | true（视口进入才触发，避免已划过的列表又 stagger）|
| `rootMargin`     | `'0px 0px -10% 0px'`（距底部 10% 时开始）|
| `threshold`      | 0.05 |
| `retriggerOnKey` | false（可在数据 key 变化时重放）|

### 技法

- `display: contents` 让容器不引入额外 flex/grid，:scope > * 直系子
- 每项初始 `opacity: 0; transform: translateY(distance) scale(0.985)`
- 下一帧注入过渡：`opacity duration delay, transform duration delay`，逐项累加 step
- IntersectionObserver 视口探针，元素不在屏内不触发（避免长列表中部已划过还在跳）
- `prefers-reduced-motion: reduce` → 直接显示，不透明无位移
- `retriggerOnKey` 在数据源刷新时可通过 `:key` 强制重放一次（用于「追加 1 条新结果」的场景）

### 适合

- 进入列表 / 详情 / 设置 / 搜索结果页的首屏（一次性 stagger 入场）
- 设置分组、聊天列表、AI 结果列表、待办列表
- 建议配合 Nuxt-style page transition 用：先做 stagger 再叠 page enter

---

## 4. 综合示例（作曲级 UI 微件）

### 4.1 录音圈（RecordingPill 升级路线）

```vue
<ProgressRing :value="recProgress" :size="44" :stroke="4" color="var(--danger)">
  <AnimatedNumber :value="recSeconds" unit="s" pulse />
</ProgressRing>
```

### 4.2 数字仪表盘

```vue
<div class="grid">
  <Card v-for="m in metrics" :key="m.id">
    <AnimatedNumber :value="m.value" :unit="m.unit" :decimals="m.decimals" pulse />
    <div class="muted">{{ m.label }}</div>
  </Card>
</div>
```

### 4.3 AI 流式回答列表

```vue
<StaggerList tag="ol" :step="40" :initial="40">
  <li v-for="chunk in streamChunks" :key="chunk.id">
    <JsonBlock :data="chunk.value" />
  </li>
</StaggerList>
```

---

## 5. 与现有触感反馈 / 路由转场叠加

- **触感反馈 10 点位**（commit `cd8892f`）：数字 tween 终值时附 `haptic('light')`，让用户耳朵 + 触觉同步收到「数字刚好抵达」反馈
- **页面转场**（commit 早）：这些组件是被转场包裹的 view 内部使用，转场外壳不动
- **路由压栈 list 缓存**：默认不动；这些组件每次挂载都跑 stagger，对列表页 KeepAlive 不缓存（`LIST_CACHE_NAMES` 白名单不收录它们）

---

## 6. 验证

- `frontend/src/composables/__tests__/useCountUp.test.mjs` — 5 个 easing 形状单测
- `npm run typecheck` — 类型清
- `npm run test:native:all` — 99/99 全绿（未触动既有测试）
- `npm run check:vm-gaps` — 0 命中（这 3 个组件是 base，无 api/store 直连）

---

**写于**：2026-09-21 · commit #34 待推送
**作者**：Mavis / mavis orchestrator
**取舍**：选中这 3 项是为「任何页面都能立刻用上」+「对原生 60fps 不引入 main thread 压力」（都是 CSS transform / opacity / stroke-dasharray，无 layout 抖动）。
