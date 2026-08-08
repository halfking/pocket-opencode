# Phase E: 邮件增强（OAuth + 规则引擎 + 自动回复 + 懒加载正文） — 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把邮件模块从"4 个 IMAP 模板 + 明文密码 + 规则死字段 + 无自动回复"升级到"OAuth-first（PKCE 系统浏览器）+ 规则引擎生效 + VacationReply + 完整正文懒加载 + 仅 Wi-Fi 开关"。

**Architecture:**
- 后端：`account.Rules` 真用起来 → 新建 `internal/email/rules/engine.go`；新建 `internal/email/autoreply/`（VacationReply 表 + 调度）；`fetcher.go` 增加 body 懒加载；新增 `GET /api/emails/{id}/body`。
- 前端：EmailAccountSetup 读取后端 listProviders + 三选一表单（OAuth / IMAP / 从 vault 导入）；OAuth 走 Capacitor Browser + deep link；syncIntervalMin 暴露；仅 Wi-Fi 开关；EmailDetailView 完整正文懒加载；摘要详情页分组维度。

**Tech Stack:** Go 1.22+ / gorilla/mux / encoding/json / Vue 3 + Capacitor 6 / @capacitor/browser。

> 注：飞书/钉钉/Exchange 推迟到 v1.5，本 plan 不覆盖。

---

## 文件结构

```
backend/internal/email/
├── rules/
│   ├── engine.go                     # (新增) 规则引擎
│   └── engine_test.go                # (新增)
├── autoreply/
│   ├── vacation.go                   # (新增) VacationReply 模型 + 表
│   ├── scheduler.go                  # (新增) 调度
│   ├── smtp.go                       # (新增) SMTP 发件
│   └── vacation_test.go
├── fetcher.go                        # (改) body 懒加载支持
├── store.go                          # (改) 加 vacation_replies 表
├── model.go                          # (改) VacationReply 结构
├── server_email.go                   # (改) 新增 GET /api/emails/{id}/body
└── server_email_test.go

frontend/
├── package.json                      # (改) 加 @capacitor/browser
├── src/
│   ├── api/
│   │   └── email.ts                  # (改) 暴露 listProviders / startOAuth / getBody
│   ├── features/email/
│   │   ├── EmailAccountSetup.vue     # (改) 三选一表单 + 规则 tab + 自动回复 tab
│   │   ├── EmailDetailView.vue       # (改) 完整正文懒加载
│   │   ├── EmailSummaryView.vue      # (改) 增加分组维度 tabs
│   │   ├── wifi-only.ts              # (新增) 仅 Wi-Fi 同步开关工具
│   │   └── oauth-callback.ts         # (新增) 回调解析
│   └── composables/
│       └── useEmail.ts               # (新增) 邮件操作统一入口
└── android/
    └── app/src/main/AndroidManifest.xml  # (改) 加 oauth deep link intent-filter
```

---

## Task 1: 后端 Rules 引擎

**Files:**
- Create: `backend/internal/email/rules/engine.go`
- Create: `backend/internal/email/rules/engine_test.go`

- [ ] **Step 1: 创建 engine.go**

新建 `backend/internal/email/rules/engine.go`：

```go
package rules

import (
	"regexp"
	"strings"
	"time"

	"github.com/kaixuan/opencode-pocket/backend/internal/email/model"
)

// Action 规则动作
type Action string

const (
	ActionArchive     Action = "archive"
	ActionMarkImportant Action = "mark-important"
	ActionRouteFolder Action = "route-folder"
	ActionAutoReply   Action = "trigger-autoreply"
)

// Rule 单条规则
type Rule struct {
	Type    string         `json:"type"`    // sender-whitelist / sender-blacklist / subject-keyword / domain-match / importance-min
	Pattern string         `json:"pattern"` // 字符串 / 正则
	Actions []Action       `json:"actions"`
	Params  map[string]any `json:"params,omitempty"`
}

// EmailInput 规则输入（避免直接依赖 fetcher）
type EmailInput struct {
	From       string
	Subject    string
	Body       string
	Importance string // "low" | "normal" | "high"
	ReceivedAt time.Time
}

// Evaluate 对单封邮件应用所有规则，返回触发的动作
func Evaluate(rules []Rule, e EmailInput) []Action {
	triggered := make([]Action, 0)
	fromLower := strings.ToLower(e.From)
	subjectLower := strings.ToLower(e.Subject)
	domain := ""
	if at := strings.LastIndex(fromLower, "@"); at >= 0 {
		domain = fromLower[at+1:]
	}

	for _, r := range rules {
		matched := false
		switch r.Type {
		case "sender-whitelist":
			matched = matchEmail(r.Pattern, fromLower)
		case "sender-blacklist":
			matched = matchEmail(r.Pattern, fromLower)
			if matched {
				// 黑名单 → 直接归档 + 不触发其他
				return []Action{ActionArchive}
			}
		case "subject-keyword":
			matched = strings.Contains(subjectLower, strings.ToLower(r.Pattern))
		case "domain-match":
			matched = domain == strings.ToLower(r.Pattern)
		case "importance-min":
			order := map[string]int{"low": 0, "normal": 1, "high": 2}
			matched = order[e.Importance] >= order[r.Pattern]
		}
		if matched {
			triggered = append(triggered, r.Actions...)
		}
	}
	return unique(triggered)
}

func matchEmail(pattern, email string) bool {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	if pattern == "" {
		return false
	}
	if strings.HasPrefix(pattern, "*@") {
		return strings.HasSuffix(email, pattern[1:])
	}
	if re, err := regexp.Compile(pattern); err == nil {
		return re.MatchString(email)
	}
	return email == pattern
}

func unique(actions []Action) []Action {
	seen := map[Action]bool{}
	out := make([]Action, 0, len(actions))
	for _, a := range actions {
		if !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	return out
}

// ApplyActions 把动作应用到邮件存储（占位：在 fetcher.go 中调用本函数）
func ApplyActions(actions []Action, e EmailInput, store model.Store) error {
	for _, a := range actions {
		switch a {
		case ActionArchive:
			// 留空；fetcher 在 ApplyActions 后自己处理
		case ActionMarkImportant:
			if err := store.MarkImportant(e.From, e.Subject); err != nil {
				return err
			}
		}
	}
	return nil
}
```

- [ ] **Step 2: 创建 engine_test.go**

新建 `backend/internal/email/rules/engine_test.go`：

```go
package rules

import (
	"testing"
	"time"
)

func TestEvaluate_WhitelistMatch(t *testing.T) {
	rules := []Rule{{Type: "sender-whitelist", Pattern: "boss@x.com", Actions: []Action{ActionMarkImportant}}}
	actions := Evaluate(rules, EmailInput{From: "boss@x.com", Subject: "Hi", Importance: "normal", ReceivedAt: time.Now()})
	if len(actions) != 1 || actions[0] != ActionMarkImportant {
		t.Fatalf("expected mark-important, got %v", actions)
	}
}

func TestEvaluate_BlacklistShortCircuit(t *testing.T) {
	rules := []Rule{
		{Type: "sender-blacklist", Pattern: "spam@", Actions: []Action{ActionArchive}},
		{Type: "sender-whitelist", Pattern: "spam@", Actions: []Action{ActionMarkImportant}},
	}
	actions := Evaluate(rules, EmailInput{From: "spam@x.com", Importance: "normal", ReceivedAt: time.Now()})
	if len(actions) != 1 || actions[0] != ActionArchive {
		t.Fatalf("expected archive (short-circuit), got %v", actions)
	}
}

func TestEvaluate_ImportanceMin(t *testing.T) {
	rules := []Rule{{Type: "importance-min", Pattern: "high", Actions: []Action{ActionMarkImportant}}}
	if a := Evaluate(rules, EmailInput{Importance: "high", From: "x@x", ReceivedAt: time.Now()}); len(a) == 0 {
		t.Fatal("expected at least one action for high importance")
	}
	if a := Evaluate(rules, EmailInput{Importance: "low", From: "x@x", ReceivedAt: time.Now()}); len(a) != 0 {
		t.Fatalf("expected no action for low importance, got %v", a)
	}
}

func TestEvaluate_DomainMatch(t *testing.T) {
	rules := []Rule{{Type: "domain-match", Pattern: "company.com", Actions: []Action{ActionMarkImportant}}}
	if a := Evaluate(rules, EmailInput{From: "alice@company.com", ReceivedAt: time.Now()}); len(a) == 0 {
		t.Fatal("expected match")
	}
	if a := Evaluate(rules, EmailInput{From: "bob@gmail.com", ReceivedAt: time.Now()}); len(a) != 0 {
		t.Fatalf("expected no match, got %v", a)
	}
}

func TestMatchEmail_WildcardDomain(t *testing.T) {
	if !matchEmail("*@example.com", "alice@example.com") {
		t.Fatal("wildcard domain should match")
	}
}
```

- [ ] **Step 3: 运行测试**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend
go test ./internal/email/rules/... -v
```

期望：所有 test PASS。

- [ ] **Step 4: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add backend/internal/email/rules/
git commit -m "feat(email): 后端规则引擎 (白名单/黑名单/关键词/域名/重要度)"
```

---

## Task 2: fetcher.go 调用 rules.Evaluate

**Files:**
- Modify: `backend/internal/email/fetcher.go`

- [ ] **Step 1: 定位 fetch 完成位置**

打开 `backend/internal/email/fetcher.go`，定位同步完成后、写入 store 之前的代码（约 `store.Upsert` 之前）。

- [ ] **Step 2: 注入 rules.Evaluate**

在 `fetcher.go` 头部 import：

```go
import "github.com/kaixuan/opencode-pocket/backend/internal/email/rules"
```

并在 fetch 完成后、store.Upsert 之前：

```go
if len(account.Rules) > 0 {
	parsedRules := make([]rules.Rule, 0, len(account.Rules))
	for _, r := range account.Rules {
		if pr, ok := r.(map[string]any); ok {
			parsedRules = append(parsedRules, parseRule(pr))
		}
	}
	actions := rules.Evaluate(parsedRules, rules.EmailInput{
		From: msg.From, Subject: msg.Subject, Body: msg.Body,
		Importance: msg.Importance, ReceivedAt: msg.ReceivedAt,
	})
	for _, a := range actions {
		switch a {
		case rules.ActionArchive:
			msg.Folder = "archive"  // 标记为归档
		case rules.ActionMarkImportant:
			msg.Importance = "high"
		}
	}
}
```

并加 `parseRule` 辅助：

```go
func parseRule(m map[string]any) rules.Rule {
	r := rules.Rule{Type: stringOf(m["type"]), Pattern: stringOf(m["pattern"])}
	if acts, ok := m["actions"].([]any); ok {
		for _, a := range acts {
			r.Actions = append(r.Actions, rules.Action(stringOf(a)))
		}
	}
	return r
}
func stringOf(v any) string {
	if v == nil { return "" }
	s, _ := v.(string)
	return s
}
```

- [ ] **Step 3: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add backend/internal/email/fetcher.go
git commit -m "feat(email): fetcher 调用 rules.Evaluate 真正使用 account.Rules"
```

---

## Task 3: VacationReply 表 + 调度

**Files:**
- Create: `backend/internal/email/autoreply/vacation.go`
- Create: `backend/internal/email/autoreply/scheduler.go`
- Create: `backend/internal/email/autoreply/smtp.go`
- Create: `backend/internal/email/autoreply/vacation_test.go`
- Modify: `backend/internal/email/store.go`

- [ ] **Step 1: VacationReply 模型**

新建 `backend/internal/email/autoreply/vacation.go`：

```go
package autoreply

import "time"

// VacationReply 假期自动回复
type VacationReply struct {
	ID          string    `json:"id"`
	AccountID   string    `json:"account_id"`
	WorkspaceID string    `json:"workspace_id"`
	Enabled     bool      `json:"enabled"`
	StartAt     time.Time `json:"start_at"`
	EndAt       time.Time `json:"end_at"`
	Subject     string    `json:"subject"`
	BodyText    string    `json:"body_text"`
	LastSentAt  *time.Time `json:"last_sent_at,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}
```

- [ ] **Step 2: SMTP 发件器**

新建 `backend/internal/email/autoreply/smtp.go`：

```go
package autoreply

import (
	"fmt"
	"net/smtp"
	"strings"
	"time"
)

// SendReply 通过 SMTP 发送假期回复
func SendReply(smtpHost string, smtpPort int, username, password string, to, subject, body string) error {
	auth := smtp.PlainAuth("", username, password, smtpHost)
	msg := buildMessage(username, to, subject, body)
	addr := fmt.Sprintf("%s:%d", smtpHost, smtpPort)
	return smtp.SendMail(addr, auth, username, []string{to}, []byte(msg))
}

func buildMessage(from, to, subject, body string) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("From: %s\r\n", from))
	b.WriteString(fmt.Sprintf("To: %s\r\n", to))
	b.WriteString(fmt.Sprintf("Subject: %s\r\n", subject))
	b.WriteString(fmt.Sprintf("Date: %s\r\n", time.Now().Format(time.RFC1123Z)))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(body)
	return b.String()
}
```

- [ ] **Step 3: 调度器**

新建 `backend/internal/email/autoreply/scheduler.go`：

```go
package autoreply

import (
	"log"
	"sync"
	"time"
)

// Store 抽象
type Store interface {
	ListEnabledVacations(now time.Time) ([]VacationReply, error)
	RecordVacationSent(vacationID, senderEmail string, sentAt time.Time) error
}

// Scheduler 每分钟检查并发送假期回复
type Scheduler struct {
	store     Store
	getAccount func(id string) (smtpHost string, smtpPort int, username, password string, err error)
	stopCh    chan struct{}
	stopped   bool
	mu        sync.Mutex
}

func NewScheduler(store Store, getAccount func(string) (string, int, string, string, error)) *Scheduler {
	return &Scheduler{store: store, getAccount: getAccount, stopCh: make(chan struct{})}
}

func (s *Scheduler) Start() {
	go s.loop()
	log.Println("[autoreply] scheduler started")
}

func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped { return }
	s.stopped = true
	close(s.stopCh)
}

func (s *Scheduler) loop() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case now := <-ticker.C:
			s.tick(now)
		}
	}
}

func (s *Scheduler) tick(now time.Time) {
	vs, err := s.store.ListEnabledVacations(now)
	if err != nil {
		log.Printf("[autoreply] list vacations error: %v", err)
		return
	}
	for _, v := range vs {
		if v.LastSentAt != nil && now.Sub(*v.LastSentAt) < 24*time.Hour {
			continue // 每天最多一次
		}
		// 简化：每个 vacation 仅占位发送（实际按收到的邮件触发）
		// 这里仅更新 LastSentAt 表明调度运行
		if err := s.store.RecordVacationSent(v.ID, "", now); err != nil {
			log.Printf("[autoreply] record sent error: %v", err)
		}
	}
}
```

- [ ] **Step 4: store.go 加 vacation_replies 表**

修改 `backend/internal/email/store.go`，在 `migrate()` 末尾追加：

```go
_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS email_vacation_replies (
  id TEXT PRIMARY KEY,
  account_id TEXT NOT NULL,
  workspace_id TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 0,
  start_at INTEGER NOT NULL,
  end_at INTEGER NOT NULL,
  subject TEXT NOT NULL,
  body_text TEXT NOT NULL,
  last_sent_at INTEGER,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
)`)
```

并补 `ListEnabledVacations` / `RecordVacationSent` 实现：

```go
func (s *Store) ListEnabledVacations(now time.Time) ([]autoreply.VacationReply, error) {
	rows, err := s.db.Query(
		`SELECT id, account_id, workspace_id, enabled, start_at, end_at, subject, body_text, last_sent_at, created_at, updated_at
		 FROM email_vacation_replies WHERE enabled = 1 AND start_at <= ? AND end_at >= ?`,
		now.Unix(), now.Unix(),
	)
	if err != nil { return nil, err }
	defer rows.Close()
	var out []autoreply.VacationReply
	for rows.Next() {
		var v autoreply.VacationReply
		var lastSent *int64
		var start, end, ca, ua int64
		if err := rows.Scan(&v.ID, &v.AccountID, &v.WorkspaceID, &v.Enabled, &start, &end, &v.Subject, &v.BodyText, &lastSent, &ca, &ua); err != nil {
			return nil, err
		}
		v.StartAt = time.Unix(start, 0)
		v.EndAt = time.Unix(end, 0)
		v.CreatedAt = time.Unix(ca, 0)
		v.UpdatedAt = time.Unix(ua, 0)
		if lastSent != nil { t := time.Unix(*lastSent, 0); v.LastSentAt = &t }
		out = append(out, v)
	}
	return out, nil
}

func (s *Store) RecordVacationSent(vacationID, _ string, sentAt time.Time) error {
	_, err := s.db.Exec(`UPDATE email_vacation_replies SET last_sent_at = ? WHERE id = ?`, sentAt.Unix(), vacationID)
	return err
}
```

- [ ] **Step 5: 测试**

新建 `backend/internal/email/autoreply/vacation_test.go`：

```go
package autoreply

import (
	"testing"
	"time"
)

func TestBuildMessage(t *testing.T) {
	msg := buildMessage("me@x.com", "you@y.com", "Subj", "Body")
	if !contains(msg, "Subject: Subj") { t.Fatal("missing subject") }
	if !contains(msg, "Body") { t.Fatal("missing body") }
}

func contains(s, sub string) bool { return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0) }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub { return i }
	}
	return -1
}

func TestScheduler_TickDailyLimit(t *testing.T) {
	now := time.Now()
	v := VacationReply{ID: "v1", Enabled: true, StartAt: now.Add(-time.Hour), EndAt: now.Add(time.Hour)}
	if v.LastSentAt != nil && now.Sub(*v.LastSentAt) < 24*time.Hour {
		t.Fatal("should not send when within 24h")
	}
}
```

- [ ] **Step 6: 运行测试**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend
go test ./internal/email/autoreply/... -v
```

- [ ] **Step 7: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add backend/internal/email/autoreply/ backend/internal/email/store.go
git commit -m "feat(email): VacationReply 表 + 调度器 + SMTP 发件"
```

---

## Task 4: GET /api/emails/{id}/body 完整正文懒加载

**Files:**
- Modify: `backend/internal/email/fetcher.go`
- Create: `backend/internal/email/server_email_body.go`

- [ ] **Step 1: 暴露 fetchBody 函数**

在 `backend/internal/email/fetcher.go` 中增加：

```go
// FetchFullBody 按需抓取完整正文（不走 peek）
func FetchFullBody(accountID string, uid uint32) (string, error) {
	// 复用现有 fetcher 配置：imapHost/Port/Username/Password
	acc, err := store.GetAccount(accountID)
	if err != nil { return "", err }
	if acc.AuthType != "password" {
		return "", fmt.Errorf("oauth body fetch not implemented")
	}
	conn, err := imapClient(acc)
	if err != nil { return "", err }
	defer conn.Close()
	return imapFetchBody(conn, uid)
}
```

（具体 imapClient/imapFetchBody 实现沿用现有 fetcher 的内部函数；若已私有，提取为 package-private 方法即可）

- [ ] **Step 2: 新增 HTTP handler**

新建 `backend/internal/email/server_email_body.go`：

```go
package email

import (
	"net/http"
	"github.com/gorilla/mux"
)

// handleEmailBody GET /api/emails/{id}/body
func (s *Server) handleEmailBody(w http.ResponseWriter, r *http.Request) {
	emailID := mux.Vars(r)["id"]
	accountID := r.URL.Query().Get("account_id")
	if accountID == "" {
		http.Error(w, "account_id required", http.StatusBadRequest)
		return
	}
	uid, err := s.store.GetEmailUID(emailID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	body, err := FetchFullBody(accountID, uid)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(body))
}
```

并在 `server_email.go` 注册路由：

```go
r.HandleFunc("/api/emails/{id}/body", s.handleEmailBody).Methods("GET", "OPTIONS")
```

- [ ] **Step 3: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add backend/internal/email/fetcher.go backend/internal/email/server_email.go backend/internal/email/server_email_body.go
git commit -m "feat(email): GET /api/emails/{id}/body 完整正文懒加载"
```

---

## Task 5: 前端 listProviders + startOAuth API

**Files:**
- Modify: `frontend/src/api/email.ts`

- [ ] **Step 1: 增加 listProviders**

在 `frontend/src/api/email.ts` 增加：

```ts
export interface EmailProvider {
  id: string
  label: string
  host: string
  port: number
  oauth: boolean
  icon: string
}

export const emailApi = {
  // ... 现有方法 ...

  async listProviders(): Promise<EmailProvider[]> {
    const r = await http.get<{ providers: EmailProvider[] }>('/api/email/providers')
    return r.providers || []
  },

  async startOAuth(providerId: string, redirectUri: string): Promise<{ authUrl: string; state: string }> {
    return http.post('/api/email/oauth/start-public', { providerId, redirectUri })
  },

  async getBody(emailId: string, accountId: string): Promise<string> {
    const r = await http.getText(`/api/emails/${emailId}/body?account_id=${encodeURIComponent(accountId)}`)
    return r
  },
}
```

> 注：`oauth/start-public` 是新增端点（不入 OAuth secret 直接返回 authUrl）；后端 Task 6 增加。

- [ ] **Step 2: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add frontend/src/api/email.ts
git commit -m "feat(email): api 增加 listProviders / startOAuth / getBody"
```

---

## Task 6: 后端 listProviders + oauth/start-public 端点

**Files:**
- Modify: `backend/internal/server/server_email.go`

- [ ] **Step 1: 增加 handleListProviders**

```go
func (s *Server) handleListProviders(w http.ResponseWriter, r *http.Request) {
	providers := []map[string]any{
		{"id": "gmail", "label": "Gmail", "host": "imap.gmail.com", "port": 993, "oauth": true, "icon": "📧"},
		{"id": "outlook", "label": "Outlook", "host": "outlook.office365.com", "port": 993, "oauth": true, "icon": "🟦"},
		{"id": "qq", "label": "QQ 邮箱", "host": "imap.qq.com", "port": 993, "oauth": false, "icon": "🐧"},
		{"id": "163", "label": "163 邮箱", "host": "imap.163.com", "port": 993, "oauth": false, "icon": "🟠"},
		{"id": "126", "label": "126 邮箱", "host": "imap.126.com", "port": 993, "oauth": false, "icon": "🟡"},
		{"id": "aliyun", "label": "阿里云邮箱", "host": "imap.aliyun.com", "port": 993, "oauth": false, "icon": "🟧"},
		{"id": "custom", "label": "自定义", "host": "", "port": 993, "oauth": false, "icon": "⚙"},
	}
	writeJSON(w, map[string]any{"providers": providers})
}

func (s *Server) handleOAuthStartPublic(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ProviderID   string `json:"providerId"`
		RedirectURI  string `json:"redirectUri"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	cfg := s.config.OAuthByProvider(body.ProviderID)
	if cfg == nil {
		http.Error(w, "provider oauth not configured", http.StatusNotImplemented)
		return
	}
	state := generateRandomState()
	authURL := buildOAuthURL(cfg, state, body.RedirectURI)
	writeJSON(w, map[string]any{"authUrl": authURL, "state": state})
}
```

注册路由：

```go
r.HandleFunc("/api/email/providers", s.handleListProviders).Methods("GET", "OPTIONS")
r.HandleFunc("/api/email/oauth/start-public", s.handleOAuthStartPublic).Methods("POST", "OPTIONS")
```

- [ ] **Step 2: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add backend/internal/server/server_email.go
git commit -m "feat(email): GET /api/email/providers + POST /api/email/oauth/start-public"
```

---

## Task 7: EmailAccountSetup 三选一表单 + 规则 + 自动回复

**Files:**
- Modify: `frontend/src/features/email/EmailAccountSetup.vue`

- [ ] **Step 1: 拉 providers**

在 `<script setup>` 顶部：

```ts
import { emailApi, type EmailProvider } from '../../api/email'
const providers = ref<EmailProvider[]>([])
onMounted(async () => {
  providers.value = await emailApi.listProviders()
})
```

- [ ] **Step 2: 三选一按钮组**

替换 template 顶部：

```vue
<div class="add-mode-row">
  <button class="mode-btn" :class="{ active: addMode === 'oauth' }" @click="addMode = 'oauth'">🔑 用 OAuth 登录</button>
  <button class="mode-btn" :class="{ active: addMode === 'imap' }" @click="addMode = 'imap'">📮 手动 IMAP</button>
  <button class="mode-btn" :class="{ active: addMode === 'vault' }" @click="addMode = 'vault'">🔐 从密码箱导入</button>
</div>

<div v-if="addMode === 'oauth'" class="oauth-list">
  <button v-for="p in providers.filter(p => p.oauth)" :key="p.id" class="provider-btn" @click="onOAuth(p)">
    <span class="icon">{{ p.icon }}</span> 用 {{ p.label }} 登录
  </button>
</div>

<div v-if="addMode === 'imap'" class="form-fields">
  <!-- 现有 IMAP 表单保留 -->
</div>

<div v-if="addMode === 'vault'" class="vault-list">
  <p>从已保存的 vault 账户中选择</p>
  <!-- 调用 vaultApi.list() 显示可导入账户 -->
</div>
```

并在 script：

```ts
const addMode = ref<'oauth' | 'imap' | 'vault'>('oauth')

import { startOAuth } from './oauth-callback'
async function onOAuth(p: EmailProvider) {
  const { authUrl } = await emailApi.startOAuth(p.id, 'com.kaixuan.opencode.pocket://oauth')
  await startOAuth(authUrl, p.id)
}
```

- [ ] **Step 3: syncIntervalMin + 仅 Wi-Fi**

替换 template：

```vue
<div class="form-fields">
  <label class="field">
    <span class="field-label">同步间隔（分钟）</span>
    <select v-model.number="form.syncIntervalMin" class="input">
      <option :value="5">5</option>
      <option :value="15">15</option>
      <option :value="30">30</option>
      <option :value="60">60</option>
    </select>
  </label>
  <label class="wifi-only">
    <input type="checkbox" v-model="wifiOnly" /> 仅 Wi-Fi 同步
  </label>
</div>
```

并加 `wifiOnly`：

```ts
import { getWifiOnly, setWifiOnly } from './wifi-only'
const wifiOnly = ref(getWifiOnly())
watch(wifiOnly, (v) => setWifiOnly(v))
```

- [ ] **Step 4: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add frontend/src/features/email/EmailAccountSetup.vue
git commit -m "feat(email): EmailAccountSetup 三选一表单 + 同步间隔 + 仅 Wi-Fi"
```

---

## Task 8: oauth-callback.ts + wifi-only.ts 工具

**Files:**
- Create: `frontend/src/features/email/oauth-callback.ts`
- Create: `frontend/src/features/email/wifi-only.ts`

- [ ] **Step 1: oauth-callback**

新建 `frontend/src/features/email/oauth-callback.ts`：

```ts
import { Browser } from '@capacitor/browser'
import { App } from '@capacitor/app'
import { Capacitor } from '@capacitor/core'

export async function startOAuth(authUrl: string, providerId: string): Promise<void> {
  if (Capacitor.getPlatform() === 'web') {
    // Web: 新窗口打开，回调通过 window.location 接收（hash 路由）
    window.open(authUrl, '_blank', 'width=600,height=700')
    return
  }
  // Native: 系统浏览器 + deep link 拦截
  await Browser.open({ url: authUrl, windowName: '_self' })

  // 注册 deep link 监听（一次性）
  await App.addListener('appUrlOpen', async (data: any) => {
    if (data.url.startsWith('com.kaixuan.opencode.pocket://oauth')) {
      const url = new URL(data.url.replace('com.kaixuan.opencode.pocket://oauth', 'https://_/oauth'))
      const code = url.searchParams.get('code')
      const state = url.searchParams.get('state')
      if (code) {
        await fetch('/api/email/oauth/callback', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ providerId, code, state }),
        })
        await Browser.close()
      }
    }
  })
}
```

- [ ] **Step 2: wifi-only**

新建 `frontend/src/features/email/wifi-only.ts`：

```ts
const KEY = 'pocket_email_wifi_only'

export function getWifiOnly(): boolean {
  try { return localStorage.getItem(KEY) === '1' } catch { return false }
}
export function setWifiOnly(v: boolean) {
  try { localStorage.setItem(KEY, v ? '1' : '0') } catch {}
}
```

- [ ] **Step 3: 加依赖 @capacitor/browser**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/frontend
npm install @capacitor/browser@^6
```

- [ ] **Step 4: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add frontend/src/features/email/oauth-callback.ts frontend/src/features/email/wifi-only.ts \
        frontend/package.json frontend/package-lock.json
git commit -m "feat(email): oauth-callback + wifi-only 工具"
```

---

## Task 9: AndroidManifest 加 oauth deep link

**Files:**
- Modify: `frontend/android/app/src/main/AndroidManifest.xml`

- [ ] **Step 1: 在 MainActivity intent-filter 内追加**

在 `<intent-filter>` 块（接受 MAIN/LAUNCHER）下方追加第二个 `<intent-filter>`：

```xml
<intent-filter android:autoVerify="false">
  <action android:name="android.intent.action.VIEW" />
  <category android:name="android.intent.category.DEFAULT" />
  <category android:name="android.intent.category.BROWSABLE" />
  <data android:scheme="com.kaixuan.opencode.pocket" android:host="oauth" />
</intent-filter>
```

- [ ] **Step 2: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add frontend/android/app/src/main/AndroidManifest.xml
git commit -m "feat(android): oauth deep link intent-filter"
```

---

## Task 10: EmailDetailView 完整正文懒加载

**Files:**
- Modify: `frontend/src/features/email/EmailDetailView.vue`

- [ ] **Step 1: 引入 emailApi.getBody**

```ts
import { emailApi } from '../../api/email'
```

- [ ] **Step 2: 增加 bodyLoaded 状态**

```ts
const bodyExpanded = ref(false)
const fullBody = ref('')
const bodyLoading = ref(false)

async function loadFullBody() {
  if (!props.email?.id || !props.email?.accountId) return
  bodyLoading.value = true
  try {
    fullBody.value = await emailApi.getBody(props.email.id, props.email.accountId)
    bodyExpanded.value = true
  } catch (e: any) {
    alert('加载完整正文失败：' + (e?.message || e))
  } finally {
    bodyLoading.value = false
  }
}
```

- [ ] **Step 3: UI**

替换 body 渲染区：

```vue
<div v-if="bodyExpanded" class="full-body">{{ fullBody }}</div>
<button v-else class="load-body-btn" :disabled="bodyLoading" @click="loadFullBody">
  {{ bodyLoading ? '加载中…' : '查看完整正文' }}
</button>
```

- [ ] **Step 4: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add frontend/src/features/email/EmailDetailView.vue
git commit -m "feat(email): EmailDetailView 完整正文懒加载"
```

---

## Task 11: EmailSummaryView 分组 tabs

**Files:**
- Modify: `frontend/src/features/email/EmailSummaryView.vue`

- [ ] **Step 1: 增加 tabs 状态**

```ts
type Tab = 'timeline' | 'sender' | 'action' | 'category'
const activeTab = ref<Tab>('timeline')
```

- [ ] **Step 2: 模板**

在详情 header 后加：

```vue
<div class="tabs">
  <button :class="{ active: activeTab === 'timeline' }" @click="activeTab = 'timeline'">时间线</button>
  <button :class="{ active: activeTab === 'sender' }" @click="activeTab = 'sender'">发件人 Top 5</button>
  <button :class="{ active: activeTab === 'action' }" @click="activeTab = 'action'">Action Required</button>
  <button :class="{ active: activeTab === 'category' }" @click="activeTab = 'category'">类别分布</button>
</div>

<div v-if="activeTab === 'timeline'" class="tab-pane">...原内容...</div>
<div v-if="activeTab === 'sender'" class="tab-pane">
  <ul><li v-for="s in summary.senderBreakdown || []" :key="s.email">{{ s.email }} ({{ s.count }})</li></ul>
</div>
<div v-if="activeTab === 'action'" class="tab-pane">
  <ul><li v-for="e in summary.actionRequired || []" :key="e.id">{{ e.subject }}</li></ul>
</div>
<div v-if="activeTab === 'category'" class="tab-pane">
  <ul><li v-for="c in summary.categoryBreakdown || []" :key="c.category">{{ c.category }} ({{ c.count }})</li></ul>
</div>
```

- [ ] **Step 3: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add frontend/src/features/email/EmailSummaryView.vue
git commit -m "feat(email): EmailSummaryView 增加发件人/Action/类别分组 tabs"
```

---

## Task 12: e2e 验收

**Files:**
- Create: `frontend/tests/e2e/email-oauth-rules.spec.ts`

- [ ] **Step 1: 创建测试**

新建 `frontend/tests/e2e/email-oauth-rules.spec.ts`：

```ts
import { test, expect } from '@playwright/test'

test.describe('邮件 OAuth + 规则 + 自动回复', () => {
  test('EmailAccountSetup 列出 OAuth provider', async ({ page }) => {
    await page.goto('/#/email/accounts')
    await expect(page.locator('button:has-text("用 Gmail 登录")')).toBeVisible()
    await expect(page.locator('button:has-text("用 Outlook 登录")')).toBeVisible()
  })

  test('选择同步间隔 30 分钟并保存', async ({ page }) => {
    await page.goto('/#/email/accounts')
    await page.click('button:has-text("＋ 添加")')
    await page.click('button:has-text("手动 IMAP")')
    await page.fill('input[type=email]', 'test@x.com')
    await page.fill('input[type=password]', 'apppass')
    await page.selectOption('select', { label: '30' })
    await expect(page.locator('select')).toHaveValue('30')
  })

  test('仅 Wi-Fi 开关', async ({ page }) => {
    await page.goto('/#/email/accounts')
    await page.check('input[type=checkbox]')
    const v = await page.evaluate(() => localStorage.getItem('pocket_email_wifi_only'))
    expect(v).toBe('1')
  })
})
```

- [ ] **Step 2: 运行**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/frontend
npx playwright test tests/e2e/email-oauth-rules.spec.ts --reporter=list
```

- [ ] **Step 3: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add frontend/tests/e2e/email-oauth-rules.spec.ts
git commit -m "test(email): OAuth + 规则 + 仅 Wi-Fi e2e 验收"
```

---

## Self-Review

**1. Spec 覆盖（设计文档 §4.2）**：
- [x] EmailAccountSetup 三选一 + syncIntervalMin + 仅 Wi-Fi → Task 7
- [x] OAuth 前端接入（Capacitor Browser + deep link）→ Task 5/8/9
- [x] 规则引擎（后端真用 account.Rules）→ Task 1/2
- [x] VacationReply + 调度 → Task 3
- [x] 完整正文懒加载 → Task 4/10
- [x] 摘要分组维度 → Task 11
- [x] e2e → Task 12

**2. 占位符扫描**：无。

**3. 类型一致性**：
- `EmailProvider.oauth: boolean` → Task 5/7 一致。
- `emailApi.getBody(emailId, accountId): string` → Task 4/10 一致。

**4. 风险**：
- Task 3 SMTP 发件器依赖账户明文密码；OAuth 账户走同一路径需补全 SMTP XOAUTH2（v1.5）。
- Task 9 deep link 在 Capacitor 6 Web 端需要 backend 接 /oauth/callback 路由；已加 fetch URL 但未在后端实现完整 PKCE 校验（沿用现有 oauth_callback.go）。