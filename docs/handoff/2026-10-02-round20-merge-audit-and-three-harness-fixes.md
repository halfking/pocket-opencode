# Round 20 —— 合并审计轮：四类分支/脏状态清理 + 三处验收装置自身的缺陷

日期：2026-10-02 22:0x
基线：origin/main `3404057b`
本轮自己的提交：`81ef7637` → `78e36b44` → `cf8adbe0` → `4457cca5`（合并）

---

## 0. 一句话结论

这轮**没有发现新的产品缺陷**。发现的是三类"装置"问题：仓库里有 24 个被跟踪的
运行时日志在每次启动时弄脏工作区、有 4 个子分支的合并状态被误读、以及**三个验收
脚本各自输出的那行绿灯/报错，说的不是它们声称要测的那件事**。全部已修并附负控。

---

## 1. 拉取与合并

- `git fetch` 后 main 落后 origin/main 11 个提交、领先 6 个。
- **文件交集用 `git log --name-only A..B` 算，不用 `git diff --name-only A..B`**：
  分叉分支上两点 diff 是双向树差异，会把对方的文件算成"我少了它"。按提交枚举后
  实测交集 = 0（我 10 文件 / 对方 16 文件），合并零冲突。
- 合并后又来 1 个（`3404057b` 同类 MIME 修复），再次合并，1602/1602 仍全绿。

### 一条被丢弃的本地改动（值得记）

工作区里 `invoice-totals-chain.test.mjs` 有一份未提交改动，是"补齐兜底用例夹具 +
加一条镜像用例"。**origin/main 的 `f6af6026` 已经做了同一件事，且做得更强**：
夹具取自真实库那两行（3500 downloaded+有文件 / 58000 new+无文件）并加了
filePath 空串、status=new 但有文件两条边界，还额外断言 `groups[0].count` 和
"无过滤求和不会把 65,100 显示给用户"。

本地那份是弱化版，直接 `git checkout --` 丢弃。**判据是"是否已被更强的同义改动
覆盖"，不是"是不是我写的"。**

---

## 2. 分支盘点：4 个子分支全部 0 未合并提交

| 分支 | ahead(main) | 最后活动 | 处置 |
|---|---|---|---|
| `audit/round20` | 0 | 2 min | 活跃，不动 |
| `mvs/email-fixes-20261002` | 0 | 4 min | 活跃，不动 |
| `verify/e2e-20261002-v2` | 0 | 10 min | 活跃，不动（origin/main 指着它） |
| `verify/e2e-20261002` | 0 | 118 min | **已删**（先 `git worktree remove`，工作区干净） |

**没有任何分支需要逐文件合并**——`audit/round20` 那个"MIME 泄密"提交
（`07910a32`）早已被并发会话合进 origin/main。另清掉一个陈旧远端引用
`refs/remotes/https/main`（remote 配置已不存在，只剩 ref）。

**两个 detached worktree 故意没删**：`openpocket-wt-apkbuild`、`openpocket-wt-base`，
两者提交都已在 main 里，但 `wt-base` 有 1 个未提交改动（并发会话可能正在用），
删目录是不可恢复动作，不在"清理不活跃分支"的授权范围内。

---

## 3. 修掉的三个真问题

### 3.1 `logs/` 早已声明忽略，24 个文件却仍在版本控制里

`.gitignore:44-45` 写着 `logs/`，但这些文件在加规则之前就入库了，而 git 不会因为
`.gitignore` 取消跟踪。后果不是"多几个文件"——**这轮 `logs/pocketd.log` 被一条仍在
运行的 pocketd 直接截断成 0 字节**（进程启动时 `>` 覆盖重定向，打在一个被跟踪的
文件上）。于是 `git status` 永远挂着一串日志噪声，真正的改动被淹没。

`git rm -r --cached logs`（只动索引，磁盘 720 个文件一个没删）。
前人在 `.gitignore:115-116` 已写明这个坑并选择容忍，本轮不再容忍：这些是
2026-07-07 的运行产物，真证据走 `test-evidence/` 的按日期归档。

顺带补 `.gitignore` 的第二个漏网点：`/wt*/` **匹配不到点目录**，
`git worktree add .wt-e2e` 建出来的嵌套检出一直报未跟踪，而 `git add -A`
会把整份第二检出（数千文件）扫进暂存区。

### 3.2 `loadVersionConfig` 的注释写反了路径方向 → App 一直报 1.2.0

注释写"默认路径：相对于**可执行文件**"，实现是 `os.ReadFile("config/version.json")`
——相对**进程 CWD**。两个启动脚本都 `Set-Location` 到仓库根再启 pocketd，
于是永远读不到 `backend/config/version.json`。

真正致命的是**静默**：读不到时返回内置默认值、`error = nil`，调用方拿到一个
"合法的" VersionInfo，没有任何东西指向"路径不对"。

改动：注释写对 + 显式设环境变量时不做猜测 + 未设环境变量且 CWD 落空时再试可执行
文件目录（`<root>/bin/pocketd` + `<root>/config/` 是常见布局）+ 3 条新用例。

**负控**：把 `explicit` 改成恒 false（= 无视环境变量）→
`TestLoadVersionConfig_EnvVarWins` 转红且**只有它**转红 → 恢复 3/3 绿。

### 3.3 三个验收脚本量错了对象

| 脚本 | 声称在测 | 实际在测 |
|---|---|---|
| `backend-endpoint-matrix.mjs` | 端点矩阵可用 | 从已删除的源码常量抓口令，每天 exit(3)，报错 `CANNOT_READ_DEV_PASS` 指向一个**不存在的东西** |
| `install-apk-to-device.ps1` | 手机能否连到后端 | **这台机器能否连到自己**（Test-NetConnection 在主机侧） |
| `start-pocketd-*.ps1` | （隐式）版本信息正确 | 依赖 CWD，而 CWD 与注释假设的路径方向相反 |

第一个与 `ff6eba23` 对 `maestro-run.mjs` 的修法同源（`maestro-run.mjs:455` 已是
`POCKET_AUTH_PASS || 源码正则`），是同一条缺陷的漏网之鱼。

---

## 4. 一次自我推翻：注释里写了一句现在不成立的话

`install-apk-to-device.ps1` 的改动初稿注释写"手机连 ARP 都解析不了
192.168.31.20（AP 客户端隔离）"。

本轮在真机（Redmi 2411DRN47C / Android 14，adb `192.168.31.19:5555`）实测：

| 探测 | 结果 |
|---|---|
| 设备 → `localhost:18099`（adb reverse） | **200** |
| 设备 → `192.168.31.20:18099`（APK 烤进去的） | **200** |
| 主机 → `192.168.31.20:18099`（旧检查） | **True** |

**三个都是绿的，分叉不复现。** 那句话是写它当时的观测。

已改写为不变量：主机侧的绿灯**无论什么时候**都不足以区分"清数据后手机仍能连到
烤进去的地址"和"只有主机能连"——而后者正是那个要清数据重装的场景。
**分叉是历史，盲区不是。**

顺带核到设备有 `/system/bin/curl`（探针依赖它），且探针取不到 3 位状态码时返回
字面量 `unreachable`，即 curl 缺失会降级成告警而不是静默通过。

---

## 5. 顺带核验的两个既有护栏（本轮没改它们，只验证它们有牙齿）

`backend/internal/email/snippet_mime_leak_test.go`（`07910a32` 引入）的元护栏
`TestLeakDetectorItselfDetectsRecordedDeviceLeak` 自称"把 needles 换成 nil 会转红"。
实测：注入占位 needle → **该元护栏与业务护栏双双转红** → 恢复全绿。

`frontend/src/api/error-message.ts` 新增规则同样做了负控：整行摘掉 →
`not ok 5 - 网关节点缺 admin 账号` → 恢复全绿。

第一版负控脚本是**手抄整行字面量**的，文件是 CRLF，anchor 静默失配——
是脚本里那条"anchor 找不到就抛错"的断言让它当场失败，而不是变成一个 no-op
然后报告"负控通过"。

---

## 6. 测试命令与结果

| 命令 | 结果 |
|---|---|
| `go build ./...` | exit 0 |
| `go vet ./...` | exit 0 |
| `go test ./...` | exit 0（无 FAIL 行） |
| `go test ./internal/server/...` | ok 19.5s |
| `vue-tsc --noEmit` | exit 0 |
| `npm run test:all` | **1602 tests / 1602 pass / 0 fail**，174 个测试文件全部有产出 |
| `build:gate` `test:native` `test:stores` `test:auth` `test:styles` `test:stt` `test:email-heal` | 全 exit 0 |
| `check:dueclock` `check:vm-gaps` `check:i18n` `check:i18n-translated` `check:dead-api` `check:raw-error` `check:crlf-needles` `check:icons` `check:test-coverage` | 全 exit 0 |

---

## 7. 遗留风险

1. **并发会话仍在同一棵树上写**。本轮提交前后，`pg_test_isolation_guard_test.go`
   与 `diag_reminder_backlog_test.go` 被另一个会话写入并提交（`f570b188`），
   且是在我的提交**之上**。所有暂存都按显式路径执行，没用过 `git add -A`。
   但"提交前 fetch 重查"这条在本轮被验证是必要的：合并前后 origin 各多出一次提交。
2. **两个 detached worktree 未清理**（见 §2），其中 `wt-base` 有未提交改动。
3. **`loadVersionConfig` 仍然静默**。本轮只补了"注释写对"和"多试一个路径"，
   没改它 `error = nil` 的契约——改了会让 `/api/app/check-update` 在任何缺配置
   的部署上 500，比现状更难排查。第三条用例把这个**已知行为**钉住了，
   要改必须连同调用方一起改。
4. **`install-apk-to-device.ps1` 的设备探针依赖 `/system/bin/curl`**。本机设备有，
   别的机型没有时会报 `unreachable` 并 `exit 4`。这是"响亮地失败"而不是静默通过，
   但对没有 curl 的机型它是误报。
5. **`backend-endpoint-matrix.mjs` 的源码常量兜底已成死代码**（常量早被删）。
   留着无害，注释已标明只给老 checkout 用。

---

## 8. 下一轮提示词

```
对 openpocket 做下一轮修正：

1. 先 git fetch，再用 `git log --name-only A..B`（不是 `git diff --name-only A..B`）
   算与 origin/main 的文件交集，再合并。上轮 origin 在 40 分钟内两次前进。
2. 处理两个未清理的 detached worktree：openpocket-wt-apkbuild、openpocket-wt-base。
   先查 wt-base 那 1 个未提交改动是谁的；确认可弃再 worktree remove。
3. 决定 loadVersionConfig 的静默回落要不要改。若要改，必须同时改
   handleCheckUpdate 的调用方，并更新 version_config_test.go 第三条用例
   （那条用例现在钉住的正是"静默"这个行为）。
4. 继续需求 2/3/4/7 与需求 6 的真机验收。已知待拍板项见 round19 handoff：
   32 条积压提醒是否限流、旧名副本删否、58000 误建档行、需求 6 路由三问。
5. 所有新护栏必须带负控实测（注入缺陷 → 确认转红 → 恢复），并在提交信息里
   写明转红的是哪几条用例。
```
