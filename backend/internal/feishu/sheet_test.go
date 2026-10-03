package feishu

// sheet_test.go — 电子表格能力的三步调用（创建 → 取 sheet_id → 写值）。
//
// 这套接口**没有在真实租户上跑过**（本地无 AppID/Secret），所以这里用
// httptest 假服务器把「端点、请求体形状、错误分支」钉死，避免以后凭记忆改坏。
// 真实环境首次启用时仍需人工确认一次（见 docs/handoff/2026-09-30-email-pipeline-verify.md §8）。

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type recordedCall struct {
	Method string
	Path   string
	Body   map[string]any
	Raw    string
}

func newSheetTestServer(t *testing.T, failOn string) (*Client, *[]recordedCall, func()) {
	t.Helper()
	var calls []recordedCall
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		calls = append(calls, recordedCall{Method: r.Method, Path: r.URL.Path, Body: body, Raw: string(raw)})
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "tenant_access_token"):
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok","tenant_access_token":"t-test","expire":7200}`))
		case r.URL.Path == "/open-apis/sheets/v3/spreadsheets":
			if failOn == "create" {
				_, _ = w.Write([]byte(`{"code":1310213,"msg":"Permission Fail"}`))
				return
			}
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"spreadsheet":{"spreadsheet_token":"shtcnTEST","url":"https://x.feishu.cn/sheets/shtcnTEST","title":"发票汇总"}}}`))
		case strings.HasSuffix(r.URL.Path, "/sheets/query"):
			if failOn == "query" {
				_, _ = w.Write([]byte(`{"code":1254043,"msg":"not found"}`))
				return
			}
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"sheets":[{"sheetId":"0Sheet1","title":"Sheet1","index":0}]}}`))
		case strings.HasSuffix(r.URL.Path, "/values"):
			if failOn == "values" {
				_, _ = w.Write([]byte(`{"code":1310213,"msg":"Permission Fail"}`))
				return
			}
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"updatedCells":18,"updatedRows":3}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":404}`))
		}
	}))
	c := New("app-id", "app-secret")
	c.BaseURL = srv.URL
	return c, &calls, srv.Close
}

func TestCreateSpreadsheet_PostsTitleToV3Endpoint(t *testing.T) {
	c, calls, done := newSheetTestServer(t, "")
	defer done()

	ss, err := c.CreateSpreadsheet(context.Background(), "发票汇总 2026-09-30", "fldTEST")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if ss.Token != "shtcnTEST" {
		t.Fatalf("unexpected token %q", ss.Token)
	}
	if len(*calls) != 2 {
		t.Fatalf("expected token + create calls, got %d", len(*calls))
	}
	create := (*calls)[1]
	if create.Method != http.MethodPost || create.Path != "/open-apis/sheets/v3/spreadsheets" {
		t.Fatalf("wrong create call: %s %s", create.Method, create.Path)
	}
	if create.Body["title"] != "发票汇总 2026-09-30" || create.Body["folder_token"] != "fldTEST" {
		t.Fatalf("create body wrong: %v", create.Body)
	}
}

func TestFirstSheetID_ReturnsSheetID(t *testing.T) {
	c, calls, done := newSheetTestServer(t, "")
	defer done()
	id, err := c.FirstSheetID(context.Background(), "shtcnTEST")
	if err != nil {
		t.Fatalf("first sheet: %v", err)
	}
	if id != "0Sheet1" {
		t.Fatalf("sheet id = %q", id)
	}
	last := (*calls)[len(*calls)-1]
	if last.Method != http.MethodGet || !strings.HasSuffix(last.Path, "/sheets/query") {
		t.Fatalf("wrong query call: %s %s", last.Method, last.Path)
	}
}

func TestWriteValues_SendsValueRangeWithSheetID(t *testing.T) {
	c, calls, done := newSheetTestServer(t, "")
	defer done()

	values := [][]any{
		{"费用类型", "对方单位", "金额"},
		{"交通", "某某出行", 1280.0},
		{"合计", "", 1280.0},
	}
	if err := c.WriteValues(context.Background(), "shtcnTEST", "0Sheet1!A1:C3", values); err != nil {
		t.Fatalf("write: %v", err)
	}
	last := (*calls)[len(*calls)-1]
	if last.Method != http.MethodPut || !strings.HasSuffix(last.Path, "/values") {
		t.Fatalf("wrong write call: %s %s", last.Method, last.Path)
	}
	vr, ok := last.Body["valueRange"].(map[string]any)
	if !ok {
		t.Fatalf("missing valueRange: %s", last.Raw)
	}
	if vr["range"] != "0Sheet1!A1:C3" {
		t.Fatalf("range = %v", vr["range"])
	}
	if got, ok := vr["values"].([]any); !ok || len(got) != 3 {
		t.Fatalf("values = %v", vr["values"])
	}
}

// 错误必须冒泡成 error，不能被当成成功（空 token / 空 sheet id 会静默产出坏表格）。
func TestSheetAPIs_SurfaceFeishuErrors(t *testing.T) {
	for _, stage := range []string{"create", "query", "values"} {
		c, _, done := newSheetTestServer(t, stage)
		var err error
		switch stage {
		case "create":
			_, err = c.CreateSpreadsheet(context.Background(), "t", "")
		case "query":
			_, err = c.FirstSheetID(context.Background(), "shtcnTEST")
		case "values":
			err = c.WriteValues(context.Background(), "shtcnTEST", "0Sheet1!A1:B2", [][]any{{"a", "b"}})
		}
		if err == nil {
			t.Fatalf("%s: expected an error", stage)
		}
		if !strings.Contains(err.Error(), "feishu:") {
			t.Fatalf("%s: error not attributed to feishu: %v", stage, err)
		}
		done()
	}
}

// 空 values 不该发请求（省一次配额）。
func TestWriteValues_EmptyIsNoop(t *testing.T) {
	c, calls, done := newSheetTestServer(t, "")
	defer done()
	if err := c.WriteValues(context.Background(), "shtcnTEST", "0Sheet1!A1:B2", nil); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 0 {
		t.Fatalf("expected no HTTP call, got %d", len(*calls))
	}
}
