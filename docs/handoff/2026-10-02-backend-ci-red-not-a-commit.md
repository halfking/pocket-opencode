# backend CI 连续 104 次红：根因不在任何一次提交

日期：2026-10-02
范围：`halfking/pocket-opencode` 的 `backend` workflow

## 结论

`backend` workflow 自 2026-09-30T04:53 起**连续 104 次 run 全部 failure**，
失败步骤恒为 `Run tests`（`go test -race ./... -count=1`）。

**这 104 次红与任何一次代码改动都无关。** 「二分找坏提交」在这个问题上是错的方向。

## 证据一：绿→红边界上，后端目录零差异

| run | SHA | 结论 | 时间 |
|---|---|---|---|
| #366 | `0740047` | **success** | 2026-09-30T04:52:30Z |
| #367 | `d9b63a2` | failure | 2026-09-30T04:53:25Z |

两次相隔 55 秒。`git diff 0740047 d9b63a2` 的**全树**结果：

```
 docs/handoff/2026-09-30-android-e2e-bug-d-e-f.md | 86 +++++++++++++++
 scripts/append-handoff-4.mjs                     | 102 +++++++++++++++
 scripts/audit-entry-reachability.mjs              | 128 ++++++++++++++++
 3 files changed, 316 insertions(+)
```

即：**0 个 Go 文件、0 行 `go.mod`/`go.sum` 变更、0 行 workflow 变更。**

同一份后端代码树，先绿后红。所以失败不是代码引入的。

补充：#371（`22a3de5`，09-30T05:50）与 #470（`4cd6e7e`，本会话 HEAD）
的失败 job 步骤序列逐项相同——`Set up job` / `Initialize containers` /
`Check out repository` / `Set up Go` 全部 success，`Run tests` failure，
`Run vet` 与 `Build backend` 被 skipped。失败点没有迁移过。

## 证据二：CI 那条命令在本机是通过的

CI 环境此前无法复现，原因是本机 `CGO_ENABLED=0` 且没有 C 编译器，
`-race` 跑不起来。机器上其实**已经装了便携 mingw-w64**：

```
C:\tools\w64devkit\w64devkit\bin\gcc.exe   # GCC 16.2.0, x86_64-w64-mingw32
```

配上它之后，按 CI 原样跑（`CI=true`、独立的 `pocket_test` 库）：

```
go test -race ./... -count=1 -p 2     ->  EXITCODE=0   189s   65 packages ok   0 DATA RACE
go test -race ./... -count=1          ->  63 packages ok   0 DATA RACE
```

两次全量真实包全绿，**一次 data race 都没有**。本会话的 9 个提交因此被排除。

## 证据三：一个我自己造出来、并且已纠正的假阳性

第一次跑 `-race` 时出现 11 个失败：

```
--- FAIL: TestStore_CreateAndGet
    Init failed: ERROR: permission denied for schema public (SQLSTATE 42501)
--- FAIL: TestPGStore_CreateScoped_Concurrent
    NewPGStore: finance migration failed: ERROR: permission denied for schema public
```

**这是我搭测试环境时造成的，不是 CI 的病因。** 我建的 `pocket_test` 角色只给了
database 级权限，而 CI 里该角色由官方 postgres 镜像以 `POSTGRES_USER` 创建、
**是超级用户**；PG 15+ 又取消了 `public` schema 对 PUBLIC 的 CREATE 默认授权。
把角色对齐成 `SUPERUSER` 后这 11 个全绿。

记录这一点是因为它演示了这类故障的形态：**报错指向业务代码，真因在环境**。

## 仍未确定的部分

剩下的差异只有三项，都无法在本机复现：

1. **linux vs windows** —— 文件路径分隔符、文件名大小写、checkout 行尾（CRLF/LF）
2. **PostgreSQL 17 vs 本机 16.4**
3. **runner 资源/镜像** —— 注意 run 里的 annotation 提示
   `ubuntu-latest` 将在 2026-10-19 迁到 Ubuntu 26

一个值得注意的量化线索：CI 上 `test` job 耗时约 **119 秒**（#470：
13:19:04→13:21:03），而本机完整跑完要 189 秒。CI 是**提前失败**的，
没有跑完整个测试矩阵——所以失败集中在靠前完成的某个包，不是全局性崩溃。

## 下一步（性价比最高的一条）

Actions 日志接口需要 admin（`403 Must have admin rights`），check-run 的
`annotations_count: 3` 里只有 `Process completed with exit code 1.`，没有用例名。

**请打开 backend run #470 的 `test` job，把失败那几行贴过来。**
有了用例名，上面三项可以立刻收敛；没有的话，任何进一步猜测都是无根据的。

## 复现配方（本机跑 CI 那条命令）

```powershell
$env:PATH = 'C:\tools\w64devkit\w64devkit\bin;' + $env:PATH
$env:CC   = 'C:\tools\w64devkit\w64devkit\bin\gcc.exe'
$env:CGO_ENABLED = '1'
$env:CI = 'true'
$env:POCKET_TEST_POSTGRES_DSN = 'postgres://pocket_test:pocket_test@127.0.0.1:5432/pocket_test?sslmode=disable'
cd backend
go test -race ./... -count=1
```

`pocket_test` 角色必须与 CI 对齐：

```sql
ALTER ROLE pocket_test WITH SUPERUSER CREATEDB;
GRANT ALL ON SCHEMA public TO pocket_test;
```

（`GRANT ALL ON DATABASE` 不足以让测试在 `public` 建表。）
