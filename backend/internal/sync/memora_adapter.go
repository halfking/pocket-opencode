package sync

// memora_adapter.go —— memora.Client → MemoraClient 的适配器
// （保留未来可能性轮 2026-11-27）。
//
// 此前 MemoraClient 接口只有声明、零实现 ⇒ server.go 无从装配 ⇒
// /api/sync/* 恒 503。本适配器把 memora.HTTPClient 的 notebook 面
// （PushNote / GetUpdatedSince）映射到 Orchestrator 需要的窄接口：
//
//   - PushNote：notebook PushNote（201 幂等由调用方 tag+hash 组合保证，
//     见 memora/client.go PushNote 注释）；返回 memora note id。
//   - GetUpdatedSince：notebook 分页遍历 + updated_at 客户端过滤
//     （memora notebook 面暂无 since 服务端参数——见 client.go 注释）。
//
// 装配在 server.New 内按 config 门控（POCKET_MEMORA_* 齐备才建）；
// 未配置 ⇒ orchestrator 为 nil ⇒ /api/sync/* 保持 503 降级语义。
import (
	"context"
	"fmt"
	"html"
	"strings"

	"github.com/halfking/pocket-opencode/backend/internal/memora"
)

// MemoraAdapter 把 *memora.HTTPClient 适配为 sync.MemoraClient。
// client 为 nil 时返回 nil（调用方按未配置降级）。
func NewMemoraAdapter(client *memora.HTTPClient) MemoraClient {
	if client == nil {
		return nil
	}
	return &memoraAdapter{client: client}
}

type memoraAdapter struct {
	client *memora.HTTPClient
}

// PushNote 推送本地笔记到 memora，返回 memora note id。
func (a *memoraAdapter) PushNote(ctx context.Context, noteID, title, content string) (string, error) {
	req := memora.PushNoteRequest{
		Title: title,
		// 纯文本 → memora 标签白名单内的受限 HTML（与 server_memora.go
		// notePlainToHTML 同一转换纪律：只包 <p>，正文 EscapeString）。
		HTML:         plainToHTML(content),
		ChangeReason: "sync-from-pocketd",
	}
	_ = noteID // 本地 id 不进 memora（memora 侧 id 自管；对应关系在 sync_state）
	resp, err := a.client.PushNote(ctx, req)
	if err != nil {
		return "", fmt.Errorf("memora push note %q: %w", title, err)
	}
	return resp.ID, nil
}

// GetUpdatedSince 拉 memora 侧增量并投影为 Orchestrator 的 MemoraNote。
func (a *memoraAdapter) GetUpdatedSince(ctx context.Context, lastSync int64) ([]MemoraNote, error) {
	notes, err := a.client.GetUpdatedSince(ctx, lastSync)
	if err != nil {
		return nil, err
	}
	out := make([]MemoraNote, 0, len(notes))
	for _, n := range notes {
		out = append(out, MemoraNote{
			ID:        n.ID,
			Title:     n.Title,
			Content:   n.Content,
			UpdatedAt: n.UpdatedAt,
			Hash:      n.Hash,
		})
	}
	return out, nil
}

// plainToHTML 把纯文本同步内容转成 memora 白名单内的受限 HTML
// （与 server_memora.go notePlainToHTML 同款纪律：空行分段各包 <p>，
// 文本 EscapeString，不引入白名单外标签）。
func plainToHTML(content string) string {
	text := strings.ReplaceAll(content, "\r\n", "\n")
	var b strings.Builder
	for _, p := range strings.Split(text, "\n\n") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		b.WriteString("<p>")
		b.WriteString(html.EscapeString(p))
		b.WriteString("</p>")
	}
	if b.Len() == 0 {
		return "<p></p>"
	}
	return b.String()
}
