package meetingagent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/llmbff"
	"github.com/halfking/pocket-opencode/backend/internal/notes"
)

// SearchNotesToolName 是本包唯一工具的名字。
const SearchNotesToolName = "search_notes"

// maxToolResults 是回给模型的条数上限。
//
// 上限的意义有两个：一是 token 预算（转写已经占了大部分），二是让模型
// 有取舍的压力 —— 20 条无关结果不如 5 条相关的。
const maxToolResults = 5

// NoteSource 是 search_notes 的数据源。
//
// 抽成接口是为了让 agent 包不依赖具体存储，也为了让测试能塞假数据 ——
// 智能体的行为（调了几次工具、结果怎么被引用）必须能在不碰 PG 的情况下验证。
type NoteSource interface {
	ListScoped(ctx context.Context, userID, workspaceID, domain string) ([]notes.Note, error)
}

// NoteSearcher 按关键词检索用户自己的笔记。
type NoteSearcher struct {
	Src NoteSource
	// UserID / WorkspaceID 是**检索作用域**。
	//
	// ⚠ 2026-10-09（§330.5）：此前本结构体**没有**这两个字段，
	// Execute 只能写死 `ListScoped(ctx, "", "", "")`。
	// 而 ListScoped 的 WHERE 是 `user_id = $1 AND workspace_id = $2`，
	// 只对 workspaceID 做了 `""→"default"` 回落，userID 没有 ——
	// 笔记行的 user_id 由鉴权写死、恒非空 ⇒ **查询恒零行**，
	// search_notes 恒回「没有找到」。
	//
	// ⚠ 两个都必须传。只补 UserID 会让同一用户跨工作区看到两个工作区的笔记
	// （ListScoped 自己的注释就写了这条），那是**跨租户泄漏**，比不补更糟。
	UserID      string
	WorkspaceID string
	// Now 注入时钟，测试用。
	Now func() time.Time
}

// Definition 告诉模型这个工具能干什么。
//
// Description 写给模型看，要**具体**：模型靠这段文字决定「要不要查」和
// 「查什么词」。写成「搜索笔记」这种没有信息量的话，模型会乱查。
func (n *NoteSearcher) Definition() llmbff.Tool {
	return llmbff.Tool{
		Type: "function",
		Function: llmbff.ToolFunction{
			Name: SearchNotesToolName,
			Description: "检索用户自己过去写的笔记，返回最相关的若干条（含标题与摘要）。" +
				"当转写里出现值得回溯的话题——项目名、客户、决定、待办——时调用，" +
				"以便在总结里引用用户自己的既有记录。关键词用中文实体，不要用整句。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "检索关键词，中文实体（项目名/人名/议题）",
					},
				},
				"required": []string{"query"},
			},
		},
	}
}

type searchArgs struct {
	Query string `json:"query"`
}

// Execute 检索并返回可直接喂给模型的文本。
func (n *NoteSearcher) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	var a searchArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("参数不是合法 JSON：%w", err)
	}
	q := strings.TrimSpace(a.Query)
	if q == "" {
		// 空查询不是错误，但没有可检索的东西 —— 如实说，模型会自己收尾。
		return "（未提供关键词，无法检索）", nil
	}
	if n.Src == nil {
		return "", fmt.Errorf("笔记检索未配置")
	}

	// 正文来源：2026-10-06 起 notes.Note 有了独立的 Content 字段
	// （此前 DB 的 content 列被读出来就丢弃，agent 只能搜开头 200 字）。
	// 命中判定同时看 Content 与 Snippet：Content 是全量，Snippet 是
	// 列表预览（有些老行 content 仍是空的，只有 snippet 有值）。
	all, err := n.Src.ListScoped(ctx, n.UserID, n.WorkspaceID, "")
	if err != nil {
		return "", fmt.Errorf("读取笔记失败：%w", err)
	}

	scored := scoreNotes(all, q)
	if len(scored) == 0 {
		return fmt.Sprintf("没有找到与「%s」相关的笔记。", q), nil
	}
	return renderResults(q, scored), nil
}

type hit struct {
	note  notes.Note
	score int
}

// scoreNotes 按关键词打分并排序。
//
// 为什么不引 BM25/向量检索：这是个人笔记量级（百到千条）的一次性查询，
// 且数据源只有 200 字 snippet —— 引入检索框架带来的复杂度远超收益。
// 规则如下，够用且可预测：
//
//	标题命中权重最高，摘要其次，标签再次；
//	全部按**子串**匹配，不分词（中文不分词也能靠实体串命中）。
func scoreNotes(all []notes.Note, query string) []hit {
	// 空格分词让多词查询能命中其中任一，而不是要求整串出现。
	// ⚠ 与呈现路径（evidenceWindow）**共用** queryTerms —— 两处各写一遍
	// 公式就会出现「打分认为命中、呈现找不到」的缝隙（§339）。
	terms := queryTerms(query)

	out := make([]hit, 0, len(all))
	for _, n := range all {
		title := strings.ToLower(n.Title)
		// Content 优先；为空时回落 Snippet（兼容只有 snippet 的老行）。
		body := strings.ToLower(n.BodyForLLM(0))
		snip := strings.ToLower(n.Snippet)
		score := 0
		for _, t := range terms {
			if t == "" {
				continue
			}
			if strings.Contains(title, t) {
				score += 5
			}
			// 正文命中权重高于摘要：正文被搜到说明词真的出现在笔记里，
			// 而 snippet 只有前 200 字，命中更可能是巧合。
			if strings.Contains(body, t) {
				score += 2
			} else if strings.Contains(snip, t) {
				score += 1
			}
			for _, tag := range parseTags(n.Tags) {
				if strings.Contains(strings.ToLower(tag), t) {
					score += 3
				}
			}
		}
		if score > 0 {
			out = append(out, hit{note: n, score: score})
		}
	}
	// 分数降序；同分按更新时间倒序（更新的更可能是相关的近期话题）。
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		return out[i].note.UpdatedAt > out[j].note.UpdatedAt
	})
	if len(out) > maxToolResults {
		out = out[:maxToolResults]
	}
	return out
}

// parseTags 解 tags 字段（DB 里是 JSON 数组字符串）。
// 解析失败返回空 —— 一个坏 tag 不该让整次检索失败。
func parseTags(raw string) []string {
	if raw == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

// evidenceLead / evidenceTrail 是「围绕命中位置」取窗口时，前后各留多少字。
//
// ⚠ 两者之和 = 原先固定的 evidenceWindowRunes，**所以这次改动的 token 代价是 0**。
// §337 实测：长正文下模型的引用 18/18 无据（工具说找到了、证据里却没那个词），
// 而 A 案「把 160 抬到 500」打不中第 500 字之后的命中（真实会议精校稿 932 字）。
// ⇒ 取窗口的位置比窗口的大小重要得多。
const (
	evidenceLead  = 80
	evidenceTrail = 80
)

// queryTerms 是查询分词的**唯一**取法。
//
// ⚠ scoreNotes 与 evidenceWindow 都调它 —— 两处各写一遍公式，
// 就会出现「打分认为命中、呈现找不到」这种不同源的缝隙（§330 那类坑）。
func queryTerms(query string) []string {
	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 {
		terms = []string{strings.ToLower(query)}
	}
	return terms
}

// flattenBody 是**呈现用**正文的唯一取法：BodyForLLM(0) + 压平换行。
func flattenBody(n notes.Note) string {
	return strings.Join(strings.Fields(strings.TrimSpace(n.BodyForLLM(0))), " ")
}

// evidenceWindow 返回回给模型看的那段正文：**以命中位置为中心的窗口**。
//
// ⚠ 2026-10-09（§339）：原先是「固定取前 evidenceWindowRunes 字」。
//
//	缺陷见 §334：打分用全文、呈现只给前 160 字，于是命中在第 161 字之后的
//	关键词，笔记会排进结果而模型看到的那段里**根本没有那个词**；
//	§337 在真网关上量到它的后果：长正文下 18/18 的引用无据。
//
// 为什么「命中位置」要在**压平之后**算
// --------------------------------------
// 呈现路径是 BodyForLLM → TrimSpace → strings.Fields 压平，
// 压平会改变下标。若用未压平的文本算位置，再用它去切压平后的文本，
// 窗口就会整体错位 —— 而错位的窗口看起来「有内容」，不会报错。
//
// 找不到命中位置时退回「从头取窗口」，与修复前的行为一致
// （例如只命中了标题或标签，正文里确实没有那个词）。
func evidenceWindow(n notes.Note, terms []string) string {
	flat := flattenBody(n)
	r := []rune(flat)
	limit := evidenceLead + evidenceTrail

	// 取**最早**的那个命中位置：多词查询时，模型最关心的是先出现的那处。
	hitAt := -1
	lowered := strings.ToLower(flat)
	for _, term := range terms {
		if term == "" {
			continue
		}
		if i := strings.Index(lowered, term); i >= 0 {
			at := len([]rune(flat[:i]))
			if hitAt < 0 || at < hitAt {
				hitAt = at
			}
		}
	}

	if hitAt < 0 || len(r) <= limit {
		// 没有命中位置，或正文本来就短于窗口 —— 原样给全文。
		if len(r) > limit {
			return string(r[:limit]) + "…"
		}
		return flat
	}

	from := hitAt - evidenceLead
	if from < 0 {
		from = 0
	}
	to := from + limit
	if to > len(r) {
		to = len(r)
	}
	out := string(r[from:to])
	if from > 0 {
		out = "…" + out
	}
	if to < len(r) {
		out += "…"
	}
	return out
}

func renderResults(query string, hits []hit) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "与「%s」相关的笔记 %d 条：\n", query, len(hits))
	terms := queryTerms(query) // 与 scoreNotes 同源，见 queryTerms 的注释
	for i, h := range hits {
		title := h.note.Title
		if strings.TrimSpace(title) == "" {
			title = "（无标题）"
		}
		snip := evidenceWindow(h.note, terms)
		fmt.Fprintf(&sb, "%d. [%s] %s\n   %s\n", i+1, h.note.ID, title, snip)
	}
	return sb.String()
}
