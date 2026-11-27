package memora

// GetUpdatedSince 测试（保留未来可能性轮 2026-11-27）：
// 客户端 updated_at 过滤 + 首次全量（lastSync<=0）+ adapter 的 HTML 白名单纪律。
import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGetUpdatedSince_ClientSideFilter(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		// 两行：一行更新过（晚于 since），一行没动过（早于 since）。
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"items":[
			{"id":"nb_new","title":"新改动","plain_text":"更新内容","updated_at":"2026-11-20T00:00:00Z"},
			{"id":"nb_old","title":"没动","plain_text":"旧内容","updated_at":"2026-11-10T00:00:00Z"}
		],"total":2}`))
	}))
	defer srv.Close()

	c := NewClientV2(srv.URL, "", "secret", "tenant", "proj", "pocketd", "", "", "", time.Second)
	since := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC).Unix()
	got, err := c.GetUpdatedSince(context.Background(), since)
	if err != nil {
		t.Fatalf("GetUpdatedSince error: %v", err)
	}
	if len(got) != 1 || got[0].ID != "nb_new" {
		t.Fatalf("只应召回 updated_at 晚于 since 的行, got %+v", got)
	}
	if !strings.Contains(gotQuery, "page=1") || !strings.Contains(gotQuery, "limit=100") {
		t.Fatalf("应分页遍历 notebook 面, got query %q", gotQuery)
	}
}

func TestGetUpdatedSince_FirstFullSync(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"items":[
			{"id":"nb_a","title":"A","plain_text":"a","updated_at":"2020-01-01T00:00:00Z"},
			{"id":"nb_b","title":"B","plain_text":"b","updated_at":"2026-11-20T00:00:00Z"}
		],"total":2}`))
	}))
	defer srv.Close()

	c := NewClientV2(srv.URL, "", "secret", "tenant", "proj", "pocketd", "", "", "", time.Second)
	got, err := c.GetUpdatedSince(context.Background(), 0) // 首次全量
	if err != nil {
		t.Fatalf("GetUpdatedSince error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("lastSync<=0 应返回全部, got %d", len(got))
	}
}
