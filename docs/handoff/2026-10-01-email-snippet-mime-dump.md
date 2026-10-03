# 邮件摘要透出原始 MIME（2026-10-01 真机审计）

> **状态：已修复并全部并入 main**，共三个提交：
>
> | 提交 | 内容 |
> |---|---|
> | `583f02e` | 纯函数 `DeriveSnippet`（`snippet.go` + 8 个用例） |
> | `d0014bd` | 接进 fetcher 的三个摘要产生点 + 调用点护栏（`fix/email-snippet-callsites` 分支，已快进并入 main） |
> | `fc3ed38` | 通知标题/正文断词（`fix(ui)`），修掉「超长串被祖先 overflow-x:hidden 静默裁掉」 |
> | `a8837c1` | 让重跑同步能**刷新已入库的坏摘要**——见下方「存量数据」一节 |
>
> 下方「精确补丁」一节保留，作为补丁内容的说明。
>
> 原文写的「调用点还没改」这段留档：纯函数先由 `583f02e` 提交，当时
> `internal/email/fetcher.go` 正被并行会话修改（`git status` 显示 `MM`，行号在变动），
> 按协作纪律没有抢写。
>
> 插曲：随后 `94b55ff` 一次 merge 把 fetcher.go 的三处调用点**和**调用点护栏测试
> `TestFetcherUsesDeriveSnippetAtEverySnippetSite` 一起连带删除，`DeriveSnippet` 退化成
> 死代码——8 个纯函数用例照样全绿，缺陷静默回归。`d0014bd` 同时补回接线与护栏。

## 0. 存量数据：代码修好 ≠ 数据变好（`a8837c1`）

`d0014bd` 只拦**新写入**的坏摘要。已入库的老数据一点没动——而真机上那批数据
正是问题本身（100 个通知正文里 46 个溢出、12,311px 被静默裁掉）。

根因：`InsertEmail` 原来是 `ON CONFLICT (id) DO NOTHING`，snippet 由���是**只写一次**
的不可自愈字段，没有任何路径能修好存量。`a8837c1` 改成只刷新 snippet：

```sql
ON CONFLICT (id) DO UPDATE SET
  snippet = CASE WHEN EXCLUDED.snippet <> '' THEN EXCLUDED.snippet ELSE emails.snippet END
```

窄口径是刻意的：其余列一律不更新（否则每轮同步把「已读」标回未读）；`EXCLUDED.snippet`
为空时保留旧值（DeriveSnippet 遇到「疑似 MIME 又剥不干净」会返回空串，直接赋值
一次同步就能把正常摘要刷成空白）。

**所以：存量坏摘要在下一次真实邮件同步后自动愈合**，不需要写迁移。但这一步需要
一个能连的邮箱账户（163/QQ 授权码、`Unsafe Login` 风控）—— 桩验证证明不了。


## 1. 真机证据

43 页 `ui-sweep` 只有 `/notifications` 报溢出（8 处）。顺着查下去发现不是布局问题，
是**数据问题**——设备上实际读到的通知正文（`source=email`、`kind=email.important`）：

**（1）字面 HTML，5/50 条**

```
您的额度即将用尽，当前剩余额度为 ¥0.002116，为了不影响您的使用，请及时充值。<br/>充值链接：<a href='https://u.syapi.cn/console/topup'>https://u.syapi.cn/console/topup</a>
```

用户看到的是字面的 `<br/>` 与 `<a href=…>`。

**（2）整段原始 MIME**

```
------=_Part_397111_1624436759.1790214518883
Content-Type: multipart/alternative; boundary="----=_Part_397110_1060649035.1790214518883"
```

用户看到的是 MIME boundary 与 `Content-*` 头。

第（2）类同时解释了那个溢出：MIME boundary 这种**不可断长串**把 `.ntf-item` 撑到
`scrollWidth 491 / clientWidth 302`，而祖先是 `overflow-x: hidden` —— 内容被静默裁掉，
用户既看不到也滚不到。

真机样式链实测（`white-space / word-break / overflow-wrap` 全是 `normal`，
`min-width: 0` 只加在 `.ntf-main`，对内容长度无约束）：

```
BUTTON.ntf-item  scrollW=491 clientW=302  overflowX=visible
LI.unread        scrollW=494 clientW=306
UL.ntf-list      scrollW=2354 clientW=330
…                → 祖先 overflow-x: hidden，内容被裁
```

## 2. 根因

`internal/email/fetcher.go` 的 IMAP 路径：

```go
var snippet string
for _, bs := range m.BodySection {
    snippet = strings.TrimSpace(string(bs.Bytes))
    break
}
if len(snippet) > 500 {
    snippet = snippet[:500]
}
```

三个问题叠在一起：

1. **不解析 MIME** —— `m.BodySection[0].Bytes` 是抓回来的原始字节，
   `BODY[TEXT]<partial>` 时就是 MIME 头。
2. **不剥 HTML** —— 只有 HTML 正文时，标签原样进摘要。
3. **按字节截断** —— `snippet[:500]` 会在多字节字符中间劈开，
   中文邮件产生 `U+FFFD` 乱码。

通知侧 `server_email_pipeline.go` 的 `Body: e.Snippet` 把这个值原样透出到界面。

同样的毛病在 `mime.go` 的 `ExtractDisplayBody`：HTML 分支直接返回未剥标签的
`HTMLBody`，解析失败则 `return string(raw)`。

## 3. 补丁

### 3.1 `internal/email/fetcher.go`（IMAP 路径，约 8 行）

```diff
 		var snippet string
 		for _, bs := range m.BodySection {
-			snippet = strings.TrimSpace(string(bs.Bytes))
+			// 2026-10-01 真机审计：原来直接把原始字节当摘要，users 会看到
+			// 整段 MIME（--part_xxx / Content-Type: …）与字面 HTML 标签。
+			// 根因在这里，不是渲染层。
+			snippet = DeriveSnippet(bs.Bytes, 500)
 			break
 		}
-		if len(snippet) > 500 {
-			snippet = snippet[:500]
-		}
```

注意：`DeriveSnippet` 返回 `""` 时，下面原有的 `if snippet == ""` 分支
（`fetchSnippetOnConnected` 单封补拉）会照常生效，**逻辑不用改**。

### 3.2 `internal/email/fetcher.go`（POP3 路径，约 2 行）

```diff
-			em.Snippet = truncateStr(strings.TrimSpace(parsed.TextBody), 500)
+			em.Snippet = truncateStr(strings.TrimSpace(parsed.TextBody), 500)
 			if em.Snippet == "" {
-				em.Snippet = truncateStr(strings.TrimSpace(parsed.HTMLBody), 500)
+				// 原来直接塞 HTMLBody，字面 <br/> / <a href=…> 会透到界面。
+				// 复用 DeriveSnippet 的 HTML 剥标签路径。
+				em.Snippet = truncateStr(DeriveSnippet([]byte(parsed.HTMLBody), 500), 500)
 			}
```

### 3.3 `internal/email/mime.go`（`ExtractDisplayBody`，可选但建议）

```diff
 		if h := strings.TrimSpace(msg.HTMLBody); h != "" {
-			return h
+			return htmlToText(h)   // 剥标签，别把 <br/> 甩给调用方
 		}
 	}
 	const maxDisplay = 256 * 1024
 	if len(raw) > maxDisplay {
 		raw = raw[:maxDisplay]
 	}
-	return string(raw)
+	// 原来是「解析失败就退回原文」，而「原文」正是用户看到的 MIME 转储。
+	// 这里保留原文兜底（邮件详情页确实需要看到原始结构），
+	// 但要保证上层不会再把它当正文显示。
+	return normalizeWhitespace(string(raw))
```

`ExtractDisplayBody` 的兜底**建议保留原文**（`/api/emails/{id}/body` 的契约就是
整封 MIME，前端 `extractEmailBody()` 依赖它），只改 HTML 分支即可。

## 4. 应用后的验证

```bash
cd backend
go test ./internal/email/ -run TestDeriveSnippet -count=1   # 8/8
go test ./internal/email/... -count=1
```

真机验证（注意：**必须先把 `adb reverse tcp:8088` 指向含此修复的实例**，
否则验的是旧二进制——这个坑本轮踩过两次）：

1. 重新同步邮件，让 `snippet` 以新逻辑落库
2. `GET /api/notifications` —— `body` 里不应再出现 `<br` / `<a ` / `--part_` / `Content-Type:`
3. `/notifications` 页复跑 `node scripts/ui-sweep.mjs`，该页应从「8 处需关注」变 0

## 5. 注意

已落库的历史数据里仍有 MIME 摘要，`DeriveSnippet` 只在**新抓取**时生效。
要清理存量需重跑一次同步（`snippet` 会被覆盖写），或写一次性迁移。
本轮未做迁移。
