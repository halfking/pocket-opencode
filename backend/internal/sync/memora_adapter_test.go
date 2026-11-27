package sync

// MemoraAdapter 测试（保留未来可能性轮 2026-11-27）：PushNote 的 HTML
// 白名单纪律 + nil 降级 + GetUpdatedSince 投影。
import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/memora"
)

func TestMemoraAdapter_PushNoteHTMLWhitelist(t *testing.T) {
	var saved map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&saved)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"nb_x","kb_id":"kb","title":"t","revision":1}`))
	}))
	defer srv.Close()

	c := memora.NewClientV2(srv.URL, "", "secret", "tenant", "proj", "pocketd", "", "", "", 5*time.Second)
	if c == nil {
		t.Fatal("client 构造失败")
	}
	adapter := NewMemoraAdapter(c)
	if adapter == nil {
		t.Fatal("非 nil client 必须得到非 nil adapter")
	}
	if NewMemoraAdapter(nil) != nil {
		t.Fatal("nil client 必须得到 nil adapter（未配置降级）")
	}

	id, err := adapter.PushNote(context.Background(), "local-1", "标题", "第一段\n\n第二段 <script>")
	if err != nil {
		t.Fatalf("PushNote error: %v", err)
	}
	if id != "nb_x" {
		t.Fatalf("应返回 memora id, got %q", id)
	}
	html, _ := saved["html"].(string)
	if !strings.Contains(html, "<p>第一段</p>") || !strings.Contains(html, "&lt;script&gt;") {
		t.Fatalf("正文必须 EscapeString 且只包 <p>, got %q", html)
	}
	if strings.Contains(html, "<script>") {
		t.Fatalf("白名单外标签不得出现, got %q", html)
	}
}
