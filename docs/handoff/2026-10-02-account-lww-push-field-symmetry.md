# 缺陷 14：账户 LWW 上行只推 3 个字段 —— IMAP 主机等改动被静默丢弃

日期：2026-10-02
分支：`feat/mail-config-deploy`
状态：**已修**

对应需求 8「这个信息有最后修改时间，在服务端与客户端中，以最后时间为准来
更新旧的一方」。

---

## 缺陷

下行 `buildMirrorAccountWrite` 会用服务端值覆盖本地这 7 列：

```
display_name, email_address, imap_host, imap_port,
auth_type, sync_interval_min, enabled
```

上行 `pushAccountToServer` 的 patch 只有 3 个：

```
display_name, sync_interval_min, enabled
```

**字段集不对称。**

## 后果：改动凭空消失，且无任何报错

```
用户改 IMAP 主机 → 本地 updatedAt 变大
  → planAccountSync 判「本地更新」→ 触发上行
  → payload 里没有 imapHost，服务端原样不动
  → 下一轮下行又用服务端的旧 imapHost 覆盖回来
  → 用户改动消失
```

服务端 `updateEmailAccount` 的 body **明确接受**这三个字段
（`IMAPHost` / `IMAPPort` / `AuthType` 三个指针），所以是客户端漏发，
不是服务端不支持。

`imapHost` / `imapPort` / `authType` 恰好是「换服务商要改的那几个字段」，
也就是用户最常改的。

## 修法

1. `pushAccountToServer` 的 patch 带上 `imapHost` / `imapPort` / `authType`。
2. 调用点必须**逐个用本地值覆盖**——`pushAccountToServer({...target, ...})`
   里的 `target` 是服务端对象，直接 spread 等于把服务端旧值原样发回去。
   字段带上了但值取错，效果与没修相同。
3. 新增 `narrowAuthType()`：本地 SQLite 读回的 `auth_type` 是 `string`，
   服务端要 `AuthType`（`'password' | 'oauth2'`）。非法值回落到 `password`
   而不是原样上行——否则一个被改坏的镜像值会让服务端 400，整轮 LWW 同步卡住。
   （这个函数是 `vue-tsc` 逼出来的：TS2322。）

## 验证

新增 `account-push-field-symmetry.test.mjs`，4 条结构断言 + 3 条行为断言：
- 下行 UPDATE 的每一列，上行 patch 都要能带上
- 上行取的是 `l.imapHost`（本地），不是 spread 的 `target.imapHost`
- 服务端 body 确实接受这三个字段
- `narrowAuthType` 的**真实行为**：合法值透传，非法/空/null/undefined 回落

**负控 3 路，每一条都先被抓出「假绿」再修正：**

| 负控 | 注入 | 第一次结果 | 处理 |
|---|---|---|---|
| 1 | `imapHost: undefined` 等 | **4/4 全绿（假绿）** | 判据只查了字段名，改为断言「键: 值」形式 + 显式禁止 `xxx: undefined` |
| 2 | 字段改用 `target.imapHost` | 转红 | — |
| 3 | 去掉 `narrowAuthType` 的回落 | **6 pass / 0 fail（假绿）** | 见下 |

### 两次假绿的教训

**判据 1（`imapHost: undefined`）**：判据是 `body.includes('imapHost')`，
而 `imapHost: undefined` 同样包含这个字样。**纯文本匹配会把自己的护栏喂饱。**
改成断言 `imapHost: _a.imapHost` 这样的键值对，并显式禁止 `xxx: undefined`
（`JSON.stringify` 会把它整个抹掉——字段名在、值没带，正是缺陷本身的形态）。

**判据 3（`as AuthType`）**：负控用了 TS 的 `as` 断言，而我的类型剥离规则
只覆盖了参数标注、没覆盖 `as`，于是 `describe` 块在**收集阶段**抛
SyntaxError，**整块用例被静默跳过**，汇总显示「# pass 4」——看起来全绿，
实际那两条行为测试根本没运行。

修法两条：剥离规则补上 `\bas\s+Type`；并加一条「函数能被加载求值」的前置
断言，把「整块跳过」从静默变成显式失败。

> 这与「负控因注入未生效而不转红」是同一类问题的另一个面：
> **测试没跑**和**测试跑了但判据太松**，在汇总里长得一模一样（都是 pass）。

回归：`vue-tsc --noEmit` exit 0；email + config 域 35 个测试文件 301 例全绿。

## 顺带核实（无缺陷）

- 163 的 RFC 2971 `ID` 客户端标识头：IMAP（`fetcher.go:483`）与
  POP3/裸 IMAP（`mime.go:220`）两条路径都在 SELECT 之前发送。
  `mime.go:92-96` 的注释记录了「常规同步成功但拉原文必失败」的已修缺陷。
- 服务端 LWW 守卫：`UpdateAccountLWTScoped` 返回 `ErrStaleWrite` → 409 +
  当前 `updatedAt`，客户端据此改走下行。链路完整。
- `account-lww.ts` 的 `pullIds` 实际未被 `account-sync.ts` 使用（下行靠
  `writeAccountIfNewer` 自己判）。逻辑重复但不致错，未改。

## 2026-10-02 09:45 补充：LWW 在真机运行时上的双向取证

前面「服务端 LWW 守卫链路完整」是**读代码**得出的。本节是在真实 Android
运行时上把两个方向都打了一遍的结果（模拟器 `emulator-5556`，app pid 21009，
经 CDP 在 App 的 WebView 内发起，用 App 自己的 token 与链路）。

### 下行：服务端 → 客户端本地库

| 环节 | 实测 |
|---|---|
| `GET /api/email/accounts` | 200，`user-admin` / `ws_user-admin` 名下 **5 个账户** |
| App 界面 `#/email/accounts` | 5 个账户全部渲染，主机端口正确 |
| 设备本地 SQLCipher 镜像库 `lobster` | `local_email_accounts` **5 行** |
| 字段比对 | `updated_at` 等与服务端**逐字段一致**（均为 1790875519） |

### 上行守卫：过期基准版本号必须被拒

模拟「本地拿着旧副本改了一下就推」：取账户当前 `updatedAt`，减 1000 当作
本地基准版本号发 `PUT /api/email/accounts/{id}`。

```
stalePutStatus = 409
stalePutBody   = {"error":"stale write: server copy is newer","updatedAt":1790875519}
```

复核那次被拒的 PUT **没有改动任何字段**（`displayName` / `imapHost` /
`updatedAt` 与请求前完全一致）。即守卫不仅拒绝，还回传当前 `updatedAt`——
正是 `account-sync.ts` 收到 409 后重新下行的依据。

### 未能取证的部分（如实记录）

**上行「成功写入」路径没有在设备上验过**，因为那会真实改动服务端数据。
已验的是：上行守卫会正确拒绝过期写。且**上行 payload 是否真的带上了
`updatedAt`** 仍只有单元测试与代码审读作证，没有设备侧实证。

### 一个会让人误判的闸门

`account-sync.ts` 在本地库未就绪时直接
`return { fetched: remote.length, applied: 0, ..., online: true }`。
设备端表现是进 `#/email/accounts` 被弹到
`#/login?returnTo=/email/accounts&unlock=1`，页面写「检测到已有登录态，但本地
加密库未解锁」。

即：**本地库锁着的时候一个账户都不写本地，却报告 `online: true`**。
真机首次使用必须先设主密码解锁，否则会误判成「同步没生效」。这是有意
为之（不把 LocalDB 未初始化当成同步失败），未改。

