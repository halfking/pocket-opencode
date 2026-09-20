# ViewModel 缺口盘点（2026-09-20）

> 上游：[`../audits/2026-09-20-native-ui-restructure-plan.md` §2.3 + §8.3](../audits/2026-09-20-native-ui-restructure-plan.md)
> 脚本：`frontend/scripts/audit-viewmodel-gaps.mjs`
> 范围：`frontend/src/features/**/*.vue`（排除 `__tests__` / `node_modules`）

---

## 0. 结论先看

| 指标 | 值 |
|---|---|
| 扫描 .vue 总数（features/） | 118 |
| 直连 api/stores 的 .vue 总数 | 多于 90（绝大多数都用了 type；少数运行时引用） |
| **真缺口（运行时 import + 无 composable）** | **2 个 .vue** |
| 审计覆盖率 | type-only 已过滤；composable 命名已两条路径识别 |

**结论**：周 5-6 的工作量从"全栈 UI 解耦"缩减为"精确命中 2 文件"。邮件域已完成天然 4 层；其他域亦意外达成。

---

## 1. 脚本判定逻辑（避免假阳性）

判定**直连 + 缺 ViewModel**需要同时满足：

1. import 来源为 api/\* 或 stores/\* 中至少一个；
2. 且为**运行时** import（`import type {...}` 被剔除）；
3. 且同一文件未出现：
   - `useXxx(` 形式（驼峰 composable 调用），或
   - `import { use-xxx }` 形式（kebab hook）。

任一 comopsable / hook 命中就视为"已分层"，不计入缺口。

---

## 2. 真命中清单

| 文件 | 实际引用 | 备注 |
|---|---|---|
| `features/config/ConfigList.vue` | `from '../../api/...'` | 配置域非高频，先放后；可以延后到配置域下一次大改再处理 |
| `features/tasks/TaskSessionSheet.vue` | `import { api, ... } from '../../api/client'` | 指挥中心会话抽屉；目前会话页有 `usePendingApprovals` 但 sheet 仍直连，是周 5-6 的优先目标 |

### 处理建议（按 ROI 排序）

1. **`tasks/TaskSessionSheet.vue`**：抽出 `useTaskSessionSheet` 组件 hook，承接 api 调用 + 打开会话聚合；
2. **`config/ConfigList.vue`**：视配置模块下一次大改动一起重构；本期不动。

---

## 3. 周 5-6 调整后阶梯

| 周 | 范围（原计划） | 调整后 |
|---|---|---|
| 5 | "emailService.ts 抽取" | 邮件域已完成天然 4 层，**本段改为：抽出 `useTaskSessionSheet`**；完成 `TaskSessionSheet.vue` ViewModel 化（约 1 个 PR）|
| 6 | "useEmailListVM" | 邮件域仅剩"缺统一契约"，可选用 `useEmailListVM` 把 `use-email-inbox.ts` + `emailsStore` 协同起来；亦可不改 |

---

## 4. 验证手段

```bash
cd frontend
node scripts/audit-viewmodel-gaps.mjs
```

期望：`共 0 个`（命中归零）；CI 跑该脚本，命中 > 0 时 fail。

---

## 5. 没被审计覆盖到的"暗分层"

下列情况脚本识别不到，需要肉眼看：

- 同一文件多个子组件共享一个 store（已有 composable 共享，不算"无 VM"）
- composable 名不以 `useX`/`use-x` 开头（少；未扫出）

---

**写于**：2026-09-20
**作者**：Mavis / mavis orchestrator
**下次更新**：周 5 抽出 `useTaskSessionSheet` 后回填
