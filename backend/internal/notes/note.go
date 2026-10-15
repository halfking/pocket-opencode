// Package notes provides local SQLite caching of voice-note metadata for
// the personal-assistant module. AI processing (classification, SSOT, graph)
// happens in the kxmemory FastAPI service; pocketd only caches metadata for
// offline browsing and proxies note content to/from kxmemory.
//
// The full notes schema (workspaces, knowledge_blocks, smart_links, etc.)
// lives in kxmemory; this package intentionally stores a thin local mirror.
package notes

import "strings"

// Note is the local cached metadata of a voice note. The full body and AI
// fields are fetched from kxmemory on demand; only fields needed for list
// rendering offline are cached here.
type Note struct {
	ID          string `json:"id"`
	UserID      string `json:"userId"`
	WorkspaceID string `json:"workspaceId,omitempty"`
	Title       string `json:"title,omitempty"`
	Snippet     string `json:"snippet"` // 列表渲染用的前 ~200 字
	// Content 是**完整正文**（2026-10-06 新增）。
	//
	// 此前 Note 结构里**没有**这个字段，于是 DB 的 `content` 列虽然
	// 存在（TEXT，无长度限制），读出来却被直接丢弃：
	// GetByIDScoped 里 `if content.Valid { n.Snippet = content.String }`
	// 紧接着又被 `if snippet.Valid { n.Snippet = snippet.String }` 覆盖。
	// ⇒ 全仓任何地方都拿不到超过 200 字的笔记正文。
	//
	// 代价是两处功能被削到只剩开头 200 字：
	//   1. handleNoteSummarize 把它当 LLM 输入 ⇒ 长语音笔记的
	//      action_items 只能从前 200 字抽，后半段说的事抽不到；
	//   2. meetingagent 的 search_notes 工具 ⇒ agent 检索不到笔记正文。
	//
	// 为什么**新增字段**而不是把 Snippet 改成完整正文：Snippet 有 3 个
	// 消费方（learning/sources、列表渲染的摘要），它们要的就是短摘要。
	// 改 Snippet 的语义会让列表把整篇正文塞进摘要栏。
	Content        string `json:"content,omitempty"`
	ContentType    string `json:"contentType"`      // voice | text | mixed
	Domain         string `json:"domain,omitempty"` // work | study | life | idea
	Tags           string `json:"tags,omitempty"`   // JSON array
	AudioPath      string `json:"audioPath,omitempty"`
	AudioDuration  int    `json:"audioDuration,omitempty"`
	CreatedByVoice bool   `json:"createdByVoice"`
	CreatedAt      int64  `json:"createdAt"`
	UpdatedAt      int64  `json:"updatedAt"`
}

// BodyForLLM 返回适合喂给 LLM 的正文，**按 rune 截断**。
//
// 为什么单独一个方法而不是让调用方自己 slice：
//   - 上限必须是**字符**不是字节（中文一字 3 字节，按字节切会产出非法
//     UTF-8 送进 prompt；同仓库 notes/store.go 的 truncateStr 记的就是
//     这个坑在写库方向的后果：PG 直接报 SQLSTATE 22021）；
//   - 三个调用方要同一个上限，重复实现必然漂移。
//
// content 为空时回落到 snippet —— 兼容只写 snippet 的老数据。
func (n Note) BodyForLLM(maxRunes int) string {
	src := n.Content
	if strings.TrimSpace(src) == "" {
		src = n.Snippet
	}
	if maxRunes <= 0 {
		return src
	}
	if runes := []rune(src); len(runes) > maxRunes {
		return string(runes[:maxRunes])
	}
	return src
}
