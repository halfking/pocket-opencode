// Package feishu — 出站客户端：tenant_access_token 管理、消息发送与文件上传。
//
// 与 handler.go（入站事件回调）互补：handler 只收不发。发票推送需要
// bot 主动向群/用户发文件，走飞书开放平台 im/v1 API：
//
//	POST /open-apis/auth/v3/tenant_access_token/internal  取应用凭证
//	POST /open-apis/im/v1/files                           上传文件拿 file_key
//	POST /open-apis/im/v1/messages?receive_id_type=...    发文本/文件消息
//
// 未配置 AppID/Secret 时 Available()=false，上层走共享文档兜底路径。
package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Client 是最小出站客户端（并发安全）。
type Client struct {
	AppID     string
	AppSecret string
	// BaseURL 默认 https://open.feishu.cn；测试可覆盖。
	BaseURL string
	HTTP    *http.Client

	mu    sync.Mutex
	token string
	expAt time.Time
}

// New 创建客户端。appID/appSecret 为空时返回的客户端 Available()=false。
func New(appID, appSecret string) *Client {
	return &Client{
		AppID:     appID,
		AppSecret: appSecret,
		BaseURL:   "https://open.feishu.cn",
		HTTP:      &http.Client{Timeout: 30 * time.Second},
	}
}

// Available 报告出站能力是否配置齐全。
func (c *Client) Available() bool {
	return c != nil && c.AppID != "" && c.AppSecret != ""
}

type tenantTokenResp struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	// 飞书这个接口的字段是 snake_case（expire_in / tenant_access_token），
	// 与其它接口的 camelCase 不一致，是官方已知特例。
	TenantAccessToken string `json:"tenant_access_token"`
	ExpireIn          int    `json:"expire"`
}

// TenantAccessToken 获取（带缓存）应用租户 token。
func (c *Client) TenantAccessToken(ctx context.Context) (string, error) {
	if !c.Available() {
		return "", fmt.Errorf("feishu: app_id/app_secret not configured")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Now().Before(c.expAt) {
		return c.token, nil
	}
	body, _ := json.Marshal(map[string]string{"app_id": c.AppID, "app_secret": c.AppSecret})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.BaseURL+"/open-apis/auth/v3/tenant_access_token/internal", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	var out tenantTokenResp
	if err := c.doJSON(req, &out); err != nil {
		return "", err
	}
	if out.Code != 0 || out.TenantAccessToken == "" {
		return "", fmt.Errorf("feishu: token code=%d msg=%s", out.Code, out.Msg)
	}
	c.token = out.TenantAccessToken
	// 提前 5 分钟过期，避免边界失效
	ttl := time.Duration(out.ExpireIn) * time.Second
	if ttl <= 0 || ttl > time.Hour {
		ttl = time.Hour
	}
	c.expAt = time.Now().Add(ttl - 5*time.Minute)
	return c.token, nil
}

// UploadFile 上传文件（file_type 按扩展名推断），返回 file_key。
func (c *Client) UploadFile(ctx context.Context, filename string, data []byte) (string, error) {
	tok, err := c.TenantAccessToken(ctx)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return "", err
	}
	if _, err := fw.Write(data); err != nil {
		return "", err
	}
	if err := mw.WriteField("file_type", "pdf"); err != nil {
		return "", err
	}
	if err := mw.WriteField("file_name", filename); err != nil {
		return "", err
	}
	if err := mw.Close(); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.BaseURL+"/open-apis/im/v1/files", &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	var out struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			FileKey string `json:"file_key"`
		} `json:"data"`
	}
	if err := c.doJSON(req, &out); err != nil {
		return "", err
	}
	if out.Code != 0 || out.Data.FileKey == "" {
		return "", fmt.Errorf("feishu: upload code=%d msg=%s", out.Code, out.Msg)
	}
	return out.Data.FileKey, nil
}

// SendMessage 发送一条消息到 receiveID（按 receiveIDType 解析：chat_id /
// open_id / user_id / email）。msgType: text | file；
// contentText 为消息 JSON 内容（text: {"text":"..."}，file: {"file_key":"..."}）。
func (c *Client) SendMessage(ctx context.Context, receiveIDType, receiveID, msgType, contentText string) error {
	tok, err := c.TenantAccessToken(ctx)
	if err != nil {
		return err
	}
	// content 直接放 contentText 本身，**不要再包一层** {"content": ...}。
	//
	// 飞书 im/v1/messages 的 content 字段要的就是消息体本身（text 时是
	// `{"text":"..."}`，file 时是 `{"file_key":"..."}`），只不过它要求这个
	// 消息体以**字符串**形式出现（整体双重编码）。原先这里先把它包成
	// `{"content": contentText}` 再塞进去，线上实际发出去的是
	// `{"content":"{\"text\":\"...\"}"}` —— 飞书解出来看到的是一个叫
	// `content` 的未知字段，于是 text 消息正文为空、file 消息拿不到
	// file_key。**每一条消息都会失败。**
	//
	// 这个缺陷能活下来只有一个原因：这条路径**从未真正跑过**（线上缺
	// POCKET_FEISHU_INVOICE_CHAT_ID，需求 3/4 都是关的）。两侧各自独立
	// 发现了它并各自写了注释（本分支 bb1c5f10 带 mock server 测试
	// client_test.go 复现，main 侧为 7eee3669），修复代码完全一致。
	// 保留分支版注释，因为它指出了复现手段（mock server 测试）。
	url := fmt.Sprintf("%s/open-apis/im/v1/messages?receive_id_type=%s", c.BaseURL, receiveIDType)
	body, _ := json.Marshal(map[string]any{"receive_id": receiveID, "msg_type": msgType, "content": contentText})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	var out struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := c.doJSON(req, &out); err != nil {
		return err
	}
	if out.Code != 0 {
		return fmt.Errorf("feishu: send code=%d msg=%s", out.Code, out.Msg)
	}
	return nil
}

// SendText 发文本消息。
func (c *Client) SendText(ctx context.Context, receiveIDType, receiveID, text string) error {
	content, _ := json.Marshal(map[string]string{"text": text})
	return c.SendMessage(ctx, receiveIDType, receiveID, "text", string(content))
}

// SendFile 上传并发送文件消息。
func (c *Client) SendFile(ctx context.Context, receiveIDType, receiveID, filename string, data []byte) error {
	key, err := c.UploadFile(ctx, filename, data)
	if err != nil {
		return err
	}
	content, _ := json.Marshal(map[string]string{"file_key": key})
	return c.SendMessage(ctx, receiveIDType, receiveID, "file", string(content))
}

// SendInvoiceFile 推送发票 PDF 到指定群（chat_id）。filename 建议用
// {费用类型}-{对方单位}-{金额}-{日期}.pdf 规范名。
func (c *Client) SendInvoiceFile(ctx context.Context, chatID, filename string, data []byte) error {
	return c.SendFile(ctx, "chat_id", chatID, filename, data)
}

func (c *Client) doJSON(req *http.Request, out any) error {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("feishu: http %d body=%s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return json.Unmarshal(raw, out)
}
