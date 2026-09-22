# Agentic UI Testing Skill · 设计选型 (2026-09-21)

> 设计文档。回答 3 件事：
> 1. Maestro MCP 思路的本质是什么
> 2. 我们能不能复用这套模式做真机验收
> 3. 我们用什么形式把它沉淀下来
>
> 配套产物：`.minimax/skills/real-device-test/`（已落地）。

## 1. Maestro MCP 思路拆解

读 `docs\audits\2026-09-21-cdp-attempt-result.md` 时我注意到一条关键信息：
> 投入 30 min 尝试 CDP 直连 WebView 后放弃，让用户端 30 秒接力验证。

Maestro MCP 把这条死路换了个走法，核心是 4 件套：

| 维度 | Maestro MCP | 我们的现状（差距） |
|---|---|---|
| 控制器 | LLM 直接调 tap / type / swipe | 手点 5 下手机 |
| 元素定位 | 用大模型看 UI tree + 自然语言意图 | uiautomator dump + 硬编码坐标 |
| 自愈能力 | 元素位置 / 文案飘了 → 模型重新理解意图 | 飘了就 FAIL |
| 落地形式 | 开源 CLI + MCP server + CI 集成 | adb 脚本 + handoff 接力卡 |

**核心观点**：MCP 只是传输协议，让 LLM 拿到工具接口；真正干活的是
**自然语言意图 → UI 操作原语 → 自愈断言**。这两层我们没有。

## 2. 我们能用这套模式吗

**能，但不必照搬 MCP server 形式。** 三个理由：

1. **基础设施已经够用**。`adb shell input` / `uiautomator dump` /
   `screencap` 是 Android 平台标准 API，不需要额外 daemon。
2. **CDP 路径在 Android System WebView 上不稳**。
   `2026-09-21-cdp-attempt-result.md` 已经实证 WebView DevTools 的
   HTTP discovery 端点经常 `RemoteDisconnected`，绕不开。
3. **5-click / 30-min 后台保活**两类场景的 user-side 摩擦点不一样，
   强行套同一个 MCP server 会把无关的复杂度拉进来。

所以更可走的路是：**把"Maestro MCP 的 4 个能力维度"沉淀成 Skill
内的 procedure**，让 Mavis（agent）能按需挑选最合适的 driver + 原语
跑一遍。这正是 `.minimax/skills/real-device-test/SKILL.md` 的设计目标。

### 2.1 与现有脚本的关系

| 已有产物 | 角色 | 与 skill 的关系 |
|---|---|---|
| `scripts/real-device-preflight.cmd` | PATH / install / start / 电池白名单一键 | skill §1 直接复用 `adb` 原语，不重复 cmd 包装 |
| `scripts/real-device-capture.ps1` | 30 min logcat 捕获 + 4 项关键 hit 计数 | skill §7 明确「不重新实现，直接调脚本」 |
| `e2e/android/android-keepalive-cdp.py` | AI stream keepalive CDP 全链路 | skill §references/cdp-runtime-apis.md 把其用法固化成可复用文档 |
| `e2e/android/local-agent-cdp.py` | local-agent 审批流 CDP 全链路 | 同上 |
| `handoff/2026-09-21-edge-to-edge-and-endpoint-switch-pickup.md` | 用户端 5-click 接力卡 | skill §Example A 把接力卡结构化成可回填的 `summary.md` |
| `scripts/redmi-capture-helper.ps1` | 二进制安全的 screencap 封装 | skill §Windows platform notes 引用，避免 CRLF 坑 |

skill 不是替代这些脚本，而是把它们**编排起来 + 加自愈能力**。

### 2.2 不做的事

为了避免范围漂移，skill 明确**不**做：

- 不写新的 MCP server —— 当前 WebView DevTools 路径不稳，建了也跑不通
- 不引入 `chrome-remote-interface` npm 依赖 —— 同样受限于 §2.2 关 2
- 不重写 30-min logcat 抓取 —— `real-device-capture.ps1` 已经能 4 项 hit 计数
- 不写 Python 工具链 —— 复用项目现有 `e2e/android/*.py` 即可

## 3. 选 Skill 而不是 MCP server 的理由

`mavis({ command: "mcp ..." })` 的能力参见
`C:\Users\86133\.minimax\.builtin-skills\mavis\references\mcp.md`。
理论上我们完全可以注册一个 `real-device-mcp` server，让 Mavis 通过
MCP tool 调用 adb 操作。但**当前阶段**选 Skill 而非 MCP，理由：

| 维度 | Skill | MCP server |
|---|---|---|
| 接入门槛 | 放进 `.minimax/skills/` 即可被 Mavis 自动发现 | 需 `mcp create` + 长期维护 daemon |
| 工具协议 | 自由（procedure 描述 + adb shell + python 脚本） | 严格（必须实现 MCP JSON-RPC） |
| 灵活性 | 按场景组合原语（CDP / uiautomator / 截图） | 工具签名固化，新场景需新 tool |
| 排错反馈 | agent 看 stderr 即可 | 需要 MCP tool result 包装 |
| 适合阶段 | 我们现在（先打通流程，沉淀经验） | 大规模 CI 自动化（未来 PR） |

文档 §6 留作 future PR：`docs\audits\2026-09-21-cdp-attempt-result.md`
提到的 release smoke test 才值得花时间升级到 MCP server。

## 4. skill 结构

```
.minimax/skills/real-device-test/
├── SKILL.md                          # 287 行 procedure + 2 canonical examples
└── references/
    ├── adb-cheatsheet.md             # PowerShell 友好 adb 命令表
    ├── cdp-runtime-apis.md           # __openpocket_*Runtime* API surface
    ├── backfill-template.md          # summary.md 的 schema + 实例
    └── self-healing-recipes.md       # 元素飘了时的 4-tier 自愈梯子
```

不写 scripts/ —— 第一版让 agent 直接调 adb / python，看哪类动作真的高频
再下沉（skill-creator anti-pattern §7 提到第一版不应过度脚本化）。
不写 README / CHANGELOG / install.sh —— skill-creator anti-pattern §2。

## 5. 与现有文档的串接

- `STATE.md` §5「全部 scripts」末尾加一行 `.minimax/skills/real-device-test/`
- `e2e/README.md` §「Android / iOS」末尾提一句「Agentic 走 skill」
- 后续每次跑 5-click / 30-min 时，`handoff/*.md` 里加一行「建议用
  `real-device-test` skill 直接代理，agent 会回填 `summary.md`」

## 6. 何时升级到 MCP server

满足以下任意 2 条再考虑升级：

1. CI 流水线需要在每次 PR 自动跑 smoke test
2. WebView DevTools 升级到 110+ 且 `/json` discovery 路径稳定
3. 我们同时维护 ≥ 3 套 driver（Android 真机 / Android 模拟器 / iOS）
4. 出现「agent 跑错 adb 命令把设备搞坏」的事故，需要强制权限边界

当前都不满足。

---

**写于**：2026-09-21 14:50 · commit `<tbd>`
**作者**：Mavis / mavis orchestrator
**产物**：`.minimax/skills/real-device-test/`（已通过 `lint-skill.js`）