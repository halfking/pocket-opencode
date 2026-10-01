package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/stt"
	"github.com/halfking/pocket-opencode/backend/internal/usersetting"
)

// 语音转写（STT）设置：存 user_settings 的 stt/default 命名空间。
//
// 为什么单独一个命名空间而不是塞进 llm_gateway：网关设置管的是「对话用哪个模型」，
// 录音转写有自己的一组约束（10MB 级音频上传、长耗时、需要成本核算），
// 而且外部 ASR 服务有独立的一把 key。混在一起会让「换对话模型」意外改掉转写目标。

const sttSettingsNamespace = "stt"
const sttSettingsID = "default"

// sttSettingsPayload 是设置页保存的载荷。
type sttSettingsPayload struct {
	// Channel: auto | gateway | external
	Channel string `json:"channel"`
	// GatewayModel 为空表示「用探测到的第一个可用模型」。
	GatewayModel string `json:"gatewayModel"`
	// External* 是外部 OpenAI 兼容 ASR 服务。
	ExternalBaseURL   string `json:"externalBaseURL"`
	ExternalModel     string `json:"externalModel"`
	ExternalTransport string `json:"externalTransport"`
	// Language 可选提示词，默认 zh。
	Language string `json:"language"`
}

type sttSettingsView struct {
	sttSettingsPayload
	HasExternalKey bool   `json:"hasExternalKey"`
	UpdatedAt      int64  `json:"updatedAt,omitempty"`
	Effective      string `json:"effectiveModel,omitempty"`
	EffectiveNote  string `json:"effectiveNote,omitempty"`
}

// sttConfigResponse 是 GET /api/stt/config 的响应。
type sttConfigResponse struct {
	Settings      sttSettingsView      `json:"settings"`
	Recommended   []stt.ModelOption    `json:"recommended"`
	Gateway       *stt.DiscoveryResult `json:"gateway"`
	GatewayBase   string               `json:"gatewayBaseURL"`
	GatewayHasKey bool                 `json:"gatewayHasKey"`
	ChannelHints  map[string]string    `json:"channelHints"`
}

// sttFallbackSettings 是「没有 PG 时的进程内兜底存储」。
//
// 为什么需要它：pocketd 没有配 POCKET_POSTGRES_DSN 时会正常启动
// （remote-only 模式），此时 s.userSettings 为 nil，而 saveSTTSettings
// 直接返回 "user settings store unavailable" → PUT /api/stt/config 恒 400。
// 后果不是「设置存不下来」这么轻：**整个 STT 功能对用户不可用**——
// 连试转、连手动填 key 的通道都进不去，因为没有地方存。
//
// 2026-10-01 黑盒验证实测：真实 pocketd（无 PG）上 PUT 恒 400，
// 后续所有转写请求都因「未配置 API Key」失败。
//
// 为什么不自己造结构：usersetting.MemStore 已经实现 Repository 接口
// 且带锁，直接复用，少一份并发正确性的负担。
//
// 边界（必须让用户知道，不能制造虚假持久化预期）：
//   - **重启即丢**，回落到默认值。
//   - 按 (user, workspace, namespace, id) 隔离，语义与 PG 路径一致。
type sttFallbackStore struct {
	once sync.Once
	mu   sync.Mutex
	repo usersetting.Repository
}

var sttFallback sttFallbackStore

// sttSettingsRepo 取出可用的设置存储：优先 PG，回落进程内。
func (s *Server) sttSettingsRepo() usersetting.Repository {
	if s != nil && s.userSettings != nil {
		return s.userSettings
	}
	sttFallback.once.Do(func() { sttFallback.repo = usersetting.NewMemStore() })
	sttFallback.mu.Lock()
	defer sttFallback.mu.Unlock()
	return sttFallback.repo
}

// loadSTTSettings 读取用户设置；没有记录时返回零值 + ok=false。
func (s *Server) loadSTTSettings(userID, workspaceID string) (sttSettingsPayload, bool) {
	var p sttSettingsPayload
	if s == nil {
		return p, false
	}
	rec, err := s.sttSettingsRepo().Get(userID, workspaceID, sttSettingsNamespace, sttSettingsID)
	if err != nil || rec == nil {
		return p, false
	}
	if err := json.Unmarshal(rec.Payload, &p); err != nil {
		return p, false
	}
	return p, true
}

// sttExternalKey 取该用户保存的外部 ASR key（密文不外泄）。
func (s *Server) sttExternalKey(userID, workspaceID string) string {
	if s == nil {
		return ""
	}
	rec, err := s.sttSettingsRepo().Get(userID, workspaceID, sttSettingsNamespace, sttSettingsID)
	if err != nil || rec == nil {
		return ""
	}
	return rec.Secret
}

func (s *Server) saveSTTSettings(userID, workspaceID string, p sttSettingsPayload, externalKey string) error {
	if s == nil {
		return fmt.Errorf("stt settings: nil server")
	}
	repo := s.sttSettingsRepo()
	// 通道归一化 + 地址修剪，避免把脏值存进去后解析不出来。
	p.Channel = stt.NormalizeChannel(p.Channel)
	p.ExternalBaseURL = strings.TrimRight(strings.TrimSpace(p.ExternalBaseURL), "/")
	p.ExternalTransport = stt.NormalizeTransport(p.ExternalTransport)
	p.GatewayModel = strings.TrimSpace(p.GatewayModel)
	p.ExternalModel = strings.TrimSpace(p.ExternalModel)
	if p.Language == "" {
		p.Language = "zh"
	}
	if p.ExternalBaseURL != "" {
		if err := validateSTTOutboundURL(p.ExternalBaseURL); err != nil {
			return err
		}
	}
	payload, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = repo.Put(usersetting.Record{
		UserID: userID, WorkspaceID: workspaceID,
		Namespace: sttSettingsNamespace, ID: sttSettingsID,
		Payload: payload, Secret: strings.TrimSpace(externalKey),
		UpdatedAt: time.Now().Unix(),
	})
	return err
}

// resolveSTTTarget 解析该用户本次转写的目标。通道语义：
//
//	auto     ：优先用网关里探测通过的模型；没有则退到外部服务；都没有则报错，
//	            且报错里带上「网关为什么不可用」的真实原因。
//	gateway  ：只用网关，指定模型必须探测通过。
//	external ：只用外部服务。
//
// 兜底：两条通道都没配置时用 env POCKET_GROQ_API_KEY（保持旧部署可用）。
func (s *Server) resolveSTTTarget(ctx context.Context, scope stt.Scope) (*stt.Target, error) {
	p, hasSettings := s.loadSTTSettings(scope.UserID, scope.WorkspaceID)
	channel := stt.NormalizeChannel(p.Channel)
	if !hasSettings {
		channel = stt.ChannelAuto
	}
	gw := s.ResolveGatewayForUser(scope.UserID, scope.WorkspaceID)
	externalKey := s.sttExternalKey(scope.UserID, scope.WorkspaceID)

	// 外部目标
	externalTarget := func() (*stt.Target, error) {
		if externalKey == "" {
			return nil, fmt.Errorf("stt_unavailable: 外部语音转写服务未配置 API Key（设置 → 语音转写）")
		}
		base := p.ExternalBaseURL
		if base == "" {
			base = defaultExternalSTTBaseURL
		}
		model := p.ExternalModel
		if model == "" {
			model = "gpt-4o-mini-transcribe"
		}
		return &stt.Target{
			BaseURL: base, APIKey: externalKey, Model: model,
			Transport: p.ExternalTransport, Channel: stt.ChannelExternal,
			Language: p.Language,
			Label:    "外部服务", CostUSDPerHour: stt.KnownUSDPerHour(model),
		}, nil
	}

	// 网关目标
	gatewayTarget := func() (*stt.Target, error) {
		if strings.TrimSpace(gw.APIKey) == "" {
			return nil, fmt.Errorf("stt_unavailable: LLM 网关未配置 API Key（设置 → LLM 网关）")
		}
		res, err := s.discoverGatewayASR(ctx, gw.BaseURL, gw.APIKey, false)
		if err != nil {
			return nil, fmt.Errorf("stt_unavailable: 网关模型目录不可用：%s", err.Error())
		}
		usable := res.UsableCandidates()
		if p.GatewayModel != "" {
			for _, c := range usable {
				if c.Model == p.GatewayModel {
					return &stt.Target{
						BaseURL: gw.BaseURL, APIKey: gw.APIKey, Model: c.Model,
						Transport: c.Transport, Channel: stt.ChannelGateway,
						Language: p.Language,
						Label:    "网关（手动指定）", CostUSDPerHour: stt.KnownUSDPerHour(c.Model),
					}, nil
				}
			}
			// 手工指定的模型当前探测不通过：说清楚为什么，别静默换模型。
			return nil, fmt.Errorf("stt_unavailable: 网关模型 %q 当前不可用（%s）",
				p.GatewayModel, candidateReason(res, p.GatewayModel))
		}
		if len(usable) == 0 {
			return nil, fmt.Errorf("stt_unavailable: 网关暂无可用的语音转写模型（%s）",
				candidateReason(res, ""))
		}
		c := usable[0]
		return &stt.Target{
			BaseURL: gw.BaseURL, APIKey: gw.APIKey, Model: c.Model,
			Transport: c.Transport, Channel: stt.ChannelGateway,
			Language: p.Language,
			Label:    "网关（自动发现）", CostUSDPerHour: stt.KnownUSDPerHour(c.Model),
		}, nil
	}

	switch channel {
	case stt.ChannelGateway:
		return gatewayTarget()
	case stt.ChannelExternal:
		return externalTarget()
	}

	// auto：先网关后外部；两边的错误都要留住，好给用户一个能行动的原因。
	gwTarget, gwErr := gatewayTarget()
	if gwErr == nil {
		return gwTarget, nil
	}
	extTarget, extErr := externalTarget()
	if extErr == nil {
		return extTarget, nil
	}
	// 两条通道都没通：优先报网关侧（更可能是用户想修的那条），并附上外部侧原因。
	//
	// 两段各自都带 `stt_unavailable:` 前缀，直接拼会在用户可见的中文句子中间
	// 露出第二个裸错误码（真机 2026-10-01 实测：
	// 「网关暂无可用的语音转写模型（…）；stt_unavailable: 外部…未配置 API Key」）。
	// 前端 sttFailureText 只剥**首位**前缀，中间那个会原样显示给用户，所以在这里
	// 去掉第二段的前缀。整体前缀保留，调用方的 HasPrefix 判断与前端窄口径都不受影响。
	return nil, fmt.Errorf("%s；%s", gwErr.Error(), stripSTTErrorCode(extErr.Error()))
}

// stripSTTErrorCode 去掉 STT 错误消息开头的 `stt_unavailable: ` 前缀。
// 只处理开头一次，不动消息内部可能出现的同名片段。
func stripSTTErrorCode(msg string) string {
	const prefix = "stt_unavailable:"
	if !strings.HasPrefix(msg, prefix) {
		return msg
	}
	return strings.TrimSpace(strings.TrimPrefix(msg, prefix))
}

const defaultExternalSTTBaseURL = "https://api.openai.com/v1"

// candidateReason 生成一句人能看懂、能行动的原因说明。
func candidateReason(res stt.DiscoveryResult, model string) string {
	if res.Error != "" {
		return "扫描失败：" + res.Error
	}
	var parts []string
	for _, c := range res.Candidates {
		if model != "" && c.Model != model {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%s", c.Model, describeProbe(c)))
	}
	if len(parts) == 0 {
		if res.TotalModels == 0 {
			return "网关未返回模型列表"
		}
		return fmt.Sprintf("网关 %d 个模型里没有语音转写类模型", res.TotalModels)
	}
	return strings.Join(parts, "；")
}

func describeProbe(c stt.Candidate) string {
	switch c.Status {
	case stt.ProbeNoProvider:
		return "网关无上游 provider"
	case stt.ProbeAudioIgnored:
		return "上游丢弃音频"
	case stt.ProbeEndpointMissing:
		return "无转写端点"
	case stt.ProbeFailed:
		// 2026-10-01 黑盒实测：c.Detail 是上游响应体（providerDetail 按字节
		// 截断到 300），网关的 503 现在返回
		// {"error":{"alternatives":{"requested_model":"…","task_type":"code",…}}。
		// 逐个候选内联进去，4 个候选就把消息顶到 1029 字符——设置页/toast 撑爆，
		// 而且按字节截断断在 JSON 中间，用户读到的只有半截花括号。
		// 这里只保留状态，原始响应去服务端日志查。
		return "探测失败"
	case "":
		return "未探测"
	default:
		return c.Status
	}
}

// discoverGatewayASR 走缓存的网关 ASR 探测。
func (s *Server) discoverGatewayASR(ctx context.Context, baseURL, apiKey string, force bool) (stt.DiscoveryResult, error) {
	if s.sttDiscovery == nil {
		s.sttDiscovery = stt.NewDiscoveryCache(10 * time.Minute)
	}
	timeout := 20 * time.Second
	// 每个候选一次真实出网请求，整体给足时间。
	if len(stt.RecommendedGatewayModels()) > 0 {
		timeout = 90 * time.Second
	}
	return stt.Discover(ctx, s.sttClient(timeout), s.sttDiscovery, baseURL, apiKey, force)
}

// sttClient 取出网客户端：测试注入过就用注入的，否则走带 SSRF 防护的默认实现。
//
// 为什么要这个回落而不是直接用字段：生产环境（cmd/pocketd）从不注入这个字段，
// 必须仍然拿到 gatewayHTTPClient 的 DNS 重绑定防护；字段为 nil 时直接返回会让
// 出网直接 panic。
//
// 分成两层是因为默认值需要一个与请求无关的超时（探测一次要打 6 个候选），
// 而测试注入的是「拒绝一切出网」的实现——给它再包一层 Timeout 也毫无意义，
// 反而会让失败从「立刻拒绝」变成「等到超时」，单测慢上几十倍。
//
// 2026-10-01 审计新增。此前这个字段是死的：discoverGatewayASR 与 /api/stt/probe
// 各自硬编码 gatewayHTTPClient(...)，所以「测试注入拒绝出网实现」根本没生效 ——
// 从 feat/2026-10-01-stt-service 恢复出来的 server_stt_settings_test.go 依赖的
// SetSTTHTTPClient 也因此根本不存在，整包编译不过。
func (s *Server) sttClient(timeout time.Duration) *http.Client {
	if s.sttHTTPClient != nil {
		return s.sttHTTPClient
	}
	return gatewayHTTPClient(timeout)
}

// SetSTTHTTPClient 注入 STT 出网客户端（供测试拒绝出网）。
//
// 存在的理由不是「为了可测性」这种套话，而是 2026-10-01 的实际事故：
// newServer 装上转写器后，server_stt_settings_test.go 里的 meeting/transcribe
// 用例顺着默认网关配置**真的出网打了生产网关**——耗时 2.3 秒、把 6 个候选的
// 503 全打了一遍，还吃到了网关限流 429。测试污染生产流量，且让单测变得随机失败。
func (s *Server) SetSTTHTTPClient(c *http.Client) { s.sttHTTPClient = c }

// ---------------------------------------------------------------- handlers

func (s *Server) handleSTTConfig(w http.ResponseWriter, r *http.Request) {
	userID := s.userIDFromRequest(r)
	wsID := s.workspaceIDFromRequest(r)
	switch r.Method {
	case http.MethodGet:
		p, _ := s.loadSTTSettings(userID, wsID)
		view := sttSettingsView{
			sttSettingsPayload: p,
			HasExternalKey:     s.sttExternalKey(userID, wsID) != "",
		}
		gw := s.ResolveGatewayForUser(userID, wsID)
		resp := sttConfigResponse{
			Settings:      view,
			Recommended:   stt.RecommendedModels(),
			GatewayBase:   gw.BaseURL,
			GatewayHasKey: strings.TrimSpace(gw.APIKey) != "",
			ChannelHints: map[string]string{
				stt.ChannelAuto:     "优先用网关里探测通过的 ASR 模型，没有再退到外部服务",
				stt.ChannelGateway:  "只用网关，网关不可用时直接报错（不静默降级）",
				stt.ChannelExternal: "只用外部 OpenAI 兼容转写服务",
			},
		}
		// 网关侧结论：只查缓存，不在 GET 里触发真实探测（否则打开设置页就打网关）。
		//
		// 两处判空都是必须的，不是防御性冗余：
		//  - sttDiscovery 可能为 nil（不走 newServer 默认初始化的部署路径）。
		//    对 nil 指针调 Peek 会 panic，而这个 handler 是**打开设置页**的必经
		//    之路——一旦 panic，整个 STT 功能对用户就是不可用的。
		//  - resp.Gateway 在缓存未命中时保持 nil，原来紧接着无条件调
		//    resp.Gateway.Best()，同样 panic。注意这条是**必然触发**的：
		//    用户首次打开设置页时缓存必然未命中，所以这不是边缘情况，
		//    而是「STT 设置页从来没被成功打开过」——单测第一次跑就抓到了。
		if s.sttDiscovery != nil {
			if res, ok := s.sttDiscovery.Peek(gw.BaseURL, gw.APIKey); ok {
				resp.Gateway = &res
				view.Effective = effectiveModelName(p, &res)
			}
		}
		// resp.Gateway 只有缓存命中时才是非 nil，而 Best() 是**值接收者**：
		// 直接 resp.Gateway.Best() 会在 nil 指针上解引用 panic，被中间件兜成
		// 500「internal server error」。
		//
		// 2026-10-01 审计：这是被恢复出来的 TestSttConfigListsBothRecommendedGroups
		// 抓到的真缺陷 —— 缓存冷（首次打开设置页、纯外部通道用户、网关没配 key）
		// 时 GET /api/stt/config 必定 500，语音转写设置页打不开。
		if resp.Gateway != nil {
			if best, ok := resp.Gateway.Best(); ok && p.GatewayModel == "" {
				view.Effective = best.Model
				view.EffectiveNote = "网关自动发现"
			}
		}
		resp.Settings = view
		writeJSON(w, http.StatusOK, resp)
	case http.MethodPut, http.MethodPost:
		var body struct {
			sttSettingsPayload
			ExternalAPIKey string `json:"externalApiKey"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		key := strings.TrimSpace(body.ExternalAPIKey)
		if err := s.saveSTTSettings(userID, wsID, body.sttSettingsPayload, key); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		// 显式置空 key：允许用户主动清掉外部服务凭据。
		if body.ExternalAPIKey == "__clear__" {
			_ = s.saveSTTSettings(userID, wsID, body.sttSettingsPayload, "")
		}
		saved, _ := s.loadSTTSettings(userID, wsID)
		writeJSON(w, http.StatusOK, sttSettingsView{
			sttSettingsPayload: saved,
			HasExternalKey:     s.sttExternalKey(userID, wsID) != "",
			UpdatedAt:          time.Now().Unix(),
		})
	default:
		writeError(w, http.StatusMethodNotAllowed, "GET/PUT only")
	}
}

func effectiveModelName(p sttSettingsPayload, res *stt.DiscoveryResult) string {
	if p.GatewayModel != "" {
		return p.GatewayModel
	}
	if res != nil {
		if best, ok := res.Best(); ok {
			return best.Model
		}
	}
	if p.ExternalModel != "" {
		return p.ExternalModel
	}
	return ""
}

// handleSTTDiscover 强制重新扫描网关并逐个探测候选模型（设置页「重新扫描」）。
func (s *Server) handleSTTDiscover(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	gw := s.ResolveGatewayForUser(s.userIDFromRequest(r), s.workspaceIDFromRequest(r))
	if strings.TrimSpace(gw.APIKey) == "" {
		writeJSON(w, http.StatusOK, stt.DiscoveryResult{BaseURL: gw.BaseURL,
			Error: "网关未配置 API Key（设置 → LLM 网关）"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	res, err := s.discoverGatewayASR(ctx, gw.BaseURL, gw.APIKey, true)
	if err != nil {
		log.Printf("[stt] gateway discovery failed: %v", err)
	}
	if res.Error == "" && err != nil {
		res.Error = err.Error()
	}
	writeJSON(w, http.StatusOK, res)
}

// handleSTTProbe 用**当前用户真实录下的一段音频**试转，用来验证「模型确实能吃音频」。
// 自动发现只验证连通性（端点/上游/是否丢音频），准确性必须由真实语音来判断，
// 而真实语音只有设备上有——所以这个接口收 multipart 音频而不是自己造样本。
func (s *Server) handleSTTProbe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	userID := s.userIDFromRequest(r)
	wsID := s.workspaceIDFromRequest(r)

	// 允许请求体里临时覆盖模型，方便用户在设置页逐个试而不必先保存。
	var override struct {
		Model     string `json:"model"`
		Channel   string `json:"channel"`
		BaseURL   string `json:"baseURL"`
		Transport string `json:"transport"`
	}
	audio, filename, err := readSTTAudio(w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// multipart 里可能同时带一个 model 字段。
	if err := r.ParseMultipartForm(25 << 20); err == nil {
		if v := strings.TrimSpace(r.FormValue("model")); v != "" {
			override.Model = v
		}
		if v := strings.TrimSpace(r.FormValue("channel")); v != "" {
			override.Channel = v
		}
		if v := strings.TrimSpace(r.FormValue("baseURL")); v != "" {
			override.BaseURL = v
		}
		if v := strings.TrimSpace(r.FormValue("transport")); v != "" {
			override.Transport = v
		}
	}

	target, err := s.probeTarget(r, userID, wsID, override)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	engine := stt.NewResolver(func(context.Context, stt.Scope) (*stt.Target, error) { return target, nil })
	engine.SetHTTPClient(s.sttClient(120 * time.Second))
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	res, err := engine.TranscribeFor(ctx, stt.Scope{UserID: userID, WorkspaceID: wsID}, audio, filename)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": false, "error": err.Error(),
			"model": target.Model, "channel": target.Channel, "transport": target.Transport,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "text": res.Text, "model": res.Model, "channel": res.Channel,
		"transport": res.Transport, "label": res.Label,
		"durationMs": res.DurationMS, "costCents": res.CostCents,
	})
}

// probeTarget 用请求里的覆盖项构造试转目标；没覆盖就用已保存的设置解析。
func (s *Server) probeTarget(r *http.Request, userID, wsID string, override struct {
	Model     string `json:"model"`
	Channel   string `json:"channel"`
	BaseURL   string `json:"baseURL"`
	Transport string `json:"transport"`
}) (*stt.Target, error) {
	if override.Model == "" {
		return s.resolveSTTTarget(r.Context(), stt.Scope{UserID: userID, WorkspaceID: wsID})
	}
	channel := stt.NormalizeChannel(override.Channel)
	if channel == stt.ChannelExternal {
		key := s.sttExternalKey(userID, wsID)
		base := strings.TrimRight(strings.TrimSpace(override.BaseURL), "/")
		if base == "" {
			p, _ := s.loadSTTSettings(userID, wsID)
			base = p.ExternalBaseURL
		}
		if base == "" {
			base = defaultExternalSTTBaseURL
		}
		if key == "" {
			return nil, fmt.Errorf("外部语音转写服务未配置 API Key（设置 → 语音转写）")
		}
		if err := validateSTTOutboundURL(base); err != nil {
			return nil, err
		}
		return &stt.Target{BaseURL: base, APIKey: key, Model: override.Model,
			Transport: stt.NormalizeTransport(override.Transport), Channel: stt.ChannelExternal,
			Label: "试转", CostUSDPerHour: stt.KnownUSDPerHour(override.Model)}, nil
	}
	gw := s.ResolveGatewayForUser(userID, wsID)
	if strings.TrimSpace(gw.APIKey) == "" {
		return nil, fmt.Errorf("LLM 网关未配置 API Key（设置 → LLM 网关）")
	}
	transport := stt.NormalizeTransport(override.Transport)
	if transport == stt.TransportAuto {
		// 复用探测结论里的传输形态，别在这里再猜一次。
		if res, ok := s.sttDiscovery.Peek(gw.BaseURL, gw.APIKey); ok {
			for _, c := range res.Candidates {
				if c.Model == override.Model && c.Transport != "" {
					transport = c.Transport
				}
			}
		}
	}
	return &stt.Target{BaseURL: gw.BaseURL, APIKey: gw.APIKey, Model: override.Model,
		Transport: transport, Channel: stt.ChannelGateway, Label: "试转"}, nil
}

// readSTTAudio 从请求里取出音频（multipart / 原始 audio/* / JSON base64 三种形态）。
func readSTTAudio(w http.ResponseWriter, r *http.Request) ([]byte, string, error) {
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		if err := r.ParseMultipartForm(25 << 20); err != nil {
			return nil, "", fmt.Errorf("failed to parse multipart: %w", err)
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			return nil, "", fmt.Errorf("missing 'file' field: %w", err)
		}
		defer file.Close()
		data, err := io.ReadAll(file)
		if err != nil {
			return nil, "", fmt.Errorf("failed to read audio: %w", err)
		}
		if len(data) == 0 {
			return nil, "", fmt.Errorf("empty audio data")
		}
		return data, header.Filename, nil
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, maxAudioBodyBytes))
	if err != nil {
		return nil, "", fmt.Errorf("failed to read audio: %w", err)
	}
	if len(data) == 0 {
		return nil, "", fmt.Errorf("empty audio data")
	}
	return data, audioFilenameForContentType(ct), nil
}
