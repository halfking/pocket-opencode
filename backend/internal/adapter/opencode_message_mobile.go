package adapter

import "strings"

// 移动端消息视图与映射层。
//
// OpenCode 上游 GET /session/:id/message 的线格式随版本漂移：
//  1. V1 信封（已发布稳定版，SDK types.gen.ts SessionMessagesResponses）：
//     [{ info: { id, sessionID, role, time: { created } }, parts: [...] }]
//     其中 parts 是 {type:'text'|'reasoning'|'tool'|'file'|'step-*', ...} 数组，
//     用户 prompt 文本也在 parts 内（info 上没有 text 字段）。
//  2. 过渡期扁平格式：{ id, role, parts: [...] }。
//  3. V2 扁平 tagged union（开发版 session/message.ts）：
//     { id, type: 'user'|'assistant'|..., text?, content: [...], time: { created } }。
//
// opencodeMessage.UnmarshalJSON 把整个对象收进 Data map；本文件负责把三种格式
// 统一映射为 MobileMessage —— 字段名与前端 session store 的 normalizeMessage
// 契约一致（{id, role, text, content:[{type:'tool',id,name,state,input,output,
// error,durationMs}], time.created}），使历史回填与 SSE 实时渲染共用同一形状。

// MobileMessage 是 /api/mobile/sessions/{id}/messages 响应中的消息行。
type MobileMessage struct {
	ID      string              `json:"id"`
	Role    string              `json:"role"` // user | assistant | system
	Text    string              `json:"text"`
	Content []MobileContentPart `json:"content,omitempty"`
	Time    MobileMessageTime   `json:"time"`
}

// MobileMessageTime Unix 毫秒时间戳（V1/V2 线格式一致）。
type MobileMessageTime struct {
	Created   int64 `json:"created,omitempty"`
	Completed int64 `json:"completed,omitempty"`
}

// MobileContentPart 对应前端 AssistantContent：文本/推理为 {type,text}，
// 工具调用为 {type:'tool',id,name,state,input,output,error,durationMs}。
type MobileContentPart struct {
	Type       string         `json:"type"`
	Text       string         `json:"text,omitempty"`
	ID         string         `json:"id,omitempty"`
	Name       string         `json:"name,omitempty"`
	State      string         `json:"state,omitempty"` // pending | running | completed | error
	Input      map[string]any `json:"input,omitempty"`
	Output     any            `json:"output,omitempty"`
	Error      string         `json:"error,omitempty"`
	DurationMs *int64         `json:"durationMs,omitempty"`
}

// ToMobile 把解码后的 OpenCode 消息映射为移动端视图。无法提取出消息 ID
// （上游格式不可识别）时返回 nil，调用方应跳过该行。
func (m opencodeMessage) ToMobile() *MobileMessage {
	if m.Data == nil {
		return nil
	}

	// 格式 1：V1 信封 {info, parts}
	if info, ok := asMap(m.Data["info"]); ok {
		msg := &MobileMessage{
			ID:   strOr(str(info["id"]), m.ID),
			Role: strOr(str(info["role"]), "system"),
		}
		if times, ok := asMap(info["time"]); ok {
			msg.Time.Created = numOr(times["created"], 0)
			msg.Time.Completed = numOr(times["completed"], 0)
		}
		if msg.ID == "" {
			return nil
		}
		if parts, ok := m.Data["parts"].([]any); ok {
			msg.Text, msg.Content = mobileParts(parts)
		}
		return msg
	}

	// 格式 2：扁平 {id, role, parts}
	if role := str(m.Data["role"]); role != "" {
		msg := &MobileMessage{
			ID:   strOr(m.ID, str(m.Data["id"])),
			Role: role,
		}
		if times, ok := asMap(m.Data["time"]); ok {
			msg.Time.Created = numOr(times["created"], 0)
			msg.Time.Completed = numOr(times["completed"], 0)
		}
		if msg.ID == "" {
			return nil
		}
		if parts, ok := m.Data["parts"].([]any); ok {
			msg.Text, msg.Content = mobileParts(parts)
		} else if text := str(m.Data["text"]); text != "" {
			msg.Text = text
		}
		return msg
	}

	// 格式 3：V2 扁平 tagged union {id, type, text?|content?, time}
	msgType := strOr(m.Type, str(m.Data["type"]))
	if msgType == "" {
		return nil
	}
	msg := &MobileMessage{
		ID:   strOr(m.ID, str(m.Data["id"])),
		Text: str(m.Data["text"]),
	}
	switch msgType {
	case "user":
		msg.Role = "user"
	case "assistant":
		msg.Role = "assistant"
	case "synthetic", "system", "compaction", "shell":
		msg.Role = "system"
	default:
		// agent-switched / model-switched 等元消息对移动端无展示价值
		return nil
	}
	if msg.ID == "" {
		return nil
	}
	if times, ok := asMap(m.Data["time"]); ok {
		msg.Time.Created = numOr(times["created"], 0)
		msg.Time.Completed = numOr(times["completed"], 0)
	}
	// V2 user 的正文在 text；assistant 的结构化内容在 content
	if content, ok := m.Data["content"].([]any); ok {
		_, msg.Content = mobileParts(content)
		if msg.Text == "" {
			for _, c := range msg.Content {
				if c.Type == "text" {
					if msg.Text != "" {
						msg.Text += "\n\n"
					}
					msg.Text += c.Text
				}
			}
		}
	}
	return msg
}

// mobileParts 归一化 parts/content 数组，返回聚合正文与结构化 content。
// text part 聚合进正文（多个以空行分隔），reasoning/tool 保留在 content；
// step-start / step-finish / snapshot / patch 等对移动端无展示价值，跳过。
func mobileParts(parts []any) (string, []MobileContentPart) {
	var (
		text    strings.Builder
		content []MobileContentPart
	)
	for _, raw := range parts {
		part, ok := asMap(raw)
		if !ok {
			continue
		}
		switch str(part["type"]) {
		case "text":
			t := str(part["text"])
			if t == "" {
				continue
			}
			if text.Len() > 0 {
				text.WriteString("\n\n")
			}
			text.WriteString(t)
			content = append(content, MobileContentPart{Type: "text", Text: t})
		case "reasoning":
			t := str(part["text"])
			if t == "" {
				continue
			}
			content = append(content, MobileContentPart{Type: "reasoning", Text: t})
		case "tool":
			content = append(content, mobileToolPart(part))
		case "file":
			// 附件以文本行呈现（前端无 file content 类型）
			label := strOr(str(part["filename"]), str(part["url"]))
			if label == "" {
				continue
			}
			if text.Len() > 0 {
				text.WriteString("\n\n")
			}
			text.WriteString("📎 " + label)
			content = append(content, MobileContentPart{Type: "text", Text: "📎 " + label})
		}
	}
	return text.String(), content
}

// mobileToolPart 归一化单个工具调用 part。兼容 V1
// {tool, callID, state:{status,input,output,error,time:{start,end}}} 与 V2
// {name, id, state:{status,input,result|content}, time:{ran,completed}}。
func mobileToolPart(part map[string]any) MobileContentPart {
	p := MobileContentPart{
		Type:  "tool",
		ID:    strOr(str(part["callID"]), str(part["id"])),
		Name:  strOr(str(part["tool"]), str(part["name"])),
		State: "pending",
	}
	if p.Name == "" {
		p.Name = "tool"
	}
	state, ok := asMap(part["state"])
	if !ok {
		return p
	}
	p.State = strOr(str(state["status"]), "pending")
	if input, ok := asMap(state["input"]); ok && len(input) > 0 {
		p.Input = input
	}
	switch p.State {
	case "completed":
		// V1: state.output（字符串，可能是 diff）；V2: state.result / state.content
		p.Output = firstNonNil(state["output"], state["result"], state["content"])
	case "error":
		p.Error = str(state["error"])
	}
	// V1 duration 在 state.time.{start,end}；V2 在 part.time.{ran,completed}
	p.DurationMs = durationFromTimes(state["time"], "start", "end")
	if p.DurationMs == nil {
		p.DurationMs = durationFromTimes(part["time"], "ran", "completed")
	}
	return p
}

// durationFromTimes 从 {startKey: ms, endKey: ms} 中计算执行时长。
func durationFromTimes(raw any, startKey, endKey string) *int64 {
	times, ok := asMap(raw)
	if !ok {
		return nil
	}
	start, ok := num(times[startKey])
	if !ok {
		return nil
	}
	end, ok := num(times[endKey])
	if !ok || end < start {
		return nil
	}
	d := end - start
	return &d
}

// —— 解码辅助：JSON 反序列化到 map[string]any 后的类型断言 ——

func asMap(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func strOr(s, fallback string) string {
	if s != "" {
		return s
	}
	return fallback
}

func num(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int64:
		return n, true
	case int:
		return int64(n), true
	default:
		return 0, false
	}
}

func numOr(v any, fallback int64) int64 {
	if n, ok := num(v); ok {
		return n
	}
	return fallback
}

func firstNonNil(values ...any) any {
	for _, v := range values {
		if v != nil {
			return v
		}
	}
	return nil
}
