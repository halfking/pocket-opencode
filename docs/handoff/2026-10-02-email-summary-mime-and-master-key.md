# 2026-10-02 邮件摘要缺陷修复 + master key 丢失事故

## 0. 本轮两件事

1. **需求 7 端到端打通**：模拟器收件箱成功拉取服务端 120 封真实邮件
   （证据 `logs/emu-56-inbox.png`）。
2. **新缺陷 7 定位并修复**：收件箱摘要显示成原始 MIME 编码，根因有三条。

---

## 1. 需求 7：模拟器收件箱拉到 120 封

之前一直「暂无邮件」，不是 bug，是**本地加密库从没建过**。完整链路：

1. 登录 admin（dev 旁路，`Veritrans&9527`）
2. 首次进入触发「创建主密码」对话框
3. `adb reverse tcp:18099 tcp:18099`，Build default `http://localhost:18099` 直连宿主
4. 填主密码解锁 → 邮箱页拉到 120 封

### 两个必须绕开的坑（都踩过）

- **`adb shell input text` 会经过输入法**。Gboard 在拼音模式下把 `itrans`
  变成 `IT燃烧`。主密码改用纯数字+符号 `9527&86133`（`&` 没问题，字母才出问题）。
  验证手段：在「密码提示」这个**可见**字段里先打一遍看实际结果。
- **键盘会顶住「解锁」按钮**。先 `input keyevent 4` 收键盘再点。

---

## 2. 缺陷 7：摘要显示原始 MIME

### 现象

```
------=_Part_8505717_93977514.1790821420306 Content-Type: text/html; …=E5=B0=8A…
PHN0eWxlPgogICAgLmVtbC13IHsK…
```

真库 120 封：**83 封**这种形态、**11 封**摘要为空、只有 26 封干净。

### 为什么已有测试是绿的

`snippet_test.go` 的样本是「头与正文之间有空行」的完整 MIME。
真实 IMAP 的 `BODY[TEXT]` 返回的是**正文部分**，首行恒为 boundary。
两种形态走完全不同的代码路径。

### 三个根因（都已修）

1. **Go 的 `quotedprintable.Reader` 不认 CRLF 软换行**（`mime.go`）
   RFC 2045 规定邮件用 CRLF，软换行于是是 `=\r\n`，直接报
   `quoted-printable: invalid bytes after =: "\r\r\n"`，整段正文丢掉。
   对照实验：同一段中文，LF 排版解得出，CRLF 排版解出空串。
2. **boundary 行让 `ParseMIMEMessage` 在第一行就失败**（`snippet.go`）
   `------=_Part_…` 没有冒号 ⇒ malformed header line ⇒ 后面完整的
   Content-Type / CTE / 正文全读不到。现在多试一个「剥掉首行 boundary」的候选。
3. **判据太宽，正常正文被判成 MIME 源码**（`snippet.go`）
   `looksLikeMIME` 原来是 `strings.Contains(head, ":")`，而调用点传进来的文本
   已被 `normalizeWhitespace` 压成一行 ⇒「会议改到三点：记得带季报」这种正文、
   任何带冒号的英文句子都被判成 MIME、摘要返回**空串**。
   改成 RFC 5322 field-name 判据，且 MIME 判定改用**保留换行的原文**
   （`^Content-Type:` / `^--boundary` 都是逐行正则，压平后行首锚点全失效）。

另加两条兜底（针对 `<partial>` 从正文起取、头被整个切掉）：
整体 base64 解码、**容错** QP 解码（真实 QP 正文混着 `&zwnj;` 和被实体化的
软换行，标准 reader 遇到 `&=` 就整段放弃），以及削掉粘在正文末尾的
MIME 结束边界。

### 护栏（负控是硬要求）

`snippet_partial_fetch_test.go` 覆盖 5 种真实形态，判据分两层：
**负向**（不得含任何 MIME / 编码痕迹）+ **正向**（必须真的还原出正文）。

> 只写负向是不够的。第一版就是这么写的：负控打坏 boundary 剥离，测试**仍然全绿**
> —— 因为去掉修复后函数返回空串，负向断言照样通过。护栏当场退化成永远绿的东西。
> 加了「空串即失败」的正向断言之后，5 路负控才逐条转红。

| 负控开关 | 转红的用例 |
|---|---|
| `NEGCTL_NO_CRLF_FIX` | quoted-printable-body-part |
| `NEGCTL_NO_BOUNDARY_STRIP` | quoted-printable-body-part, base64-body-part |
| `NEGCTL_NO_QP_BODY` | qp-body-only |
| `NEGCTL_NO_BOUNDARY_TAIL` | text-with-trailing-boundary |
| `NEGCTL_LOOSE_MIME` | ColonInPlainBodyIsNotMIME |

（开关是临时的，验证完已全部从代码里移除，`grep NEGCTL` 无命中。）

### 真库端到端复验

重建二进制 → 重启 18099 → 5 个账户 `last_synced_uid` 归零 → `POST /api/emails/sync`
→ `{"mode":"imap_fetch","new":120,"synced":5}`：

```
rawMIME  83 -> 23
empty    11 -> 0
clean    26 -> 97
```

---

## 3. 事故：email master key 丢失（本轮第二件大事）

重启 18099 后 5 个账户全部 `decrypt credential: cipher: message authentication failed`。

排查结论：**加密这 5 个账户凭据的那把 `email_master.key` 已经不在磁盘上了。**

- 23:52 那个实例的日志**没有** `WARN: POCKET_EMAIL_MASTER_KEY not set` 之前
  也没有走 `C:\workspace\openpocket\data\email_master.key`（该文件 09-30 起未变），
  说明它当时是用 `POCKET_EMAIL_MASTER_KEY` 环境变量注入的，来源是
  `C:\workspace\openpocket\wt3\backend\data\email_master.key` —— **该文件已不存在**。
- 全盘 4 把 key 逐一实测（临时诊断读 `credential_encrypted` 逐个试解），
  **0/5 全部解不开**。

处置：用当前实例的 key 重新部署凭据，
`node scripts/deploy-email-accounts-to-device.mjs` → **12 PASS / 0 FAIL**。

**教训**：`POCKET_EMAIL_MASTER_KEY` 必须固定注入并落进运维文档。
key 一丢，5 个真实邮箱的凭据全部作废、必须重新录入 —— 这次是运气好，
用户原始需求里带着密码。

---

## 4. 已知缺口（如实记录，本轮未修）

1. **服务端还剩 23 封脏摘要** —— **2026-10-02 04:5x 已查清：不是代码缺陷。**
   见 [`2026-10-02-remaining-dirty-snippets-are-not-a-code-bug.md`](./2026-10-02-remaining-dirty-snippets-are-not-a-code-bug.md)。
   当前 `DeriveSnippet` 对真实 rawMIME 形态处理是正确的（实测剥掉首行 boundary
   后 `ParseMIMEMessage` 成功、QP 折行正确还原、摘要干净可读）。这批是修复前
   写入的历史脏数据，且 `body_path` 全空——**原文从未落盘，无法就地重算**。
   自愈路径已就位（`store.go:515` 的 `ON CONFLICT` 刷新 snippet），重置
   `last_synced_uid` 重新同步即可刷干净；因会真的登录真实邮箱，未擅自执行。

   > 2026-10-02 02:40 更正：原文写「全部集中在 account `-2`」是**错的**。
   > 实测这 23 封横跨 account `-1`/`-2`/`-3`/`-5`（`每日信用管家`、
   > `企业微信邮箱登录提醒`、OpenAI 验证码等）。已在客户端收件箱顶部肉眼
   > 复现（`logs/emu-88-classify-stalled.png` 前两条即脏摘要），
   > 属服务端历史脏数据，不是客户端镜像的新回归。


2. ~~**客户端列表刷新后仍显示旧摘要**~~ —— **已于 `394ec6f` 修复**。
   > 原文的归因（「KeepAlive 不重渲 / `onMounted(load)` 只跑首挂载」）是**错的**：
   > `load()` 每次都跑，`emails.value` 也确实被重新赋值了。真正丢字段的是
   > **合并函数**：`email-inbox-pagination.ts` 的 `applyRefreshPage` 只收
   > 「全新 id」，同 id 的行整行丢弃。于是 `emails-store.ts` 的
   > `ON CONFLICT ... SET snippet=excluded.snippet` 把摘要写进了本地库，
   > 却在合并这一步被扔掉——**库是对的，列表是旧的**。
   > 现改为「刷新页（去重）+ 刷新页未覆盖的已翻页行」，并把「无变化」
   > 与「有更新」分开计数（`addedCount` / `updatedCount`）。
   >
   > **真机证据**：把服务端 `em-11` 的摘要改成 `[[REFRESH-PROBE]] …` 并
   > 抬高 `created_at`（让增量同步带上它），重启 App 重新挂载后，
   > 列表该行从 base64 串变成了探针文案（`logs/emu-97-merge-proven.png`），
   > 顶部同时出现「已同步 4 个账户，新邮件 2」。随后已把该行
   > `snippet`/`created_at` 按原值还原（503 字节，与实验前一致）。
   > 负控（退回旧实现）6 个用例转红，见提交正文。
   >
   > 附带发现：**`adb input swipe` 触发不了这个 PullToRefresh**——
   > 滑动中列表纹丝不动、`syncHint` 始终为空。真机验证改用
   > 「force-stop + 重新挂载」走 `load()` 内部的 `showLocal(false)`，
   > 那是同一条合并路径。

3. ~~**「正在归类 1/120」卡住不动**~~ —— **已于 `48ae6aa` 修复**。
   原缺口是 `use-email-inbox.ts` 的 `runClassify` 用
   `do{...}while(!classifyCancel.value)` 且终止条件只认 `remain <= 0`；
   未配 LLM provider 时服务端恒返回 `classified=0 / remaining=120`，
   循环永不退出，模拟器实测约 35 req/s、日志 8 秒涨约 630 KB。
   现改为 `classifyRunVerdict` 判定（连续 2 轮零进展 → `stalled`），
   并如实提示「归类未生效（AI 分类服务未配置），仍有 N 封未归类」。
   修后 60 秒日志仅涨 4590 字节。
   仍需 kxmemory 地址才能验证**成功**路径（配了 provider 时能真的归完类）。

4. **设备上同时存在两个包**（2026-10-02 02:3x 踩过，差点误判修复无效）：
   本仓库新构建的 APK 装成 `com.kaixuan.opencode.pocket`；
   而模拟器上还留着一个更早的 `com.kaixuan.opencode.pocket.sttdev`。
   `adb install -r` 会报 Success 但**不会**更新你以为在用的那个包。
   核验办法：`adb shell pm path <pkg>` 看 base.apk 的字节数/mtime，
   再和本地产物 `app-debug.apk` 的 Length 对比；或
   `adb shell dumpsys window | findstr mCurrentFocus` 确认当前前台包的包名。
   本轮「装上了但 classify 仍在刷」的结论就是被这个旧包误导的，
   实际旧包的 35 req/s 刷屏才是刷屏源（force-stop 后 10 秒零增长）。

5. ~~模拟器登录页 Custom backend URL 丢失、回落 Build default~~ —— **已修，
   且原归因是错的**，见
   [`2026-10-02-device-localhost-api-base-unreachable.md`](./2026-10-02-device-localhost-api-base-unreachable.md)。
   地址一直存得好好的（`http://localhost` 分区里有
   `pocket_api_base = http://10.0.2.2:18099`）；装机包 `androidScheme` 是
   `https`，页面 origin 变成 `https://localhost`，而 localStorage 按 origin
   分区，于是读不到那份 override。真正的问题是：**构建默认值里的
   `localhost:18099` 在设备上指向手机自己**，此前全靠 `adb reverse` 才通。