# 发票采集两处缺陷 + 真机部署的真实状态（2026-10-02 上午）

分支 `feat/mail-config-deploy`。本文件汇总今天上午从「磁盘上 6 个异常发票文件」出发，
一路查到真机的结论。**每条都标注了是实测还是推断，以及证据局限。**

## 0. 一句话结论

APK 装到真机这件事**安装成立、身份可证，但此前「可用」并不成立**——真机上的 App
指向 `http://127.0.0.1:8088`（真机到不了的回环地址）。现已改为
`http://192.168.31.20:18099` 并用服务器真实应答（401/200）证明链路通。
**最后一步（看到真机上的邮件列表）缺 admin 口令，未完成。**

---

## 1. 已修并提交的两处发票采集缺陷

### 1.1 退化 PDF 仅凭 magic 就被当成下载成功（`220da1b1`）

`isPDFBytes` 只验 `%PDF`，只有 Catalog、无页树的 69 字节空壳能过；
`saveInvoiceFile` 把它写盘并置 `status=downloaded`。后果两条：

1. 台账多出一张**金额为 0** 的假发票；
2. 这正是导出侧 pdfcpu 遍历页树会 panic 的输入（见 `pdfPageCountSafe` 与
   `TestExportInvoiceGrid_SkipsMalformedPDFAndKeepsGoodOnes`）——采集器在制造
   导出器不得不防御性跳过的文件。

金额为 0 的记录能活到落盘，靠的是 `invoice.go:332` 的 `hasInvoiceAttachment` 逃生口。

改法：落盘前用 pdfcpu 权威解析确认「至少有 1 页」，否则 `markRetry`。
判据**不用字节扫 `/Type /Page`**：压缩对象流里扫不到，会误杀好件。

### 1.2 销售方把「列头标签」当成了对方单位（`220da1b1` + `08a85957`）

`reSeller` 的值规则是「标签后取 1–6 个 ≥2 字的词」，挡不住**紧接着的另一个标签**。
实测复现：

```
body = "销售方\n发票抬头\n价税合计：1280.00"
  旧：reSeller -> "发票抬头"   fileName -> "其他-发票抬头-….pdf"
```

`reSeller` 上方注释早写着「值要取第一个非标签词」——那是**意图**，实现里从没有过。
`08a85957` 补上跨行兜底：表头行形态下真正的单位名在下一行（`reSeller` 的分隔符
`[^\S\r\n]+` 刻意不含换行，否则会吞掉整段散文）。四道闸：只在紧邻取值失败时启用 /
列头与「标签：值」黏行跳过且不计次数 / 候选要过 `cleanSellerValue` 与实体名判定 /
最多试 3 行。

判据选型踩了两个坑：按「≥2 个汉字」形同虚设（「如需」「您好」直接成为对方单位）；
提到「≥4 个汉字」仍不够（「详见附件」正好 4 个）。最终按**企业后缀**判定。
代价：短品牌名会退到发件人兜底（只作用于跨行兜底，不碰主路径）。

### 1.3 顺带升级两个自相矛盾的夹具

`TestHarvestOne_POP3UsesRawCacheInsteadOfIMAPFetch` 的 XMLRenderer 返回
`"%PDF-1.4 fake rendered pdf"`、邮件附件是 `"%PDF-1.4 fake pdf body"`，都只有 magic
没有页树，被 1.1 的新门禁拒收。该用例主题是「POP3 走原文缓存而非 IMAP FETCH」，
渲染字节不该决定成败，已换成 `e2eInvoicePDF`。

---

## 2. 真实邮件探到的两处**至今未修**的缺陷

生产调用形态（三样都要传，缺一样会造出假象）：
`ExtractInvoiceLoose(Email{Snippet: parsed.TextBody}, parsed.TextBody, HasInvoiceAttachment(att))`。
**我第一版探针三样都没传**，于是「通行费电子发票」这种带 3 个附件的真发票被判
`hit=false`——那是探针的错，不是缺陷。对齐后实测：

### A. 附件型发票 amount=0

```
主题「通行费电子发票」  atts=3  hasInvoiceAtt=true  hit=true
  seller="通行费发票通知"  amount=0  date="2026-09-14"
  file="其他-通行费发票通知-0.00-2026-09-14.pdf"
  att 通行费电子票据汇总单(票据).pdf  45464 B  isPDF=true  hasPages=true
```

PDF **有效**，但正文无金额，**没有任何代码从 PDF 文本抽金额**（采集器只用
`ParseInvoiceDateFromBytes` 补日期）。所以「记录必要信息并汇总金额」对这类发票
**恒为 0**。

⚠️ **`220da1b1` / `08a85957` 都不解决它。** 磁盘上那个 `其他-财务部-0.00-….pdf`
很可能就是这类邮件，不只是空壳 PDF 造成的。

可修性已探明：`/ToUnicode` 存在、`hasImage=false` ⇒ 文本层机器可读，原理上可抽。
但 **pdfcpu v0.11.0 没有文本抽取 API**（`go doc` 无 `ExtractText`，只有
`ExtractContent` 导出内容流）。要修需引第三方 CJK 抽取器，或自研
内容流 + ToUnicode 解码。

### B. ZIP 发票附件不被采集

同一封邮件里 `通行费电子发票.zip`（136 KB）才是逐张的通行费发票。
`DetectInvoiceMedia` 对 zip 返回 unknown，harvestOne 的附件循环只认
`isPDFBytes || isImageBytes`，zip 整个被跳过。
成本比 A 低得多：`archive/zip` 是标准库，解开后里面是标准 PDF，现有分支能接。

### 附带：正文无日期时文件名用**下载日**

`InvoiceFileName` 的 `time.Now()` 兜底会静默把「开票日期」换成下载日期。

---

## 3. 真实邮箱探针的其余结论

### 3.1 master key：旧记录已过期（重要更正）

旧记录说真实 key 只在 `wt3\backend\data\email_master.key`、活跃 data 目录解不开。
**已过期**：`wt3` 目录不存在，而 **`C:\workspace\openpocket\data\email_master.key`
才对**——5 个账户凭证全部解开成功，QQ 与企业邮还真实 POP3 登录成功。

含义：只读探测真实邮箱**不需要** `POCKET_EMAIL_MASTER_KEY`（那个 env 只决定重启后
进程读哪把 key）。仍不能做的：改生产配置、回写、据此重启 pocketd。

### 3.2 两个 163 账户 POP3 失败 = 未开 POP3 授权，不是口令错

POP3 返回 `-ERR Unable to log on`；同一凭证走 **IMAP 三个 163 账户全部 LOGIN 成功**。
163 的 IMAP 默认开，POP3/SMTP 客户端授权要单独在设置里开。
**要做的：去 163 网页设置给这两个账户开 POP3/SMTP 授权，不用重置口令。**

### 3.3 IMAP 挂死的**确切范围**（`last_synced_at` 实测）

```
feikemanager@163.com   uid=1669791329  lastAt=11:25   正常
kimmy.huang@163.com    uid=1298896147  lastAt=11:28   正常
56551681@qq.com        uid=10455       lastAt=01:37   停 10 小时
huangxutao@kxpms.cn     uid=11          lastAt=01:37   停 10 小时
feikemanager1@163.com  uid=0           lastAt=never   收件箱空
```

**挂死的只有 `imap.qq.com` + `imap.exmail.qq.com`。**
`feikemanager1` 的 `never` 也有解释了：它收件箱 `MESSAGES 0`，而空同步不推进
`last_synced_at` 正是 `c459400a` 修的——**部署后这条状态会自己变正常**。

### 3.4 两个「看起来像缺陷」其实不是

1. **163 的 `NO SELECT/EXAMINE Unsafe Login`**：是我的手写探针**漏发 RFC 2971 ID**。
   `fetcher.go:492-497` 早就写明网易 Coremail 在 SELECT 前要求 ID，`sendClientID`
   在四处调用点都有。这正是需求原文提到的「163 邮箱访问要有一个头信息」，已实现。
2. **正文缓存 41 个 `.bin` 不是发票样本来源**：能全解开，但内容从
   `MIME-Version: 1.0` 开始、**没有 RFC822 头**（服务层只存 body part），
   From/Subject 全空，且多份长度相同（27156 B）——是 newsletter。

### 3.5 当前真实邮件里 `reSeller` 一次都没命中

5 封里 4 封发票邮件销售方都来自「您收到来自XX的发票」或发件人兜底。
所以 1.2 的标签词闸与跨行兜底**当前无真实样本覆盖**——它们修的是 09-30 那批已随
schema 重建丢失的邮件（`财务部` / `云服务开票中心`）。

---

## 4. 真机部署的真实状态

### 4.1 App 之前连不上后端（已修）

真机 localStorage `pocket_api_base = http://127.0.0.1:8088`，真机到不了。
**所以「已装到真机、App 正常启动、0 条混合内容报错」只证明安装与包身份，
不证明可用。**

根因是设计使然，不是 bug：`api-base-loopback-device.test.ts` 的注释写明 loopback
只在**构建默认值**时被拒，用户**显式填的 localhost 予以尊重**（`adb reverse`
开发流需要它）。于是错值会一直留在设备上，代码守卫管不到。

已改为 `http://192.168.31.20:18099`，用**服务器真实应答**证明，不用「没报错」当证据：

```
POST /api/auth/login（假密码）  -> 401   ← 打到了真后端
GET  /api/email/accounts        -> 401
fetch /healthz（页面上下文）     -> 200
```

注意：App「后端服务器」页的**构建默认那一项写的是 `:8088`**，与实际端口 18099 不符，
选它会连不上。

### 4.2 CDP 在真机上可用（此前「打不通」已过期）

```
adb -s <serial> shell pidof com.kaixuan.opencode.pocket     # 注意 $pid 是 PS 只读变量
adb -s <serial> forward tcp:9333 localabstract:webview_devtools_remote_<pid>
powershell -ExecutionPolicy Bypass -File scripts\webview-eval.ps1 -Expression <expr> -Port 9333
```

`/proc/net/unix` 里有**两个** devtools socket，必须按 app 的 pid 选。
表达式必须**无空格**且用**单引号**（`powershell -File` 会重新切分参数并吃掉引号）。

### 4.3 本地镜像库确实存在（正面证据）

`/data/data/com.kaixuan.opencode.pocket/databases/lobsterSQLite.db`，585,728 B，
写入至 2026-10-01 12:06。**文件级加密**（无 SQLite 魔数），
`pocket_crypto_cfg` = `{"fieldEncryption":"disabled","hasMasterPassword":true,...}`、
salt 24 位。**未尝试解密**——那是用户自己的主密码，不该绕。
另有 6 个 email 相关 localStorage 条目，是早前同步的残留。

### 4.4 两个遗留陷阱（未处理，等用户确认）

- **双包**：`com.kaixuan.opencode.pocket`（目标）与 `...pocket.sttdev`（10-01 旧包）
  并存；后者会**自己抢前台**（实测一次 `input tap` 后 `mCurrentFocus` 跳到它）。
- **遗留 adb reverse**：`host-29 tcp:18099 tcp:18111`（非本次建立）。它把设备的
  `localhost:18099` 接到宿主 **18111**，既不指向 pocketd 又会**造假绿**——与
  `76c83693` 从装机脚本撤掉 `adb reverse` 的结论一致。

---

## 5. 证据局限（如实记录）

出问题那两封源邮件与 `email_invoices` 行已随 2026-10-01 的 schema 重建丢失
（现只剩 2 行，磁盘 6 个文件中 4 个已成无记录孤儿），唯一的 `invoice-harvest`
日志（93MB）已被轮转掉。所以 1.1 / 1.2 的回归数据是**合成的**：形态取自实测复现时
`reSeller` 的真实输出，断言钉在性质上（「销售方绝不能是列头」「退化件绝不能置
downloaded」）而非某段具体文件名。真实样本回来应重跑。

---

## 6. 仍需用户决定

1. **admin 口令** —— 缺它无法在真机看到邮件列表（最后一步）。
2. **修 A / B** —— A 需新依赖或自研 CJK 解码；B 建议先做（标准库即可）。
3. **重启 pocketd** —— 今日 20 个提交未进运行进程，需 `POCKET_EMAIL_MASTER_KEY`。
4. **IMAP 挂死的修法** —— 降 `imapHardTimeout` 已证伪（有效截止与它无关，实测是
   `4/3·idle = 80s`）；剩四个选项：`hard <= idle/3`、调大 `syncBudget`、
   给 POP3 保底预算、越界时 `Close()`。
5. **163 授权** —— 给 `feikemanager*@163.com` 开 POP3/SMTP 客户端授权。
6. 其余：LLM 配额 / 飞书凭证 / 5 个账户配 rules / 明文 http 长期方案 /
   OAuth 半成品 / 日志轮转 / 垃圾 MOVE / 台账保留 / 4 个孤儿文件 /
   删除 sttdev 旧包 / 清理遗留 reverse 隧道。
