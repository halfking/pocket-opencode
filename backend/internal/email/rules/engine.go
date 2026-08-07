// Package rules is a pure-function rule engine for email auto-processing.
//
// The rules engine is intentionally decoupled from the parent email package
// to avoid an import cycle. It evaluates Account.Rules JSON against an
// EmailInput snapshot and returns the supported action set. Higher-level
// persistence (mark-important, label, skip) is performed by the caller;
// actions that are not yet supported by the data model (archive,
// route-folder, trigger-autoreply) are explicitly returned as
// ActionUnsupported so we never claim a behaviour that does not exist.
package rules

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Action 分类后的执行动作。当前实现仅支持落库到 emails.importance
// 和 emails.category 的两条；其它三个尚无对应持久化字段，调用方应忽略。
type Action string

const (
	ActionMarkImportant  Action = "mark-important"
	ActionLabelCategory Action = "label-category"
	// ActionUnsupported 表示类型匹配但当前未实现，调用方应安全忽略。
	ActionUnsupported Action = "unsupported"
)

// EmailInput 评估一条规则所需的最小信息（不依赖父包）。
type EmailInput struct {
	From       string
	Subject    string
	Body       string
	Importance string // "low" | "normal" | "high" | ""
	Category   string
	ReceivedAt time.Time
}

// ActionResult 命中的动作 + 原因摘要 + 可选的副参数（如 category 名称）。
//
// 副参数让 `label-category` 规则能把具体分类名带到调用方，而不是只标记
// "你被分类了"——前端规则编辑器已经允许在 actions 里写
// {"action":"label-category","category":"work"}，调用方拿到
// ActionResult.Category 后即可直接写入 emails.category。
type ActionResult struct {
	Action   Action
	Reason   string
	Category string // 当前仅 ActionLabelCategory 使用；其它动作留空。
}

// actionSpec 是 Rule.Actions 的内部表示，支持 "mark-important" 这类字符串
// 与 {"action":"label-category","category":"work"} 这类带副参数的对象。
// 解析在 ParseRules 里完成，调用方拿到的是统一的 ActionResult.Category。
type actionSpec struct {
	Name     string `json:"name"`
	Category string `json:"category,omitempty"`
}

// Rule 解析后的内部规则表示。Type 限定白名单；Actions 只输出支持的动作。
//
// 历史 JSON 用 ["label-category"]（字符串数组），新格式支持
// [{"name":"label-category","category":"work"}]（对象数组，含 category
// 等副参数）。ParseRules 在反序列化阶段统一两种写法，因此外部调用方无需
// 关心 JSON 形态。
type Rule struct {
	Type    string
	Pattern string
	Actions []actionSpec
}

// rawRule 与 Rule 对应但 Actions 是 json.RawMessage 数组——这是为了在
// ParseRules 内部允许 heterogeneous 数组项（字符串 or 对象），并在
// 解析时按元素单独 decode，避免 Go 标准库对混合数组的硬性限制。
type rawRule struct {
	Type    string            `json:"type"`
	Pattern string            `json:"pattern"`
	Actions []json.RawMessage `json:"actions"`
}

// ImportanceRank 把重要度文本映射到可比较的整数。
func ImportanceRank(s string) int {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "high":
		return 3
	case "normal", "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}

// ParseRules 解析 Account.Rules JSON。允许两种输入：
//   1) 新格式：{"rules": [{ "type": "...", "pattern": "...", "actions": [...] }, ...]}
//   2) 旧格式：{"whitelist": [...], "blacklist": [...], "keywords": [...]}
//
// 任一格式解析成功即返回；都不识别或解析失败时返回错误，调用方应保留
// 原始 JSON，不静默吞掉用户的配置。
func ParseRules(raw string) ([]Rule, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var anyShape map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &anyShape); err != nil {
		return nil, err
	}

	if v, ok := anyShape["rules"]; ok {
		var arr []rawRule
		if err := json.Unmarshal(v, &arr); err != nil {
			return nil, err
		}
		out := make([]Rule, 0, len(arr))
		for _, r := range arr {
			specs, err := decodeActionSpecs(r.Actions)
			if err != nil {
				return nil, fmt.Errorf("rule %q actions: %w", r.Pattern, err)
			}
			out = append(out, Rule{Type: r.Type, Pattern: r.Pattern, Actions: specs})
		}
		return out, nil
	}

	var legacy struct {
		Whitelist []string `json:"whitelist"`
		Blacklist []string `json:"blacklist"`
		Keywords  []string `json:"keywords"`
	}
	if err := json.Unmarshal([]byte(raw), &legacy); err != nil {
		return nil, err
	}
	var rs []Rule
	for _, from := range legacy.Whitelist {
		rs = append(rs, Rule{Type: "sender-whitelist", Pattern: from, Actions: []actionSpec{{Name: "mark-important"}}})
	}
	for _, from := range legacy.Blacklist {
		rs = append(rs, Rule{Type: "sender-blacklist", Pattern: from, Actions: []actionSpec{{Name: "unsupported"}}})
	}
	for _, kw := range legacy.Keywords {
		rs = append(rs, Rule{Type: "subject-keyword", Pattern: kw, Actions: []actionSpec{{Name: "label-category"}}})
	}
	return rs, nil
}

// decodeActionSpecs 把 heterogeneous action 数组统一成 []actionSpec：
//   - 字符串 → {Name: s, Category: ""}（旧格式）
//   - 对象   → {Name: <name>, Category: <category>}
// 其它类型（数组/数字/null）视为错误，避免静默吞掉用户配置。
func decodeActionSpecs(raw []json.RawMessage) ([]actionSpec, error) {
	out := make([]actionSpec, 0, len(raw))
	for i, item := range raw {
		item = bytes.TrimSpace(item)
		if len(item) == 0 {
			continue
		}
		switch item[0] {
		case '"':
			var s string
			if err := json.Unmarshal(item, &s); err != nil {
				return nil, fmt.Errorf("action[%d]: %w", i, err)
			}
			out = append(out, actionSpec{Name: s})
		case '{':
			var a actionSpec
			if err := json.Unmarshal(item, &a); err != nil {
				return nil, fmt.Errorf("action[%d]: %w", i, err)
			}
			if a.Name == "" {
				return nil, fmt.Errorf("action[%d]: missing name", i)
			}
			out = append(out, a)
		default:
			return nil, fmt.Errorf("action[%d]: unsupported shape", i)
		}
	}
	return out, nil
}

// Evaluate 对单封邮件评估所有规则并返回支持的动作 + 命中原因。
// 评估规则：黑名单命中短路（仅返回 archive，但 archive 尚不支持，
// 因此转为 unsupported，便于日志观察）。
func Evaluate(rules []Rule, in EmailInput) []ActionResult {
	fromLower := strings.ToLower(in.From)
	subjectLower := strings.ToLower(in.Subject)
	domain := ""
	if at := strings.LastIndex(fromLower, "@"); at >= 0 {
		domain = fromLower[at+1:]
	}
	results := make([]ActionResult, 0, 4)
	seen := map[Action]bool{}

	for _, r := range rules {
		matched, why := matchRule(r, in, fromLower, subjectLower, domain)
		if !matched {
			continue
		}
		for _, spec := range r.Actions {
			act := normalizeAction(spec.Name)
			if act == "" {
				continue
			}
			if seen[act] {
				continue
			}
			seen[act] = true
			results = append(results, ActionResult{Action: act, Reason: why, Category: spec.Category})
		}
	}
	// Sort for stable test order
	sort.SliceStable(results, func(i, j int) bool { return results[i].Action < results[j].Action })
	return results
}

func matchRule(r Rule, in EmailInput, fromLower, subjectLower, domain string) (bool, string) {
	switch r.Type {
	case "sender-whitelist":
		if matchEmail(r.Pattern, fromLower) {
			return true, "sender matches whitelist"
		}
	case "sender-blacklist":
		if matchEmail(r.Pattern, fromLower) {
			return true, "sender matches blacklist"
		}
	case "subject-keyword":
		if strings.Contains(subjectLower, strings.ToLower(r.Pattern)) {
			return true, "subject contains keyword"
		}
	case "domain-match":
		if domain != "" && domain == strings.ToLower(r.Pattern) {
			return true, "domain matches"
		}
	case "importance-min":
		if ImportanceRank(in.Importance) >= ImportanceRank(r.Pattern) {
			return true, "importance >= " + r.Pattern
		}
	case "category-match":
		if in.Category != "" && in.Category == r.Pattern {
			return true, "category matches"
		}
	}
	return false, ""
}

func normalizeAction(s string) Action {
	switch s {
	case "mark-important":
		return ActionMarkImportant
	case "label-category":
		return ActionLabelCategory
	case "archive", "route-folder", "trigger-autoreply":
		// 尚未实现：保留为 unsupported 以便调用方记录审计。
		return ActionUnsupported
	}
	return ""
}

func matchEmail(pattern, email string) bool {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	if pattern == "" || email == "" {
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

// SupportedActions 列出已落地的动作（前端规则编辑器可见范围）。
func SupportedActions() []string {
	return []string{string(ActionMarkImportant), string(ActionLabelCategory)}
}
