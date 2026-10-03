# 缺陷 15：发票链接下载到非 PDF 内容时被静默丢弃，last_error 误报

日期：2026-10-02
分支：`feat/mail-config-deploy`
状态：**已修**

对应需求「有可能我们需要多次操作才能下载到发票文件」。

---

## 缺陷

`downloadPDF` 只判 `StatusCode != 200`，**不校验内容**。而调用方的判据是：

```go
data, dlErr := h.downloadPDF(ctx, u)
if dlErr == nil && (isPDFBytes(data) || isImageBytes(data)) {
    return h.saveInvoiceFile(ctx, inv, data, "pdf-url")
}
if dlErr != nil {
    log.Printf(...)          // ← 只有 err != nil 才记
}
```

发票链接常带登录态/时效限制，对未授权请求返回 **HTTP 200 + 一个 HTML 登录页**。
这时 `dlErr == nil` 而内容不是 PDF → **既不记错误也不记录**，静默落到下一个分支。

## 后果

最后一封 XML 也没有时，`last_error` 记成：

```
no usable pdf/xml found in message
```

把「**链接存在但拿回来不是发票文件**」误报成「**邮件里没有发票文件**」。
真正的原因（登录态过期 / 链接失效 / 返回错误页）被吞掉——人和后续排查都无从判断
该不该重试、该不该人工介入。

这正好砸在「多次操作才能下载到」这个需求点上：**最需要看清的一步恰好最不可见。**

---

## 修法

1. `downloadPDF` 把内容校验前移成显式错误，错误信息带 Content-Type：
   ```
   not-pdf: 下载内容不是 PDF/图片（content-type=text/html; charset=utf-8, 50 字节）
   ```
   判据用**魔数**（`isPDFBytes` / `isImageBytes`）而不是 Content-Type——有些
   服务器 Content-Type 不准但内容确实是发票文件。
2. 调用方收集 `linkErrs`，把每个链接的失败原因写进 `last_error`：
   ```
   发票链接未能取到 PDF 文件：<url> -> not-pdf: ...
   ```

**不改变重试语义**：仍然 `pending` 等下一轮（`MaxInvoiceAttempts = 8`），
超限转 `failed`。只是让 `last_error` 从"猜"变成"看得见的事实"。

---

## 验证

新增 `invoice_link_diagnosis_test.go`，用 `httptest` 起本地服务器，不碰 IMAP/PG：

| 用例 | 作用 |
|---|---|
| `TestDownloadPDF_HTMLResponseIsNotSilentlyAccepted` | 200 + HTML 登录页必须报错 |
| `TestDownloadPDF_Non200StillReports` | 对照：403 仍报错（回归） |
| `TestDownloadPDF_RealPDFStillPasses` | 对照：真 PDF 不能被新判据误伤 |

**负控**：把内容校验整段摘掉（退回修复前行为）→ 精确转红，报错信息直接点出
「调用方会静默丢弃它并误报成『邮件里没有发票文件』」。恢复后 3/3 绿。

> 负控第一版因为留下未使用的 `data` 变量而 build failed——**负控必须能编译**。
> 另外这次 `git checkout` 恢复时把我自己的修复也一起撤销了（HEAD 里本来没有），
> 只好重做一遍。教训：手搓备份要先于注入，`git checkout` 只对已提交内容可靠。

回归：`go build ./...` 0；`go vet ./...` 0；`go test ./internal/email/` 26.6s ok；
`go test ./internal/server/` 14.2s ok。

---

## 仍未验证的（不夸大）

「多次重试后成功」这条**真实数据路径依然没跑过**：真实库里那张发票
`attempts=1` 首次即成功。本轮修的是**失败时能不能看清原因**，不是让它更容易成功。
要真正验证需要一张「链接会失效/需登录」的发票——仓库里没有这种样本。
