// 本文件是「即时转写 + 全量转写」的服务端 HTTP 暴露层（2026-10-01）。
//
// 为什么要有这两个端点，而不只是既有的 /api/stt/transcribe：
//
// 调研（2026-10-01）确定了一个硬事实——**没有任何 ASR 允许无限长音频单次上传**：
// 智谱 30 秒、OpenRouter 约 60 秒、MiniMax 500 秒。所以：
//
//  1. /api/stt/transcribe-full  整段长录音的**全量**转写。
//     服务端按静音边界切段 → 逐段转写 → 按序聚合，并如实报告哪段失败。
//     这是「停止录音后一次性拿到完整文字」的能力。
//
//  2. /api/stt/transcribe-incremental  录音进行中的**即时**转写。
//     前端 VAD 切片后逐片送入，服务端做跨片去重并返回累计文本 + 增量。
//     这是「说话时文字在长出来」的能力。
//
// 两者共用同一套目标解析（resolveSTTTarget），所以通道选择、key 管理、
// SSRF 防护与单次转写完全一致——不存在「即时能用、全量不能用」的配置割裂。
package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/stt"
)

// fullTranscribeTimeout 是全量转写的整体上限。
//
// 为什么单独给一个比单段大得多的上限：全量要串行跑 N 段（两小时会议按
// 25 秒切 = 288 段），用单段的 120 秒会立刻超时。设 10 分钟是权衡——
// 覆盖绝大多数会议，同时挡住「上游挂死导致连接永不释放」。
const fullTranscribeTimeout = 10 * time.Minute

// handleSttTranscribeFull 处理整段长录音的全量转写。
func (s *Server) handleSttTranscribeFull(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	userID := s.userIDFromRequest(r)
	wsID := s.workspaceIDFromRequest(r)
	scope := stt.Scope{UserID: userID, WorkspaceID: wsID}

	// 目标解析放在读音频之前：配置都没好时没必要先把几十 MB 音频收进来。
	target, err := s.resolveSTTTarget(r.Context(), scope)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	// 全量端点用 JSON base64 入参，不能复用 readSTTAudio —— 那个函数只认
	// multipart 与 raw audio/*，收到 application/json 时会把**整个 JSON 串**
	// 当成音频字节传下去，症状是转写返回一段莫名其妙的话或直接空文本。
	var req struct {
		AudioBase64 string `json:"audioBase64"`
		Filename    string `json:"filename"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxFullAudioBytes*4/3+1024)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	audio, err := decodeBase64AudioLimited(req.AudioBase64, maxFullAudioBytes)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	filename := req.Filename
	if filename == "" {
		filename = "meeting.wav"
	}

	engine := stt.NewResolver(func(context.Context, stt.Scope) (*stt.Target, error) { return target, nil })
	engine.SetHTTPClient(s.sttClient(120 * time.Second))

	ctx, cancel := context.WithTimeout(r.Context(), fullTranscribeTimeout)
	defer cancel()

	res, err := engine.TranscribeFull(ctx, scope, audio, filename)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": false, "error": err.Error(),
			"model": target.Model, "channel": target.Channel,
		})
		return
	}
	// 部分段失败时仍返回 200：成功段的内容对用户有价值，整段丢掉才是损失。
	// 失败信息在 failed / segments 里，前端必须把「有段失败」呈现出来。
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "text": res.Text, "segments": res.Segments,
		"failed": res.Failed, "succeeded": res.Succeeded,
		"durationMs": res.DurationMS, "costCents": res.CostCents,
		"model": res.Model, "channel": res.Channel, "label": res.Label,
	})
}

// sttIncrementalSessions 保存进行中的即时转写会话。
//
// 为什么要会话态：增量去重必须知道「此前已经产出过什么」。无状态就意味着
// 每次都要重传历史文本，或者放弃去重（段边界会累积出「今天今天下午三点」）。
//
// 淘汰策略：LRU + 容量上限。不做上限的话，长时间运行的实例会因每个用户
// 每场录音留一个会话而缓慢泄漏内存。
var sttIncrementalSessions = struct {
	mu       sync.Mutex
	sessions map[string]*stt.IncrementalTranscriber
	order    []string // 简单的插入序 LRU
}{sessions: map[string]*stt.IncrementalTranscriber{}}

const sttSessionMaxCount = 64

// sttSessionKey 会话键：user + workspace + 前端给的 sessionId。
//
// 必须带 workspace：转写目标按用户/工作区解析，跨工作区共用一个会话会让
// 去重状态串台（A 工作区的词被拿去消解 B 工作区的重叠）。
func sttSessionKey(userID, wsID, sessionID string) string {
	return userID + "\x00" + wsID + "\x00" + sessionID
}

// takeSttSession 取出（或新建）会话，并用本次请求解析出的引擎重新绑定。
//
// 引擎每次都重建是有意的：用户可能中途改了 STT 设置，沿用旧引擎会让去重
// 拿旧模型的结果去消解新模型的输出。累积的**文本状态**则必须保留，所以
// 保留会话对象、只换引擎。
func takeSttSession(key string, tr *stt.Transcriber) *stt.IncrementalTranscriber {
	sttIncrementalSessions.mu.Lock()
	defer sttIncrementalSessions.mu.Unlock()
	s, ok := sttIncrementalSessions.sessions[key]
	if !ok {
		s = stt.NewIncrementalTranscriber(tr)
		sttIncrementalSessions.sessions[key] = s
		sttIncrementalSessions.order = append(sttIncrementalSessions.order, key)
		for len(sttIncrementalSessions.order) > sttSessionMaxCount {
			oldest := sttIncrementalSessions.order[0]
			sttIncrementalSessions.order = sttIncrementalSessions.order[1:]
			delete(sttIncrementalSessions.sessions, oldest)
		}
		return s
	}
	return s.Rebind(tr)
}

func dropSttSession(key string) {
	sttIncrementalSessions.mu.Lock()
	defer sttIncrementalSessions.mu.Unlock()
	delete(sttIncrementalSessions.sessions, key)
	for i, k := range sttIncrementalSessions.order {
		if k == key {
			sttIncrementalSessions.order = append(sttIncrementalSessions.order[:i], sttIncrementalSessions.order[i+1:]...)
			break
		}
	}
}

// maxFullAudioBytes 是全量转写的音频解码后上限。
//
// 为什么远大于单次转写的上限：全量转写处理的是**整场录音**。两小时会议
// 按 16kHz 单声道 16-bit 算是 230MB，base64 膨胀后约 310MB。所以这里的
// 上限按「一整场最长的会议」而不是「一次请求」来定。
//
// 但它必须有上限：没有上限的话一个恶意/失控的请求就能把进程内存打满
// （base64 解码会一次性分配等量内存）。
const maxFullAudioBytes = 400 << 20

// maxIncrementalAudioBytes 限制单次即时转写的音频解码后大小。
//
// 为什么要显式上限：base64 的膨胀率约 4/3，48MB 的请求体解码后可能变成
// 36MB 音频，直接透传给上游既是浪费也可能打爆内存。即时转写本该是几秒
// 的切片，超过这个量级说明前端切错了，或者调用方在滥用这个端点做全量。
const maxIncrementalAudioBytes = 32 << 20

// decodeBase64Audio 解码即时转写请求里的 base64 音频（用即时转写自己的上限）。
func decodeBase64Audio(b64 string) ([]byte, error) {
	return decodeBase64AudioLimited(b64, maxIncrementalAudioBytes)
}

// decodeBase64AudioLimited 是共用的解码实现。
//
// limit 参数存在的理由：全量与即时两个端点的体量差两个数量级（整场录音
// 几百 MB vs 每片几秒），共用一个上限要么挡住全量、要么让即时端点形同虚设。
func decodeBase64AudioLimited(b64 string, limit int) ([]byte, error) {
	b64 = strings.TrimSpace(b64)
	if b64 == "" {
		return nil, fmt.Errorf("audioBase64 is required")
	}
	// 先按 base64 长度粗筛，避免为一个明显过大的请求分配等量内存。
	if len(b64) > limit*4/3+16 {
		return nil, fmt.Errorf("audio too large: base64 length %d exceeds limit", len(b64))
	}
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		// 兼容前端偶尔带上换行/空白的情况：拼接大 base64 时有时会带换行，
		// 直接失败会让用户莫名看到「转写失败」。
		data, err = base64.StdEncoding.DecodeString(strings.Map(func(r rune) rune {
			if r == '\n' || r == '\r' || r == ' ' || r == '\t' {
				return -1
			}
			return r
		}, b64))
		if err != nil {
			return nil, fmt.Errorf("invalid base64 audio")
		}
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("audio is empty")
	}
	if len(data) > limit {
		return nil, fmt.Errorf("audio too large: %d bytes exceeds limit %d", len(data), limit)
	}
	return data, nil
}

// incrementalRequest 是 /api/stt/transcribe-incremental 的请求体。
type incrementalRequest struct {
	AudioBase64 string  `json:"audioBase64"`
	Filename    string  `json:"filename"`
	SessionID   string  `json:"sessionId"`
	StartSec    float64 `json:"startSec"`
	EndSec      float64 `json:"endSec"`
	SilenceCut  bool    `json:"silenceCut"`
	// Reset 为真表示开始新会话（丢弃此前的累积文本）。
	Reset bool `json:"reset"`
	// IsFinal 为真表示这是最后一片。
	IsFinal bool `json:"isFinal"`
}

// handleSttTranscribeIncremental 处理录音进行中的即时转写。
//
// 入参是 base64 JSON 而不是 multipart：即时转写每 3-15 秒一次、每次只有几秒
// 音频，multipart 的 boundary 开销与解析成本在这个频率下不划算，且前端
// 已经在用 fetch 传 JSON。
func (s *Server) handleSttTranscribeIncremental(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	userID := s.userIDFromRequest(r)
	wsID := s.workspaceIDFromRequest(r)
	scope := stt.Scope{UserID: userID, WorkspaceID: wsID}

	var req incrementalRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 48<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	audio, err := decodeBase64Audio(req.AudioBase64)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.SessionID) == "" {
		// 没有 sessionId 就无法去重，如实报错而不是静默每次都当新会话
		// （那会让每一片都是完整文本，段边界重复文字会持续累积）。
		writeError(w, http.StatusBadRequest, "sessionId is required for incremental transcription")
		return
	}

	key := sttSessionKey(userID, wsID, req.SessionID)
	if req.Reset {
		// 显式重开会话：先丢掉旧的累积状态，避免残留文字与新录音拼在一起。
		dropSttSession(key)
	}
	if req.IsFinal {
		// 最后一片之后会话就没用了，立刻释放而不是等 LRU 淘汰。
		defer dropSttSession(key)
	}

	target, err := s.resolveSTTTarget(r.Context(), scope)
	if err != nil {
		// 目标解析失败**不**返回错误 HTTP：前端的即时文本区应该继续显示
		// 已有内容，把「配置问题」显示成「转写失败」会掩盖真正的原因。
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": false, "error": err.Error(),
		})
		return
	}

	engine := stt.NewResolver(func(context.Context, stt.Scope) (*stt.Target, error) { return target, nil })
	engine.SetHTTPClient(s.sttClient(60 * time.Second))
	// 复用会话里的累积文本状态，只换引擎（见 takeSttSession 的说明）。
	sess := takeSttSession(key, engine)

	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()

	res, err := sess.TranscribeChunk(ctx, scope, stt.IncrementalChunk{
		Audio: audio, StartSec: req.StartSec, EndSec: req.EndSec, SilenceCut: req.SilenceCut,
	}, req.IsFinal)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "text": res.Text, "delta": res.Delta,
		"startSec": res.StartSec, "endSec": res.EndSec, "isFinal": res.IsFinal,
		"model": res.Model, "channel": res.Channel, "costCents": res.CostCents,
		"error": res.Error,
	})
}
