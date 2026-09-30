package feishu

// sheet.go — 电子表格（云文档）能力：创建表格 → 取工作表 → 写单元格。
//
// 需求原文：「这些文件如果没有办法发送，可以建立共享文档及文件，进行整理，
// 需要整理一个列表，记录必要信息并汇总金额。」
//
// 原来这条只有「本地 CSV/MD 落盘」，并不是真正的共享文档——文件在设备上，
// 别人拿不到。这里按飞书开放平台的表格接口把它补齐：
//
//	POST /open-apis/sheets/v3/spreadsheets                              创建（title, folder_token）
//	GET  /open-apis/sheets/v3/spreadsheets/{token}/sheets/query         列举工作表（拿 sheet_id）
//	PUT  /open-apis/sheets/v2/spreadsheets/{token}/values               写值（valueRange.range/values）
//
// 官方文档：https://open.feishu.cn/document/server-docs/docs/sheets-v3/spreadsheet/create
// 注意元数据走 v3、单元格读写走 v2，两个版本的字段风格不同，混用会 400。
//
// 未在真实租户上跑过（本地没有 AppID/Secret），行为由 httptest 假服务器钉住：
// 端点、请求体形状、错误分支都不靠记忆。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// Spreadsheet 是一次「创建表格」的最小结果。
type Spreadsheet struct {
	Token string `json:"token"`
	URL   string `json:"url,omitempty"`
	Title string `json:"title,omitempty"`
}

// SheetInfo 是表格下的一个工作表。
type SheetInfo struct {
	SheetID string `json:"sheetId"`
	Title   string `json:"title"`
	Index   int    `json:"index"`
}

// CreateSpreadsheet 在云空间创建电子表格。folderToken 为空则建在根目录。
func (c *Client) CreateSpreadsheet(ctx context.Context, title, folderToken string) (*Spreadsheet, error) {
	tok, err := c.TenantAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	body := map[string]string{"title": title}
	if folderToken != "" {
		body["folder_token"] = folderToken
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.BaseURL+"/open-apis/sheets/v3/spreadsheets", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	var out struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Spreadsheet struct {
				SpreadsheetToken string `json:"spreadsheet_token"`
				URL              string `json:"url"`
				Title            string `json:"title"`
			} `json:"spreadsheet"`
		} `json:"data"`
	}
	if err := c.doJSON(req, &out); err != nil {
		return nil, fmt.Errorf("feishu: create spreadsheet: %w", err)
	}
	if out.Code != 0 || out.Data.Spreadsheet.SpreadsheetToken == "" {
		return nil, fmt.Errorf("feishu: create spreadsheet code=%d msg=%s", out.Code, out.Msg)
	}
	return &Spreadsheet{
		Token: out.Data.Spreadsheet.SpreadsheetToken,
		URL:   out.Data.Spreadsheet.URL,
		Title: out.Data.Spreadsheet.Title,
	}, nil
}

// ListSheets 列出表格下的工作表（取 sheet_id 用）。
func (c *Client) ListSheets(ctx context.Context, spreadsheetToken string) ([]SheetInfo, error) {
	tok, err := c.TenantAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.BaseURL+"/open-apis/sheets/v3/spreadsheets/"+spreadsheetToken+"/sheets/query", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	var out struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Sheets []SheetInfo `json:"sheets"`
		} `json:"data"`
	}
	if err := c.doJSON(req, &out); err != nil {
		return nil, fmt.Errorf("feishu: query sheets: %w", err)
	}
	if out.Code != 0 {
		return nil, fmt.Errorf("feishu: query sheets code=%d msg=%s", out.Code, out.Msg)
	}
	return out.Data.Sheets, nil
}

// FirstSheetID 取第一个工作表的 sheet_id。写值时 range 必须带它
// （形如 "<sheetId>!A1:H10"），没有它表格 API 会 400。
func (c *Client) FirstSheetID(ctx context.Context, spreadsheetToken string) (string, error) {
	sheets, err := c.ListSheets(ctx, spreadsheetToken)
	if err != nil {
		return "", err
	}
	for _, s := range sheets {
		if s.SheetID != "" {
			return s.SheetID, nil
		}
	}
	return "", fmt.Errorf("feishu: spreadsheet %s has no usable sheet", spreadsheetToken)
}

// WriteValues 写一片单元格。rng 必须形如 "<sheetId>!A1:H10"。
// 单次上限 5000 行 × 100 列，调用方自己分段。
func (c *Client) WriteValues(ctx context.Context, spreadsheetToken, rng string, values [][]any) error {
	if len(values) == 0 {
		return nil
	}
	if len(values) > 5000 {
		return fmt.Errorf("feishu: %d rows exceeds the 5000-row write limit", len(values))
	}
	tok, err := c.TenantAccessToken(ctx)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]any{
		"valueRange": map[string]any{"range": rng, "values": values},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		c.BaseURL+"/open-apis/sheets/v2/spreadsheets/"+spreadsheetToken+"/values", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	var out struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			UpdatedCells int `json:"updatedCells"`
		} `json:"data"`
	}
	if err := c.doJSON(req, &out); err != nil {
		return fmt.Errorf("feishu: write values: %w", err)
	}
	if out.Code != 0 {
		return fmt.Errorf("feishu: write values code=%d msg=%s", out.Code, out.Msg)
	}
	return nil
}
