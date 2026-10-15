// meetingagent — 会议纪要的有界智能体（tool-calling 循环）。
//
// # 为什么需要一个 agent，而不是再调一次 chat
//
// 需求原文：「在总结同时，给出一些参考的资料与建议，**这个需要有一个智能体
// 来完成这些**」。单次 chat 只能「凭转写编一段话」——它不知道用户自己写过什么，
// 所以「参考资料」要么是空的（当前状态），要么是模型编的。
//
// agent 与单次 chat 的差别不在形式：在于**模型可以自己决定去查一次**，
// 拿到检索结果后再写总结。这就是「参考资料」从幻觉变成事实的唯一途径。
//
// # 工具是真实的，不是装饰
//
// 唯一的工具 `search_notes` 查的是**用户自己的笔记**（2026-10-06 起笔记会镜像到
// 后端，见 frontend notes-persist 的 mirrorNoteToBackend；此前后端 notes 表是空的，
// 任何检索都无从谈起）。所以这个 agent 的产出是「用户确实写过的东西」，
// 不是模型凭印象生成的关联。
//
// # 三条硬约束（都是为了不让它变成一个不可控的东西）
//
//  1. **有界**：最多 MaxTurns 轮。工具循环最典型的失败是模型反复调同一个工具，
//     每轮都在烧 token。到达上限就用当前已有内容收尾，不报错。
//  2. **工具失败不致命**：单个工具出错就把它作为 tool 消息回给模型
//     （"该工具失败：…"），让模型自己决定是否继续，而不是整个循环崩掉。
//  3. **不改变降级链**：Run 返回错误时，调用方必须能回落到原来的一次性
//     llmChatOnce 路径。总结是增强，不能因为 agent 不可用就没有总结。
package meetingagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/halfking/pocket-opencode/backend/internal/llmbff"
)

// DefaultMaxTurns 是默认的轮数上限。
//
// 为什么是 3：够模型「查一次 → 看结果 → 写总结」，也够它查两次不同的关键词。
// 再多只会让它反复查同一个词（实测过的失败形态），而每次查询都是一次出网。
const DefaultMaxTurns = 3

// Tool 是 agent 可调用的一个工具。
//
// 刻意做小：只有一个 Execute 方法，不给工具自己发网的能力（由 Runner 决定
// 查什么数据源）。工具集越小，模型选错工具的概率越低。
type Tool interface {
	// Definition 返回给模型的函数定义。
	Definition() llmbff.Tool
	// Execute 执行。args 是模型给的 JSON 参数。
	//
	// **返回错误而不是 panic/空串**：Runner 会把错误文本作为 tool 消息回给模型，
	// 让它自己决定换工具还是直接收尾。
	Execute(ctx context.Context, args json.RawMessage) (string, error)
}

// Runner 驱动有界 tool-calling 循环。
type Runner struct {
	// Chat 必须支持流式：只有 Delta 携带 tool_calls（见 llmbff.Delta 的注释），
	// 一次性 ChatResponse 没有 ToolCalls 字段，拿不到工具调用。
	Chat func(ctx context.Context, req llmbff.ChatRequest, kind string, fn func(llmbff.Delta) bool) (*llmbff.Usage, error)
	// WorkspaceID / UserID 用于配额归属与数据隔离（工具按用户过滤）。
	WorkspaceID string
	UserID      string
	// MaxTurns <= 0 时用 DefaultMaxTurns。
	MaxTurns int
	// Kind 传给 llmbff 的用途标记。
	Kind string
}

// Result 是一次 agent 运行的结果。
type Result struct {
	// Content 是最终模型的正文（应当是结构化 JSON，由调用方解析）。
	Content string
	// Turns 是实际跑了几轮。
	Turns int
	// ToolCalls 记录模型调过哪些工具（调试与审计用）。
	ToolCalls []string
	// Truncated 表示是否因为到达轮数上限而提前收尾（不是错误）。
	Truncated bool
	// Usage 累积用量。
	Usage llmbff.Usage
}

// Run 跑一轮有界 agent 循环。
//
// system 负责给模型立规矩；transcript 是会议转写。tools 为空时退化成一次性
// chat（模型拿不到工具，自然不会调）——这不是错误路径，是合法的退化。
func (r *Runner) Run(ctx context.Context, system, transcript string, tools []Tool) (*Result, error) {
	if r.Chat == nil {
		return nil, fmt.Errorf("meetingagent: Chat 未配置")
	}
	maxTurns := r.MaxTurns
	if maxTurns <= 0 {
		maxTurns = DefaultMaxTurns
	}

	defs := make([]llmbff.Tool, 0, len(tools))
	byName := make(map[string]Tool, len(tools))
	for _, t := range tools {
		if t == nil {
			continue
		}
		d := t.Definition()
		defs = append(defs, d)
		byName[d.Function.Name] = t
	}

	msgs := []llmbff.Message{
		{Role: llmbff.RoleSystem, Content: system},
		{Role: llmbff.RoleUser, Content: transcript},
	}

	res := &Result{}
	for turn := 1; turn <= maxTurns; turn++ {
		content, calls, usage, err := r.oneTurn(ctx, msgs, defs)
		if err != nil {
			return nil, err
		}
		res.Turns = turn
		addUsage(&res.Usage, usage)

		// 没有工具调用 = 模型已经给出最终答案。
		if len(calls) == 0 {
			res.Content = content
			return res, nil
		}

		// 有工具调用：先把 assistant 那轮原样记回消息序列（OpenAI 协议要求），
		// 再追加每个工具的 tool 消息。
		assistant := llmbff.Message{Role: llmbff.RoleAssistant, Content: content, ToolCalls: calls}
		msgs = append(msgs, assistant)

		for _, c := range calls {
			name := c.Function.Name
			res.ToolCalls = append(res.ToolCalls, name)
			out, execErr := r.execOne(ctx, byName, name, c.Function.Arguments)
			if execErr != nil {
				// 工具失败不终止整个循环：把失败当作观察结果回给模型。
				out = "工具执行失败：" + execErr.Error()
			}
			msgs = append(msgs, llmbff.Message{
				Role:       "tool",
				Content:    out,
				ToolCallID: c.ID,
			})
		}
		// 最后一轮也调了工具：没有下一次机会写总结了。
		// 显式标记 Truncated，让调用方知道结果是「工具输出」而不是「模型结论」。
		if turn == maxTurns {
			res.Truncated = true
			res.Content = renderLastToolOutput(msgs)
			return res, nil
		}
	}
	// 理论不可达（循环内每轮都会 return），保底不返回空结果。
	res.Truncated = true
	res.Content = renderLastToolOutput(msgs)
	return res, nil
}

// execOne 执行单个工具调用，未知工具名也算错误（回给模型而不是崩掉）。
func (r *Runner) execOne(ctx context.Context, byName map[string]Tool, name, args string) (string, error) {
	t, ok := byName[name]
	if !ok {
		return "", fmt.Errorf("未知工具 %q（可用：%s）", name, strings.Join(toolNames(byName), ", "))
	}
	raw := json.RawMessage(args)
	if strings.TrimSpace(args) == "" {
		// 模型偶尔给空参数串。给 `{}` 而不是报错：工具本身可能无参。
		raw = json.RawMessage(`{}`)
	}
	return t.Execute(ctx, raw)
}

func toolNames(byName map[string]Tool) []string {
	out := make([]string, 0, len(byName))
	for k := range byName {
		out = append(out, k)
	}
	return out
}

// oneTurn 跑一轮流式 chat，聚合正文与工具调用。
//
// 工具调用是 OpenAI 的**增量分片**格式：一个 tool_call 被拆成多帧，
// 每帧只有 index 与部分 arguments。所以必须按 index 累加，不能覆盖。
// 忘了这一步的后果很隐蔽：参数 JSON 会被后来的分片覆盖成半截，
// 然后工具解析失败 —— 而模型明明给对了。
func (r *Runner) oneTurn(
	ctx context.Context,
	msgs []llmbff.Message,
	defs []llmbff.Tool,
) (string, []llmbff.ToolCall, *llmbff.Usage, error) {
	req := llmbff.ChatRequest{
		WorkspaceID: r.WorkspaceID,
		User:        r.UserID,
		Messages:    msgs,
		Tools:       defs,
		Stream:      true,
		Temperature: 0.2,
		MaxTokens:   2048, // 与笔记总结同口径：网关会路由到推理模型，300 会把正文吃光
		Kind:        r.Kind,
	}

	var sb strings.Builder
	byIndex := map[int]*llmbff.ToolCall{}
	order := []int{}
	var usage *llmbff.Usage

	_, err := r.Chat(ctx, req, r.Kind, func(d llmbff.Delta) bool {
		if d.Content != "" {
			sb.WriteString(d.Content)
		}
		for _, tc := range d.ToolCalls {
			idx := toolCallIndex(tc)
			cur, ok := byIndex[idx]
			if !ok {
				// 首个分片：整帧存下来。
				cp := tc
				byIndex[idx] = &cp
				order = append(order, idx)
				continue
			}
			// 后续分片：只**拼接增量**。
			//
			// ★ 这里踩过一个坑：第一版写的是「先存一份清空 arguments 的副本，
			//   再把本帧所有字段 += 上去」。那样在**整帧一次到齐**时（很多
			//   provider 与测试假件都是这样）会把 name/arguments 追加两遍 ——
			//   症状是工具名变成 "search_notessearch_notes"，
			//   于是「未知工具」而整个 agent 看起来像坏了。
			//   首个分片之后只补增量，两种形态都正确。
			cur.ID += tc.ID
			cur.Function.Name += tc.Function.Name
			cur.Function.Arguments += tc.Function.Arguments
		}
		if d.Done && d.Usage != nil {
			usage = d.Usage
		}
		return true // 继续读到 Done
	})
	if err != nil {
		return "", nil, nil, err
	}

	out := make([]llmbff.ToolCall, 0, len(order))
	for _, idx := range order {
		out = append(out, *byIndex[idx])
	}
	return sb.String(), out, usage, nil
}

// toolCallIndex 取 OpenAI 协议自带的 index —— 这是分组的**唯一**可靠依据。
//
// ⚠ 2026-10-06 真网关复测改的。此前这里从 id 字符串里猜
// （"call_0_abc" 取中间那段），单测全绿。真网关给的却是 `call_<hex>`：
// 没有数字段 ⇒ 猜出来恒为 0。而真实网关一轮里经常**并发吐多个工具调用**
// （实测一次转写就吐了 2 个 search_notes，各带独立 id），于是
//
//	第 1 个 → 进桶 idx 0，name = "search_notes"
//	第 2 个 → 命中同一个桶，被当成「增量分片」
//	        → name += "search_notes" ⇒ "search_notessearch_notes"
//	        → arguments 拼成两个 JSON 对象（非法）
//
// 症状是「未知工具 search_notessearch_notes」，agent 每轮都失败 ⇒
// 到达轮数上限 ⇒ truncated ⇒ 生产上**每次都静默回落**到一次性总结，
// references 永远为空。而所有夹具一轮只发一个工具调用，这个形态在
// 每一个夹具下都是隐形的。
//
// 现在直接用协议字段。Index 缺省 0 仍然正确：非流式响应一轮只有一个
// 工具调用，Anthropic 路径的 index 由 content_block 下标赋值。
func toolCallIndex(tc llmbff.ToolCall) int {
	return tc.Index
}

// renderLastToolOutput 在轮数耗尽时，把最后一次工具输出当作结果。
//
// 为什么不是报错：工具查询的结果**本身**对用户就有价值（那是真实资料），
// 丢掉它比返回它更糟。调用方拿到 Truncated=true 知道这不是模型结论。
func renderLastToolOutput(msgs []llmbff.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "tool" && strings.TrimSpace(msgs[i].Content) != "" {
			return msgs[i].Content
		}
	}
	return ""
}

func addUsage(dst *llmbff.Usage, src *llmbff.Usage) {
	if src == nil {
		return
	}
	dst.PromptTokens += src.PromptTokens
	dst.CompletionTokens += src.CompletionTokens
	dst.TotalTokens += src.TotalTokens
	dst.CostUSD += src.CostUSD
}
