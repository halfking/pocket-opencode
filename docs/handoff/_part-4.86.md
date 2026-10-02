
# §4.86 真机 CDP 通道统一：把「固定端口」这个共享可变状态收进一个 helper，并把债务量出来

> 承 §4.85。本轮做三件事：① 新增 `scripts/lib/adb-cdp.mjs` 作为真机 CDP 通道的**唯一入口**，
> 并新增守门脚本把「硬编码固定 CDP 端口」的债务量化；② 把两个夹具迁到 helper，顺手修掉
> 迁移过程中暴露的**三处判据缺陷**；③ 全仓扫「不实日期」，只改我拥有的 15 个文件。
> **138 个文件仍硬编码固定端口**，这一项没做完，见 §4.86.5。

## §4.86.0 结论

- **新增** `scripts/lib/adb-cdp.mjs`：`forward tcp:0` 让 adb 分配端口、按**当前 pid** 选
  devtools socket、`close()` 必删 forward。端口碰撞从「概率事件」变成「不可能」。
- **新增** `scripts/check-fixed-cdp-ports.mjs`：守门脚本，把债务从「凭印象」变成可复测的数字。
  现状 **286 个 `.mjs` 里 140 处硬编码固定 CDP 端口，分布在 140 个文件，取值 8088–9630**。
- **迁移 + 修缺陷**：`pkm-test-fixture.mjs`（9418）、`flashcards-test-fixture.mjs`（9420）改走 helper。
  迁移时发现两处真实缺陷，均已修并做了负控：
  1. 两个夹具的**出错分支都直接 `process.exit(1)`，没关 ws、没 `forward --remove`** ——
     失败会在设备上留下 forward 残留。与 BUG-V10 的「失败路径不还原覆盖值」同源。
  2. `flashcards-test-fixture.mjs` 在 localStorage 清理**超时/异常时只打印一句「未确认」，
     然后继续跑 PG 删除，最后照样打 ✅**。这是把「零状态前置没生效」印成了成功：
     缓存没清 ⇒ 列表回显上一轮卡组 ⇒ 这轮测的是 `deck-toggle` 分支而不是零卡组分支，
     而且**不会红**。已改成硬失败。
- **不实日期**：机器时钟是 `2026-10-02 23:38 (+08:00)`，Maestro 产物目录名
  （`~/.maestro/tests/2026-10-02_231342`）独立佐证今天是 10-02。因此注释里
  「2026-10-03 真机实测」这类声明都是**未来日期的实测**。已在我拥有的 15 个文件里改掉 46 处。

## §4.86.1 为什么固定端口是缺陷，而不只是「不好看」

端口是**同机所有会话共享**的状态：本机同时有别的会话在驱同一台设备，而且 adb server 重连、
WiFi 抖动、App 重启都不会替谁清理 forward。

撞上时报 `cannot bind listener ... 10048`，而**那句报错指向的是装置**，根本看不出
「真问题是上次没清干净」。BUG-V9 就是这么撞上的，而且它撞得很隐蔽：落在
`assertFetchIntact` 上时，那道守卫**只会降级成一句「未能判定，不阻断」，然后照样 `exit=0`**
—— 守卫没跑成，绿灯照出。

> 这就是「恒真的判据比没有判据更糟」的一个实例：不是守卫救了这次运行，是守卫**假装**救了它。

另外两条已实测、已固化进 helper 的坑：

- **socket 必须按当前 pid 选，不能取最后一个。** 设备 `/proc/net/unix` 里会留着**死进程**的
  `webview_devtools_remote_<pid>`。取「最后一个」会连到不响应的旧 socket，
  表现是「CDP 探测超时」——看着像 CDP 坏了，其实是自己选错了。helper 在按 pid 匹配不到时
  会**退回并大声告警**（打印实际用的 socket 和陈旧 socket 的数量），不做静默退回。
- **`process.exit()` 不会跑 `finally`。** 要退出就设 `process.exitCode`，让流程自然落到块外，
  否则你以为清干净了其实没清。

### 债务实测（`node scripts/check-fixed-cdp-ports.mjs`）

| 量 | 值 | 怎么量的 |
|---|---|---|
| 扫描的 `.mjs` | 286 | 递归，排除 `node_modules`/`.git` |
| 硬编码固定 CDP 端口 | **140 处 / 140 个文件** | 三条规则：`hardcoded-default` 118、`plain-port-const` 21、`literal-port` 1 |
| 端口取值范围 | 8088 – 9630 | 同上 |
| `forward tcp:…` 绑定点 | 154 | 含 `forward` 且 `tcp:` 且非 `--remove` 的代码行 |
| 其中做了 `--remove` 的 | **27** | 同上 |

**154 个绑定点只有 27 个会清理** —— 残留 forward 正是 BUG-V9 那类碰撞与污染的来源。

> 口径提醒：本节第一张表的 140 与第二张表的 154 **不是同一个量**。
> 140 数的是「写死端口的声明处」，154 数的是「实际发起 forward 绑定的代码行」。
> 早先注释里这两个数被写成 157 / 24，两个都不对，已按实测更正。

## §4.86.2 守门脚本的判据自检

`node scripts/check-fixed-cdp-ports.mjs --selftest` 三件事都做了，缺一件这道门禁就只是装饰：

| 检查 | 结果 |
|---|---|
| 3 类硬编码都能报出（敏感度） | ✅ |
| 5 类合法写法都能放过（特异度） | ✅ |
| 逐条关掉某条规则 → 该条恰好少报 1（覆盖面 / 「故意变瞎看少算多少」） | ✅ 三条各差 1 |

> 上一轮写「长请求必须接 signal」那道护栏时，判据自己连错了 4 次（嵌套泛型、跨文件常量、
> `N * 60_000` 写法、`if (` 被当成函数签名），**4 次全都不报红，只是安静地漏看**。
> 所以这次把「变盲对照」写进了 selftest，而不是只验「故意改坏会不会红」。

**刻意没有接进 `frontend/package.json` 的 `gates` 聚合**。原因：现在它报 140 处，
接进去会立刻把所有会话的 `gates` 打红。要接必须是**基线棘轮**形态（只对新增违规失败），
那是独立一件事，本轮没做。

## §4.86.3 两个夹具的迁移与负控

```
node --check scripts/lib/adb-cdp.mjs            # 0
node --check scripts/pkm-test-fixture.mjs        # 0
node --check scripts/flashcards-test-fixture.mjs # 0
node --check scripts/check-fixed-cdp-ports.mjs   # 0
```

**实跑（设备 `192.168.31.19:5555`，App pid 15543）：**

```
node scripts/pkm-test-fixture.mjs
  deleted:[{"id":"ast_mur3kclr_zmscrh","ws":"ws_user-admin","title":"MaestroPKM笔记"}]  remaining:0
  exit=0        adb forward --list → 0 条

node scripts/flashcards-test-fixture.mjs
  before [decks|notes|cards|revlog] = 1|1|1|0
  localStorage 清理：[["flashcards:v1",true],["flashcards:v1:outbox",true]]
  after  [decks|notes|cards|revlog] = 0|0|0|0
  exit=0        adb forward --list → 0 条
```

`ast_mur3kclr_zmscrh` 与 §4.84 记录的笔记 id 一致，可确认删的是设备本地加密库里那条，
不是别的库。localStorage 清理**自带证伪信息**：两个键改前都存在（`true`）、改后被移除。

**负控（证明新加的硬失败分支不是装饰品）：** 复制真脚本，只把页内表达式换成
`() => 'not-json-shape'`（模拟「CDP 应答了但形状不对」）：

```
before [decks|notes|cards|revlog] = 0|0|0|0
❌ localStorage 清理失败：CDP 返回了非预期形状："not-json-shape"
   前置没生效就不能声称「已清零」——否则这轮会静默地测错分支。
exit=1        adb forward --list → 0 条
```

三点都被这次负控坐实：**转红**（旧代码这里会打 ✅）、**没走到 PG 删除**、**失败路径的 forward
也清干净了**（旧代码这条路径会把 forward 留在机上）。

> 注意负控的方向：我是**改坏被观察的对象**（让形状不对），看**判据**会不会红。
> 早先那次 `hideKeyboard` 负控方向反了——要求真实实现返回 true，于是「没复现」
> 被我读成「白做」，其实是我之前的因果解释错了。

## §4.86.4 不实日期：只改我拥有的，剩下 39 个交给拥有它们的会话

| 范围 | 文件数 | 处置 |
|---|---|---|
| `.maestro/*.yaml` | 6 | ✅ 已改（共 12 处） |
| `scripts/*`（`maestro-run.mjs` / `start-local-backend.ps1` / `install-apk-to-device.ps1` / `tasks-crud-fixture.mjs` / `verify-bug-ax-401-on-device.mjs`） | 5 | ✅ 已改（共 24 处） |
| `docs/handoff/2026-09-30-android-e2e-bug-d-e-f.md` + `_part-4.82/83/84` | 4 | ✅ 已改（共 10 处） |
| `frontend/**` | 25 | ❌ 不动 |
| `backend/**` | 8 | ❌ 不动 |
| `docs/handoff/` 其余（email / font-scale / invoice 三条线的 handoff） | 6 | ❌ 不动 |

**为什么剩下 39 个不批量改**，两条理由，第二条更要紧：

1. `frontend/`、`backend/` 与那 6 个 handoff **正被别的会话写**。批量改会制造冲突；
   共享分支上制造冲突等于逼合并者单侧取舍，那会**静默覆盖对方工作**。
2. **同一个 token 有两种语义。** `backend/internal/email/invoice_future_date_test.go:68/94`
   里的 `2026-10-03` 是**有意构造的测试数据**（测试自己的「明天」= `at(2026, 10, 2)` + 1 天），
   盲目全仓替换会把测试改坏。另外
   `docs/handoff/2026-10-02-round10-...-future-dates.md` 里的 `2026-10-03` 是
   **故意引用的错误日期范例**。所以这类清扫必须逐条判语义，不能当正则题做。

这也不是新问题：`2026-10-02-round9` 已把它记成缺陷类，round12 写的是「约 20 个文件」，
本轮实测已是 39 个（在别人持续新增注释）。**根因是没人有一条「日期不许超前」的判据**，
靠人记是记不住的。

**本轮用的做法（可复用）：** Buffer 级字节替换，**不做任何编码往返**（避免 UTF-8 被二次编码
毁掉——`Get-Content -Raw` → `Set-Content -Encoding UTF8` 那条路会毁中文）。
自证用了一个物理不变量：`2026-10-03` 与 `2026-10-02` **等长** ⇒ 改完每个文件字节数必须一模一样。
15 个文件全部通过，字节数校验失败 0 个（handoff 仍 533027 字节、`maestro-run.mjs` 仍 61715 字节）。
改完 `git diff --stat` 每文件变更行数恰好是替换处数的 2 倍 ⇒ 没有产生整文件假 diff。

## §4.86.5 本轮遗留

- **还有 140 个 `.mjs` 硬编码固定 CDP 端口**（这就是上面那张表的当前值；本轮从 142 降到 140，
  减的正是 `pkm-test-fixture.mjs` 与 `flashcards-test-fixture.mjs` 这两个）。
  helper 与门禁已就位，但**其余 138 个没迁**。门禁目前是独立工具，未接 `gates`（理由见 §4.86.2）。
- **154 个 forward 绑定点里 127 个不清理**。只迁 2 个夹具不改变这个局面。
- **BUG-V11 的修复仍需生产 env 变更授权**（`POCKET_ALLOWED_ORIGINS` 补三个壳 origin），
  本机不做 —— 共享部署，单方面改 env 不合适。
- `verify-https-prod.mjs` 的第 2、3 项仍需 `POCKET_PROD_PASS`（本机没有，不猜）；
  **且在 CORS 修好前这两项必然失败**，顺序上应先修 CORS。
- 功能点遗留（全部未做）：BUG-AX 设备侧负控、闪卡两入口渲染/点击、会议写入设备侧持久化、
  「tap 报 COMPLETED 但没反应」的坐标对账、`:param` 模板、gateway 六页、
  `_login.yaml` 孤儿（无任何 flow 引用它）。
- 待产品定范围：Keystore 原生插件（**已确认全平台不可用**，`StubKeystore` 11 个方法全
  `Promise.reject`）、同步编排层、改密入口、gateway 四页、BUG-AV、i18n ~800 条、
  BUG-AR（`default` 分区历史数据是否自动迁移）、PKM 删除入口、BUG-AQ/AK、
  `/contacts` 后端缺端点、`TICK_MS=30s` 与后台暂停默认值、录音离开页面后是否应继续。

### 下一轮建议的第一件事

先把 `check-fixed-cdp-ports.mjs` 改成**基线棘轮**（记录当前 140 的文件清单，只对新增违规失败），
再接进 `gates`。这样债务不会继续涨，且不会因为存量把别人的流水线打红；
之后每轮顺手迁几个脚本，数字单调下降。
