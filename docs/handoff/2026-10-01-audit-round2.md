# 2026-10-01 审计轮 · 第二轮

> 接 `2026-10-01-audit-round.md`。上一轮结论是「这 24 小时的邮件/STT 工作
> 从未编译通过一次」，并把 3 条缺陷记在 §5 待修。本轮处理这 3 条。
> 本文只写**有对照或可复现证据**的部分。

## 1. 开工前置检查（按上一轮 §8 定下的三条走）

| 检查 | 结果 |
|---|---|
| `origin/main` 是否已含邮件/STT feature | **否**。`3598357` 之后未动，feature 仍只在未提交工作区 |
| 是否有别的会话在写同一工作区 | **有**。`internal/stt`、`internal/server`、前端、Android 下的文件持续变动；另有两个 worktree（`wt3`、`.wt-consolidate`）在活动 |
| 工作区是否可入库 | **否**。`go vet ./...` 报 `llm_gateway_default_init_test.go:40: undefined: opencode.DefaultLLMGatewayAPIKey` |

第 3 条是本轮的直接约束：工作区里有一处在飞的测试编译不过，
**不能提交**。所以本轮没有落 feature 本身。

## 2. 上一轮 §5 三条缺陷的处置

### ① `LooksLikeMissingAudio` 漏判 `don't see` —— 已由并发会话修好

正则现在覆盖 `don'?t (?:see|hear|receiv\w*|get|access)`，并且额外补了
`do not ...`。比上一轮建议的最小修法更全（多了 `do not` 全称形式）。
`TestLooksLikeMissingAudio` 通过。

### ② `ProbeEndpointMissing` 是死常量 —— **本轮修掉**

上一轮发现：常量声明了、`server_stt_settings.go` 也为它配了「无转写端点」
中文文案，但**生产代码从未给它赋过值**。

**用户可见后果**（上一轮只是推断，本轮实测复现出来了）：
网关两种传输形态都不存在时，候选落到 `ProbeFailed`，
而 `describeProbe` 对 `ProbeFailed` 拼的是 `"探测失败(" + Detail + ")"`，
`Detail` 带着上游原始响应体，于是设置页显示：

```
探测失败(http 404: {"error":{"code":"no_candidate","message":"No available provider for model 'no-asr-here'"}})
```

**修法**：两种形态都 404/405 时判 `endpoint_missing`，`Detail` 换成人话，
不再把上游 JSON 甩给用户。

**证据不是「测试绿了就算」**：我把修复临时摘掉重跑，
测试如实失败并复现了上面那串原始 JSON：

```
discovery_test.go:250: status=failed want endpoint_missing
  （detail=http 404: {"error":{"code":"no_candidate",…}}）
```

再装回修复，`internal/stt` 全绿。所以这条测试确实咬得住这个 bug，
不是自证自话的摆设。

### ③ `TestDiscoverClassifiesNoProvider` 夹具与注释不符 —— 已由并发会话修好

夹具现在给三个模型都显式返回 `503`，与它自己的注释「chat 一律 no_candidate」
以及 `discovery.go` 里记录的真实观测（503 no_candidate）对上了。
顺带把 `isNoProvider` 这条生产路径从**零覆盖**变成有覆盖。

## 3. 本轮新增的产物：可应用的补丁

修复代码在**未提交的工作区**里，而 `stt/discovery.go` 目前不在任何 git 分支上
（feature 未落地），所以没法做成正常提交——单拎这两个文件进 main 会编不过
（`discovery.go` 依赖同样未落地的 `target.go` 里的符号）。

因此把修复导出成补丁：

`docs/handoff/patches/2026-10-01-stt-endpoint-missing.patch`

- 由「修复前 / 修复后」两份文件用 `git diff --no-index` 生成，不是手写
- 路径已改写为仓库相对路径，`git apply -p1` 可直接用
- **已验证可应用**：`git apply --check` 退出 0；实打实 apply 之后，
  与我实测通过的那份文件逐字节一致（仅 CRLF/LF 差异，内容相同）

给 consolidate 会话或人用：feature 落地后，直接

```
git apply -p1 docs/handoff/patches/2026-10-01-stt-endpoint-missing.patch
```

## 4. 本轮未解决 / 遗留

1. **feature 仍未落地**。`consolidate/2026-10-01` 还停在 `origin/main`，
   它的 worktree 里有 146 处未提交改动。本轮不碰。
2. **工作区当前不可入库**（§1 第 3 条）。`llm_gateway_default_init_test.go`
   引用了尚不存在的 `opencode.DefaultLLMGatewayAPIKey`，是并发会话的在飞改动。
3. **`TestFetchPOP3MailboxAuthRejected` 仍未定性**（上一轮 §4 结转）。
   基线 `origin/main` 绿、本轮工作区红，偶发还是回归没定论。
4. **并发写入本身仍是最大风险**。同一工作区被多个会话同时写，
   本轮已第二次撞上（第一次是 `server_stt_settings_test.go` 语法错误中途态）。
   这是流程问题，不是代码问题，但造成的返工比任何单个 bug 都贵。
