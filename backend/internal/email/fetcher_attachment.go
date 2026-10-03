package email

// fetcher_attachment.go — `has_attachments` 的判定口径。
//
// ## 为什么需要它
//
// `emails.has_attachments` 全仓库原本**只有一处**被赋值，在 POP3 降级路径里
// （`em.HasAttachments = len(parsed.Attachments) > 0`，基于 `ParseMIMEMessage`
// 对完整 RFC 5322 原文的解析）。IMAP 同步路径**从未**赋过值 —— 不是漏写一行，
// 而是那条路径上根本没有数据来源：
//
//   - IMAP 的 FETCH 只请求 Envelope / UID / InternalDate，envelope 不含附件信息；
//   - go-imap v2 的 `imap.Envelope`（fetch.go:85-95）**也没有 Body 字段**
//     （v1 才有），所以「反正 envelope 里就有」是错的。
//
// 实测（2026-10-03 08:25:22，schema opencode_pocket）：真实库 120 封里
// pop3_sourced=0、imap_sourced=120、has_attachments_true=0 —— 唯一能置位的那条
// 路径在本部署产出为 0，于是前端 `EmailCard.vue` 的 📎 标记**永不显示**（需求 7）。
//
// 修法是给 `fetchOpts` 加 `BodyStructure`，判定逻辑就是本文件。
//
// ## 这个文件**证明了什么、没证明什么**
//
// 端到端用例（fetcher_attachments_test.go）跑在 go-imap 自己的
// `imapmemserver` 上，证明的是：客户端 ↔ 服务端这一对能正确协商并解析
// BODYSTRUCTURE，且判定口径对「纯文本 / 带附件 / 内联图 / 嵌套 multipart」
// 都成立。
//
// 它**没有**证明真实第三方 IMAP server 不会因此出问题 —— fetcher.go 里那条注释
// 记录过「加过数据项导致部分 server 响应缺 SP 分隔符、imapwire 解析失败」的历史，
// 而 imapmemserver 是 go-imap 自己的实现，**不可能**复现那种畸形响应。
// 那一半仍需 Greenmail（卡在 Docker daemon 未运行），未验。

import (
	"strings"

	"github.com/emersion/go-imap/v2"
)

// bodyStructureHasAttachment 判断一封邮件的 BODYSTRUCTURE 里是否有**真附件**。
//
// 口径与 POP3 路径的 `len(parsed.Attachments) > 0` 保持一致：
//
//   - `Content-Disposition: attachment` → 是附件；
//   - 带 filename（Disposition 的 filename，或 Content-Type 的 name）→ 是附件。
//
// **内联图不算附件。** multipart/related 里的 cid 图片（签名档 logo、正文插图）
// 在邮件客户端里属于正文的一部分；若把它算成附件，每封带签名图的邮件都会亮 📎，
// 需求 7 的附件标记就失去意义。判据是 `Content-Disposition: inline` 且无 filename。
//
// bs 为 nil 时返回 false：那意味着 server 没回 BODYSTRUCTURE（例如 FETCH 被裁剪）。
// 此时**不能**当成「确定无附件」，但也不凭空置真——保守 false，如实表现为「无附件」。
func bodyStructureHasAttachment(bs imap.BodyStructure) bool {
	if bs == nil {
		return false
	}
	found := false
	bs.Walk(func(_ []int, part imap.BodyStructure) bool {
		if found {
			// 已判定为有附件，无需再走子树。
			return false
		}
		single, ok := part.(*imap.BodyStructureSinglePart)
		if !ok {
			// multipart 容器本身不算附件，但子树还要继续走。
			return true
		}
		disposition := ""
		filename := ""
		if disp := single.Disposition(); disp != nil {
			disposition = strings.ToLower(disp.Value)
			filename = disp.Params["filename"]
		}
		if filename == "" {
			// Content-Type 的 name= 是 RFC 2046 允许的附件命名方式。
			filename = single.Params["name"]
		}
		switch disposition {
		case "inline":
			// inline 且带 filename：Outlook 等会把「作为附件插入的签名图」这么发。
			//
			// 注意：**这一分支与下面的 attachment 分支都无法被 imapmemserver
			// 覆盖**（实测它不填 extended 部分，Disposition 恒为 nil），
			// 所以它们由同文件的单测直接构造 BodyStructureSinglePart 来钉，
			// 端到端用例只覆盖 default 分支。
			found = filename != ""
		case "attachment":
			found = true
		default:
			// 没有 disposition 但带 filename —— 实践中 Outlook / QQ 企业邮
			// 有这么发的，按附件算。
			found = filename != ""
		}
		return true
	})
	return found
}
