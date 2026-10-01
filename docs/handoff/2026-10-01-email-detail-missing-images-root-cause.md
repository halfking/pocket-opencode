# 邮件详情「缺失图片或内容」的根因：旧后端把正文压平成纯文本，内联图在管线里就丢了

> 用户原话：「邮件的详情展示不自常，缺失图片或内容」。2026-10-01 真机定位。
> **结论：代码已修好，设备跑的是旧二进制。** 本文给出可复现的对照证据。

## 1. 现象（真机）

对 `em-1-acct-1790811900843306300-1`（桩夹具 UID 1「季度视觉规范 v3」，
multipart/related + cid 内嵌 PNG）打开 `#/email/:id` 详情页：

```
detailFound: true
text.len: 109          正文中文正常，无 MIME 原文
images.total: 0        ← 一张图都没有
images.dataUri: 0
images.unresolvedCid: 0
html.len: 635
```

正文在、图没有。`unresolvedCid: 0` 说明不是「cid 没解析出来」，而是**根本没拿到含 cid 的数据**。

## 2. 关键对照：同一封邮件、同一个库、两个后端

从当前 main 构建的 pocketd 跑在 8097；设备连的是 8088 上的旧二进制。两者共用
schema `opencode_pocket`、同一封邮件、同一个 `source=imap`：

| 后端 | body 字节 | 是原始 MIME | 含 `src="cid:` |
|---|---|---|---|
| 8088（旧二进制） | **150** | ❌ | ❌ |
| 8097（当前 main） | **27,156** | ✅ | ✅ |

设备上 `pocket:email_body:<id>` 本地缓存也是 66/150 字节的压平文本，
`remoteEqualsCache: true` —— 说明**服务端给的就已经是没有图的了**，前端无从恢复。

## 3. 为什么会这样

后端详情接口是**刻意**透传原始 MIME、由前端解析的（`emailBodyResponse` +
`TestEmailBodyResponse_ReturnsRawMIMEForRealMessage` 钉住这个契约）。前端解析器
`extractEmailBody` + `resolveCidImages` 能把 cid 内联成 data URI。

但**服务端正文缓存** `dataDir/email-bodies/<emailID>.bin`（`writeCachedEmailBody`）
在旧二进制里写进去的是**已经压平的纯文本**：图片部件在管线阶段就没被保留。
于是 `emailBodyResponse` 拿到的是一段合法 UTF-8 的普通文本，走
`ParseMIMEMessage` 不报错、utf8 校验也过，于是原样返回 —— 契约上「合法」，
内容上图片已经没了。

当前 main 的管线存的是原始 MIME，所以 8097 能返回 27KB 带 cid 的报文。

## 4. 前端侧已单独验证正确

`frontend/src/features/email/__tests__/email-detail-fixtures.test.mjs`
（本次新增，3/3 通过）把 `scripts/imap-fixture-mails.mjs` 的 `buildMails()`
**真实字节**喂进 `extractEmailBody`：

1. 7 封全部解析成功，输出里**没有** MIME 原文（`--OUTER/--REL1/--_Part`、
   `Content-Type: multipart`、`MIME-Version` 均不出现），无 `<script>`
2. 两封带 cid 的（UID1 季度视觉规范、UID3 9 月度对账单）**内联成 `data:image/png;base64,`**，
   且没有残留 `src="cid:"` 引用
3. 增值税发票那封能取到「发票号码/开票日期/销售方」正文

即：给前端足够的数据，它是能正确渲染的。**缺的是数据，不是渲染。**

## 5. 结论与后续

- 代码侧无需再改。修复已提交在 main（工作树 `internal/email` 与 `internal/server`
  均干净，非未提交在制品）。
- 设备上仍会复现，直到 8088 上的旧二进制被替换。这一步需要授权，我没有动它。
- 存量已压平的缓存不会自愈：`emails.body_path` 指向的旧文件里已经没有图片部件，
  重新同步才能重写（与 snippet 的 a8837c1 同理，但 body 缓存的失效策略不同，
  值得后续确认是否也需要纳入刷新范围）。

## 6. 探针教训（本轮第 4~5 次，同一个模式）

写设备探针前**必须先读模板和接口**，本轮又踩了两次：

1. 邮件行选择器靠猜（`.mail-item` / `.email-item` / `li`）全部落空，报「邮件列表为空」。
   真实是 `EmailInboxView.vue:112` 的 `.email-card`、详情是 `EmailDetailView.vue:22`
   的 `.detail`。
2. PowerShell 里按中文主题筛选 → 接口返回的中文被按 Latin-1 解码成乱码，匹配永远失败。
   改用纯 ASCII 的 `accountId` 定位才成功。

再加上此前的相对 URL、无凭据 fetch、`accountId` vs `account_id`，同一个会话里
因为「没先读实现就写探针」产出的假结论至少有 5 次。**这是我的方法问题，不是运气。**
