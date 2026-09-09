package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// CompanionClient is a read-only client for agent-companion native APIs.
type CompanionClient struct {
	baseURL string
	secret  string
	http    *http.Client
}

func NewCompanionClient(baseURL, secret string) *CompanionClient {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil
	}
	return &CompanionClient{
		baseURL: baseURL,
		secret:  secret,
		http:    &http.Client{Timeout: 12 * time.Second},
	}
}

func (s *Server) SetCompanionClient(c *CompanionClient) {
	if s != nil {
		s.companion = c
	}
}

type companionSession struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Title     string `json:"title"`
	Path      string `json:"path"`
	UpdatedAt int64  `json:"updatedAt"`
}

type companionMessage struct {
	Seq  int    `json:"seq"`
	ID   string `json:"id"`
	TS   int64  `json:"ts"`
	Type string `json:"type"`
	Role string `json:"role"`
	Name string `json:"name,omitempty"`
	Text string `json:"text"`
}

func (c *CompanionClient) get(path string, q url.Values, dest any) error {
	if c == nil {
		return fmt.Errorf("companion not configured")
	}
	u := c.baseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	if c.secret != "" {
		req.Header.Set("Authorization", "Bearer "+c.secret)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("companion %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	if dest == nil {
		return nil
	}
	return json.Unmarshal(body, dest)
}

func nativeAgentKind(kind string) string {
	kind = strings.TrimSpace(kind)
	if strings.HasPrefix(kind, "disk-") {
		return strings.TrimPrefix(kind, "disk-")
	}
	return kind
}

func (c *CompanionClient) GetTranscript(kind, id, types string) (companionSession, []companionMessage, error) {
	return c.GetTranscriptPage(kind, id, types, 0, 0)
}

// GetTranscriptPage 增量/分页读取会话正文：afterSeq 为 keyset 游标
// （只返回 Seq > afterSeq 的行，0 = 从头），limit<=0 时由 companion 决定。
// 客户端以返回行的最大 seq 作为下一次的 afterSeq，实现断点续传增量同步
// （docs/2026-09-09-list-sync-rules.md §4.1 会话正文按需/增量加载）。
func (c *CompanionClient) GetTranscriptPage(kind, id, types string, afterSeq, limit int) (companionSession, []companionMessage, error) {
	q := url.Values{}
	if k := nativeAgentKind(kind); k != "" {
		q.Set("kind", k)
	}
	if types != "" {
		q.Set("types", types)
	}
	if afterSeq > 0 {
		q.Set("after_seq", strconv.Itoa(afterSeq))
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var out struct {
		Session  companionSession   `json:"session"`
		Messages []companionMessage `json:"messages"`
	}
	err := c.get("/api/v1/native/sessions/"+url.PathEscape(id)+"/transcript", q, &out)
	return out.Session, out.Messages, err
}

func (c *CompanionClient) GetSession(kind, id string) (companionSession, error) {
	q := url.Values{}
	if k := nativeAgentKind(kind); k != "" {
		q.Set("kind", k)
	}
	var out companionSession
	err := c.get("/api/v1/native/sessions/"+url.PathEscape(id), q, &out)
	return out, err
}
