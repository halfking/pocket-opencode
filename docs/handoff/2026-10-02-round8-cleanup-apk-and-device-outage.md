# round8 收尾：孤儿卡口已存在并经负控验证；APK 归因到 29048294；真机在装包瞬间掉线

日期：2026-10-02
分支：无（全部结论在 main 上，只新建本文件）

## 0. 本轮最重要的结论：**5 条待办里有 3 条的前提已经不成立**

接手时按字面准备执行 5 条，逐条核对后发现一半的阻塞描述是**过期的**：

| 待办 | 接手时的前提 | 实际状态 |
|---|---|---|
| ① 删 `audit/round8-2026-10-01`、`audit-snapshot-rd7` | 两个分支存在、且内容已被 main 覆盖 | **两个分支都已不存在**（本地与远端皆无），无需删除 |
| ② 两分支的外部凭证阻塞 | 阻塞待确认 | **确认仍未解除**，但本轮不需再查 |
| ③ 加孤儿测试卡口 | 卡口不存在，要新写 | **已由 `c1bf953b` 实现并在 main 上**，本轮改为验证它有效 |
| ④ 先解决 adbd 零回包 | adbd 不通 | **已自然恢复**，阻塞不存在；但恢复后又有新掉线（见 §3） |
| ⑤ 重建 APK 并按 dirty=0 确认 | APK 需重建 | **已完成**，dirty=0 @ `29048294` |

**教训**：待办清单是上一轮会话的**快照**，不是当前事实。本轮每一条都先做
`git rev-parse` / `pm list packages` / `adb devices` 级别的实证核对，
才发现 3 条的前提已失效。**照着待办直接动手会做无用功，
更糟的是会把「已解决」重做成「再修一遍」。**

## 1. 孤儿测试卡口：已存在，负控实测能转红

`c1bf953b`（在 main 上）已实现，`frontend/scripts/check-test-coverage.mjs`
接在 `gates` 末尾。基线：

```
gates 可达脚本 19 个 · 测试文件 162 个（.mjs 107 / .ts 55，2 个豁免）
被覆盖 160 / 160   ✅ 无孤儿测试文件
```

**但「它会红」这件事没有证据就不能算护栏。** 本轮做了负控，两次，
第一次还放错了位置：

| 负控 | 放置位置 | 结果 | 判读 |
|---|---|---|---|
| NC-A | `src/styles/__tests__/zz-nc.test.mjs` | **仍绿**（161/161） | **无效负控**——`test:styles` 的 glob 正好覆盖这里 |
| NC-B | `frontend/tests/zz-nc.test.mjs`（src 之外） | **红，exit 1**，点名该文件 | 判据确实在判 |

NC-A 变绿不是护栏坏了，是**负控放错了地方**：判据只覆盖 gates 可达的 glob，
放进已被 glob 覆盖的目录当然触发不了。判据本身没问题（NC-B 证明），
但这提醒：**选负控位置时要挑「判据应当覆盖、而实际没覆盖」的那一类**，
放在天然被覆盖的目录下等于没测。

两个负控文件都已删除（走 `rm --` 回收站），复跑回到 160/160。

## 2. APK 归因：dirty=0 @ `29048294`

```
commit  : 29048294551e02c1622dc75e45db5733c47a8c76  (dirty=0)
sha256  : 1E6DA6F588E71E99DB477948BEC4817EDBE50C2EFBA171E7411E697627A07221
size    : 34044622
bundle  : dist\assets\index-DAqDTu3h.js
VITE_API_BASE=http://192.168.31.20:8088
```

在**独立 worktree** `C:\workspace\openpocket-wt-apkbuild`（detached HEAD）
里构建，没在主工作区动手——构建期间主工作区有并发会话在跑
`.scratch-sttdev` 后端（见 §6）。

### 2.1 踩坑：`node_modules` 用 junction 会让 dirty 永远不为 0

`wt-apkbuild` 没有 `node_modules`，我用 `mklink /J` 指向主工作区。
结果 `cap sync android` 把两个**被 git 跟踪**的生成文件改写成绝对路径：

```diff
-project(':capacitor-android').projectDir = new File('../node_modules/...')
+project(':capacitor-android').projectDir = new File('../../../openpocket/frontend/node_modules/...')
```

`capacitor.settings.gradle` 与 `capacitor.build.gradle` 变 dirty，
**dirty=0 判据直接不成立**。junction 指向的是同一份文件，内容等价，
所以处置是 `git checkout --` 还原这两个生成文件后**再 assemble 一次**，
而不是接受 dirty=2 的产物。

**关键验证**：还原前后两次构建产出的 sha256 **完全一致**
（`1E6DA6F5…`），这才敢声称「还原没有改变 APK 内容」。
只还原不重编、或只重编不比对，都会把「内容没变」当成结论而非证据。

### 2.2 一次「哈希没变」的假警报

第二次构建（切到新 HEAD `29048294` 后）产物 mtime 与 sha256 都没变。
查了 `git show --name-only 29048294`：只改了一个 handoff 文档和一个
`__tests__` 文件——**都不进 bundle**，所以 APK 字节不变是**正确**的，
不是没重新构建。**哈希不变要先解释成因，再当成异常。**

## 3. 真机：本轮最大的坑 —— 阻塞解除了，然后又在装包时掉线

### 3.1 接手时 adbd 已经是好的

`2026-10-02-real-device-adbd-not-speaking.md` 写的是
`192.168.31.19:5555` TCP 通但 8 秒 0 字节。实测**已经恢复**：

```
4c308e2e               device   ← USB
192.168.31.19:5555     device   ← WiFi，与 USB 同为 4c308e2e
```

`adb shell` 返回真实数据（`uid=2000(shell)`、`ro.serialno=4c308e2e`）。

### 3.2 `probe-adbd-handshake.mjs` 是**假阴性**，别再拿它当判据

恢复后重跑那个探针，仍然 `NO-REPLY`：

```
[8012ms] 收到 0 字节 —— 端口开着但对端不说 ADB 协议。
```

**但 adb 自己是通的。** 原因：WiFi 5555 已被 adb server 建立的连接占用，
探针再另开一条裸 TCP 过去，对端不接。**这条探针只有在「adb 完全没连上」
时才有诊断价值**；一旦 adb 连上了，它必然报 NO-REPLY。
接手文档把它当「决定性判据」，会让人误判成阻塞未解除——
本轮差点就这么误判。**判据要问：它测的是不是同一个对象/同一条链路。**

### 3.3 然后设备真的掉了（这才是当前阻塞）

装 APK 时：

```
Performing Streamed Install
adb.exe: device offline      ← INSTALL_EXIT=1
```

`adb kill-server` + `start-server` 后**所有 transport 全空**
（USB 与 WiFi 一起没了，emulator-5556 也 offline），重连：

```
cannot connect to 192.168.31.19:5555 ... (10060)
cannot connect to 127.0.0.1:5556  ... (10061)
ping 192.168.31.19              → False
Test-NetConnection :5555        → False
```

**ping 都不通 ⇒ 设备离开网络，不是 adb 层问题**，宿主侧无解。
需要有人在手机上操作：确认 WiFi 仍连着 `192.168.31.19`、
重新开一次无线调试（端口会变，要回报新端口）、必要时重启手机。

⇒ **APK 已构建好但尚未装机**，设备一回来直接：

```powershell
$adb="C:\Users\86133\AppData\Local\Android\platform-tools\adb.exe"
& $adb connect <新端口>
& $adb -s 192.168.31.19:<新端口> install -r -g `
  C:\workspace\openpocket-wt-apkbuild\frontend\android\app\build\outputs\apk\debug\app-debug.apk
```

## 4. Maestro 其实**早就装好了**（又一个过期的阻塞描述）

`2026-09-30-android-e2e-bug-d-e-f.md` 记的是「Maestro 装不上，
需用户手动开 USB 安装」并称真机零次执行。实测：

- CLI 在**非 PATH 位置**：`C:\workspace\openpocket\logs\maestro\dist\maestro\bin\maestro.bat`，`--version` → `2.11.0`
- driver 已在设备上：`dev.mobile.maestro`、`dev.mobile.maestro.test`
- `.maestro/notes-stt-error-visibility.yaml` + `_connectivity-sttdev.yaml` 都在

所以 STT 流的阻塞**不在 Maestro**，只在 §3.3 的设备掉线。
接手时若只按 handoff 文字判断，会误以为要先去装 Maestro。

另：设备在线期间实测过设备→宿主连通性（当时 8088 有实例）：

```
adb shell curl http://192.168.31.20:8088/healthz  →  200
```

## 5. 两个分支的凭证阻塞：确认仍未解除

`POCKET_FEISHU_APP_ID` / `APP_SECRET` / `INVOICE_CHAT_ID` /
`POCKET_KXMEMORY_BASE_URL` 全部缺失（env 与仓库内均无）。
这属于**只能由用户提供**的外部条件，不是代码问题，本轮不重复排查。

`feat/mail-config-deploy` 还有**未提交改动**
（`server_auth_extended_test.go` + 一份 handoff），
说明那个 worktree 可能仍有会话在动——**合并前先确认无人使用**。
`email-pipeline-snapshot-2026-10-01` 未提交改动已清空（`git status` 干净）。

## 6. 并发提醒：本轮全程有别的会话在写同一个仓库

| 时刻 | 观察 |
|---|---|
| 10:05–10:07 | `wt-email` / `wt-maildeploy` / `wt-font` 文件 mtime 持续更新 |
| 10:09 | 主工作区出现 `.scratch-sttdev/`（`pocketd.exe` 10:09:03 落盘） |
| 10:12:36 | 主工作区 `api-timeout-budget-table.test.mjs` 被改（**非本会话所为**） |
| 10:14 | pocketd 重启，日志 `[tasksync] disabled` |
| 10:21 | main HEAD 从 `73dbf596` 前进到 `29048294`（**别的会话推的**） |

**因此本轮全部构建动作都放在 `wt-apkbuild` 独立 worktree**，
主工作区一个字节都没改（`git status` 仅有别人的 `.scratch-sttdev/`）。
也正因如此，APK 在构建中途就落后一个 commit，只能重跑一次——
**dirty=0 是在构建时对齐的，HEAD 一动就作废**，报结论时必须写明是哪个 commit。

## 7. 待办（本轮实际状态）

1. **需要你**：手机重连 WiFi + 重开无线调试，回报新 IP:端口
2. 设备回来后：装 APK（§3.3 命令）→ 跑 `notes-stt-error-visibility.yaml`
3. 提供飞书 / kxmemory 凭证，解 §5 的两个分支
4. 确认 `feat/mail-config-deploy` worktree 是否还有人用，再谈合并
5. `check-maestro-flows.mjs` 在 `scripts/` 下但**未接进 gates**，
   可考虑并入（`.maestro` 流的静态检查目前不在回归里）
