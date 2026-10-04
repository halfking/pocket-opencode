# 真机回归收尾：ASR 已解锁、边缘 502、底部导航间歇失效

> 2026-10-04 下午。本节只记**实测结论**与**负面实验结果**，不记推测。
> 每一条都给了可复现的判据来源。

## 一、ASR 已解锁（此前报告的阻塞项之一，现已不成立）

此前多轮报告「网关无上游 provider，语音转写不可用」。**该结论已过期**：

```
[stt] transcribed 256044 bytes (recording.wav) via gateway/mimo-v2.5-asr -> 2 chars   HTTP 200
[stt] transcribed 259244 bytes (recording.wav) via gateway/mimo-v2.5-asr -> 57 chars  HTTP 200
```

对 `backend/pocketd-main.err.log` 统计：**成功 18 次 / 失败 8 次**（最近一次成功 13:26:16）。
失败的 8 次**全部**是上游限流：

```
[stt] transcribe failed: stt mimo-v2.5-asr 429:
  {"error":{"code":"rate_limit_exceeded","message":"Rate limit exceeded","type":"rate_limit_error"}}
```

⇒ **新的待办不再是「开通 provider」，而是「上游 429 限流」**。约 31% 的转写请求被限流掉。
这是网关侧的配额/并发问题，不是本仓代码问题，但会直接表现为用户录音转写偶发失败。

**判据来源**：`pocketd-*.err.log` 里 `[stt] transcribed` / `[stt] transcribe failed` 两类行。
注意 `/api/stt/config` 需鉴权，裸请求拿不到（401），用日志判比用 API 判省事。

## 二、`m.kxpms.cn` 从「回调指错」变成「整站 502」

同一时段内阻塞性质发生了变化，**不要沿用旧结论**：

| 时段 | `/healthz` | `/callback/feishu` | `/callback/weixin` |
|---|---|---|---|
| 早间 | 200（真实 pocketd） | 401 `missing_key`（AI 网关格式，CORS 含 `X-Gw-Project-Id`） | 401 同上 |
| 13:0x | **502** | **502** | **502** |

连续 5 次探测（约 90 秒）全部 502，其中一次直接连接失败 ⇒ **持续宕机，不是抖动**。
`scripts/diag-callback-upstream.mjs` 复核：公网三条路径全是 nginx 的 502 HTML，
本地对照全部正常（`/healthz` 200 pocketd、飞书 `code=0`、企微明文 `success`）。

⇒ 需服务器侧**先恢复服务**；恢复后再把 `/callback/` upstream 指回 pocketd。
后端协议实现本身无需改动，已由本地对照证实正确。

## 三、底部导航间歇失效：机制已查明，但有一个负面实验结果

### 3.1 两条 flow 共用同一段导航块，栽在同一处

`more-grid-reach.yaml` 与 `meetings-entry.yaml` 的「进更多」段落**逐字相同**：

```yaml
- tapOn: { text: "更多|More", retryTapIfNoChange: true }
- extendedWaitUntil: { visible: "更多功能", timeout: 40000 }
- runFlow: { when: { notVisible: "更多功能" }, commands: [ tapOn: "更多|More", … ] }
- extendedWaitUntil: { visible: "更多功能", timeout: 30000 }
```

两条 flow 都在 **step-008 `更多功能`** 红过。失败形态一致：页面停在**首页**，
tabbar（首页/笔记/消息/更多）完整在树里，但 `更多功能` 始终不出现。

### 3.2 已排除的假设（都有对照实验）

| 假设 | 实验 | 结果 |
|---|---|---|
| 首页测试残留挡住导航 | 隔离跑 6 次（残留存在） | 6/6 绿 → **排除** |
| 前序 flow 污染 | `messages-hub → more-grid-reach` | 绿 → **排除** |
| `tasks-crud` 残留 + 相邻 | `tasks-crud → more-grid-reach` ×3 | 3/3 绿 → **排除** |
| 套件特有 | 完整 11 条套件 ×3 | 2 红 1 绿 → **不成立**（不是必然） |
| 完整 4 条前缀即可复现 | `smoke-login→tasks-crud→notes-crud→messages-hub→more-grid-reach` | 绿 → **排除** |

另有一条决定性证据：失败现场首页是 `运行中 0 / 暂无运行中的任务`，
**零测试残留**下依然复现 ⇒ 残留假设彻底出局。

### 3.3 App 侧是好的，坏的是合成 tap

用失败现场的 a11y bounds 定位：标签「更多」= `[606,1570][654,1600]`，
tabbar 容器 = `[0,1496][720,1640]` ⇒ 中心 **(630,1585)**。

在该坐标用 `adb shell input tap` 真实点下去，**能正常进「更多」**，
截图里 10 个主功能入口（学习/对话/会议/邮箱/定时自动化/技能市场/智能体市场/
本地智能体/工作搭子/闪卡）与「运维与高级」全部在位。

⇒ **App 的导航没问题，失效的是 Maestro 的合成 tap 落不到 WebView 的 tabbar。**

### 3.4 ⚠️ 负面结果：加「坐标兜底」不管用，别再试

既然坐标点已被证明有效，很自然会想「给 flow 加一层 `tapOn: {point:}` 兜底」。
**实测做过了，不管用**：

| flow | 加兜底后 |
|---|---|
| more-grid-reach | 4/4 绿 |
| meetings-entry | **3/4，仍红 1 次** |

修前失败率约 3/19（≈16%），修后 7/8。统计上完全不足以证明改善
（即便按 20% 算，7/8 的偶然概率也有 0.21）。**该改动已 `git checkout` 回退，未提交。**

⇒ 结论：**加坐标兜底这条路已经被排除**，下一个人不必重复这个实验。
真正的修法得换机制（例如让 harness 用 CDP 直接改路由，而不是靠合成 tap 驱动 tabbar），
并且要有足够样本量（≥30 次）才能宣称有效。

### 3.5 顺带记一个检测方法的坑

排查时用 `adb shell uiautomator dump` 判断页面内容，**读不到 WebView 的 a11y 节点**
（连 `首页` 都读不到），据此一度得出「adb 点也没跳转」的**假阴性**。
后来改用截图才看清 App 实际已在「更多」页。

⇒ 判断页面状态请用 **Maestro 的 `screen-hierarchy/*.json`**（它能读到 WebView 节点）
或直接截图；`uiautomator dump` 在这个 App 上不可用。两者不能混用，
混用会造出一个不存在的 bug。
