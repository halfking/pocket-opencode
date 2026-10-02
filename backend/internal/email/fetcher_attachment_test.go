package email

// fetcher_attachment_test.go —— `has_attachments` 的判定口径。
//
// ## 分工（这一条很重要，别合并两层）
//
// `bodyStructureHasAttachment` 有三个分支：
// attachment / inline / 无 disposition 但有 filename。端到端用例
// （TestSync* 那三条）**只覆盖第三个**——实测 go-imap 的 imapmemserver
// 根本不填 BODYSTRUCTURE 的 extended 部分，`Disposition()` 对任何 part 都返回
// nil（探针实测：内联图与附件两封都是 `disp=<nil> extended=false`，附件只靠
// `Content-Type` 的 `name=invoice.pdf` 被认出）。
//
// 所以如果只写端到端用例，前两个分支会**静悄悄地不被执行**，而删掉它们的
// 豁免逻辑测试依然全绿（本轮实测过一次这种「负控不转红」：变异对所选用例是
// no-op，见 handoff §7dq）。下面这批单测直接构造 `BodyStructureSinglePart`
// 逐个分支钉住，端到端用例负责证明「整条链路真的把这个函数用上了」。

import (
	"testing"

	"github.com/emersion/go-imap/v2"
)

// single 构造一个带 disposition 的单 part。
func single(mediaType, subtype, dispValue string, dispParams, ctParams map[string]string) imap.BodyStructure {
	bs := &imap.BodyStructureSinglePart{
		Type: mediaType, Subtype: subtype, Params: ctParams,
	}
	if dispValue != "" || len(dispParams) > 0 {
		if dispParams == nil {
			dispParams = map[string]string{}
		}
		bs.Extended = &imap.BodyStructureSinglePartExt{
			Disposition: &imap.BodyStructureDisposition{Value: dispValue, Params: dispParams},
		}
	}
	return bs
}

func TestBodyStructure_NilIsNotAttachment(t *testing.T) {
	if bodyStructureHasAttachment(nil) {
		t.Error("nil BODYSTRUCTURE 被判成有附件 —— server 没回时必须保守为 false")
	}
}

func TestBodyStructure_DispositionAttachment(t *testing.T) {
	bs := &imap.BodyStructureMultiPart{Children: []imap.BodyStructure{
		single("text", "plain", "", nil, map[string]string{"charset": "utf-8"}),
		single("application", "pdf", "attachment",
			map[string]string{"filename": "invoice.pdf"}, nil),
	}}
	if !bodyStructureHasAttachment(bs) {
		t.Error("Content-Disposition: attachment 未被认成附件")
	}
}

func TestBodyStructure_AttachmentWithoutFilename(t *testing.T) {
	// 有些发送方只给 disposition 不给 filename（很常见，尤其是程序生成的）。
	bs := single("application", "octet-stream", "attachment", nil, nil)
	if !bodyStructureHasAttachment(bs) {
		t.Error("只有 disposition=attachment、没 filename 时也应算附件")
	}
}

func TestBodyStructure_InlineWithoutFilenameIsNotAttachment(t *testing.T) {
	// 这是签名档 logo / 正文插图：客户端里属于正文，不该亮 📎。
	bs := &imap.BodyStructureMultiPart{Children: []imap.BodyStructure{
		single("text", "html", "", nil, map[string]string{"charset": "utf-8"}),
		single("image", "png", "inline", nil, nil),
	}}
	if bodyStructureHasAttachment(bs) {
		t.Error("内联图被当成了附件 —— 每封带签名图的邮件都会亮 📎")
	}
}

func TestBodyStructure_InlineWithFilenameIsAttachment(t *testing.T) {
	// Outlook 把「作为附件插入的签名图」发成 inline + filename，那确实是附件。
	bs := single("image", "png", "inline",
		map[string]string{"filename": "sig.png"}, nil)
	if !bodyStructureHasAttachment(bs) {
		t.Error("inline 但带 filename 时应算附件（Outlook 的签名图就是这种）")
	}
}

func TestBodyStructure_ContentTypeNameCountsAsAttachment(t *testing.T) {
	// 无 disposition、只有 Content-Type 的 name= —— 端到端那条走的就是这条。
	bs := single("application", "pdf", "", nil, map[string]string{"name": "invoice.pdf"})
	if !bodyStructureHasAttachment(bs) {
		t.Error("Content-Type 带 name= 时应算附件")
	}
}

func TestBodyStructure_PlainTextIsNotAttachment(t *testing.T) {
	bs := &imap.BodyStructureMultiPart{Children: []imap.BodyStructure{
		single("text", "plain", "", nil, map[string]string{"charset": "utf-8"}),
	}}
	if bodyStructureHasAttachment(bs) {
		t.Error("纯文本被当成了附件")
	}
}

func TestBodyStructure_DispositionCaseInsensitive(t *testing.T) {
	bs := single("application", "pdf", "ATTACHMENT",
		map[string]string{"filename": "a.pdf"}, nil)
	if !bodyStructureHasAttachment(bs) {
		t.Error("Content-Disposition 值大小写不敏感（RFC 2045 的 disposition 是 token，实测有发送方发大写）")
	}
}

func TestBodyStructure_NestedMultipartFindsDeepAttachment(t *testing.T) {
	// multipart/mixed -> multipart/alternative + attachment 两层嵌套。
	inner := &imap.BodyStructureMultiPart{Children: []imap.BodyStructure{
		single("text", "plain", "", nil, map[string]string{"charset": "utf-8"}),
		single("application", "pdf", "attachment",
			map[string]string{"filename": "deep.pdf"}, nil),
	}}
	outer := &imap.BodyStructureMultiPart{Children: []imap.BodyStructure{
		single("text", "html", "", nil, map[string]string{"charset": "utf-8"}),
		inner,
	}}
	if !bodyStructureHasAttachment(outer) {
		t.Error("嵌套 multipart 里的附件没被走到 —— Walk 必须下钻子树")
	}
}

func TestBodyStructure_ContainerAloneIsNotAttachment(t *testing.T) {
	// 只有两个正文 part 的 multipart/alternative：容器本身不算附件。
	bs := &imap.BodyStructureMultiPart{Children: []imap.BodyStructure{
		single("text", "plain", "", nil, map[string]string{"charset": "utf-8"}),
		single("text", "html", "", nil, map[string]string{"charset": "utf-8"}),
	}}
	if bodyStructureHasAttachment(bs) {
		t.Error("multipart/alternative 被当成了附件")
	}
}
