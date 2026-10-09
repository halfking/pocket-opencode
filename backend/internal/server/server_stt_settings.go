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
	// Channel: auto | gateway | external | minimax
	//
	// ⚠ 2026-10-08 新增 minimax：它是**第四个通道**，不是 external 的一个模型。
	// 原因是 MiniMax 原生 API 与 OpenAI 兼容层有三处硬差异
	//（路径 /v1/speech_to_text、language 走 HTTP 头、粒度参数叫 timestamp_level），
	// 塞进 external 只能靠 if 分支区分，久了就长成「按厂商 if」。
	// 详见 backend/internal/stt/provider.go 的文件头注释。
	Channel string `json:"channel"`
	// GatewayModel 为空表示「用探测到的第一个可用模型」。
	GatewayModel string `json:"gatewayModel"`
	// External* 是外部 OpenAI 兼容 ASR 服务。
	ExternalBaseURL   string `json:"externalBaseURL"`
	ExternalModel     string `json:"externalModel"`
	ExternalTransport string `json:"externalTransport"`
	// Provider 选「转写模板」（stt.ProviderIDs() 里的一个）。
	// 空 = 按 baseURL/模型推断（ProviderForTarget）。仅在 external/minimax 通道有意义。
	Provider string `json:"provider"`
	// MiniMaxBaseURL 默认 https://api.minimax.cn（国内站）；国际站填 api.minimaxi.com。
	MiniMaxBaseURL string `json:"minimaxBaseURL"`
	// MiniMaxModel 默认 asr-1.0。
	MiniMaxModel string `json:"minimaxModel"`
	// MiniMaxStream 走 SSE 流式（实测可用）。
	//
	// ⚠ 与说话人分离**互斥**（上游 400 明确拒绝：verbose_json cannot be used
	// with stream=true）。设置页据此把两者做成二选一，而不是两个独立开关。
	MiniMaxStream bool `json:"minimaxStream"`
	// MiniMaxDiarization 走 verbose_json 拿说话人标签。
	MiniMaxDiarization bool `json:"minimaxDiarization"`
	// Language 可选提示词，默认 zh。
	Language string `json:"language"`
}

type sttSettingsView struct {
	sttSettingsPayload
	HasExternalKey bool   `json:"hasExternalKey"`
	HasMiniMaxKey  bool   `json:"hasMiniMaxKey"`
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
	// Templates 是可选的转写模板清单（id + 展示名 + 能力）。
	//
	// 为什么由后端给而不是前端写死：模板集合会随部署变化（自建网关、代理层、
	// 未来新增厂商），写死在前端就等于「后端加一个模板，前端不知道」。
	// 判据在 stt.ProviderIDs()，前端只渲染。
	Templates []sttTemplateView `json:"templates"`
}

// sttTemplateView 是设置页渲染一个模板所需的最小信息。
type sttTemplateView struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// SupportsStream / SupportsDiarization 是**服务能力**，不是本仓开关。
	// 设置页据此把「边出字」和「说话人分离」在互斥的模板上做成二选一，
	// 而不是给用户两个都会失败的开关。
	SupportsStream      bool `json:"supportsStream"`
	SupportsDiarization bool `json:"supportsDiarization"`
	// StreamAndDiarizationExclusive 标记这两项**不能同时开**。
	// MiniMax 为 true（上游 400 明确拒绝，实测 (2013)）；OpenAI 兼容层为 false。
	StreamAndDiarizationExclusive bool `json:"streamAndDiarizationExclusive"`
	// MaxSeconds 是该模板单次请求的时长上限（0 = 未知）。
	MaxSeconds int `json:"maxSeconds"`
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
	return s.sttSecretFor(userID, workspaceID, sttSettingsID)
}

// sttMiniMaxKey 取该用户保存的 MiniMax key。
//
// 为什么与外部 key **分两个命名空间**存，而不是复用一条：
// 两者是不同的凭据，用户很可能只配了其中一个（网关通了就不想花钱直调、
// 或反过来）。共用一条会让「清掉外部 key」连带清掉 MiniMax 的，
// 而用户根本没意识到那是两个服务。
//
// 载荷仍然存在同一条 stt/default 记录里（设置是一组选择），
// 只有 Secret 字段分开 —— 因为 usersetting.Record 的 Secret 是单值。
func (s *Server) sttMiniMaxKey(userID, workspaceID string) string {
	return s.sttSecretFor(userID, workspaceID, sttSettingsMiniMaxID)
}

// sttSettingsMiniMaxID 是 MiniMax 凭据的记录 id（与 stt/default 载荷共享设置）。
const sttSettingsMiniMaxID = "minimax"

// sttClearSentinel 是「主动清空凭据」的哨兵值。
//
// 为什么不接受空串：前端保存设置时会提交一整套字段，其中没改过的 key 输入框
// 是空的。若把「空串」解释成「清空」，用户只改一下语种就会连带清掉两把 key。
// 所以清空必须是一个**显式**的、不会与「未提交」混淆的值。
const sttClearSentinel = "__clear__"

// normalizeMinimaxKeyInput 把请求里的 minimaxApiKey 归一化。
//
// 三种输入 → 三种结果：
//
//	nil（字段没提交）        → nil（本次不改动）
//	"__clear__"              → &""（显式清空）
//	" sk-api-xxx "           → &"sk-api-xxx"（去空白）
//
// ★ 为什么必须在这里归一化、而不是存完再补一次清理（见 PUT 分支的注释）：
//
//	「先存再清」会先把哨兵字面量写进存储，之后每次请求都重复这个错误。
//	入口归一化让清空与普通保存走同一条路径，没有中间态。
//
// ★ 另一条收益：哨兵字面量**永远不会落进存储**。
//
//	否则用户若把真 key 误填成 "__clear__"，它就成了一把"能用"的凭据，
//	而排查时看到的是「key 已设置」——比直接报错难查得多。
func normalizeMinimaxKeyInput(v *string) *string {
	if v == nil {
		return nil
	}
	s := strings.TrimSpace(*v)
	if s == sttClearSentinel {
		empty := ""
		return &empty
	}
	return &s
}

// normalizeExternalKeyInput 与 normalizeMinimaxKeyInput 同构，区别只在
// **缺省值**：外部 key 是老契约（零值 = 清空），MiniMax 是新契约（零值 = 不改动）。
//
// 为何外部不是「nil = 不改动」：既有调用点与测试都依赖「空串 = 清空」那个语义
// （例如前端只改语种的场景历史上就会清掉外部 key，行为虽不理想但已成契约）。
// 改它的语义会连带改变既有用户的实际行为，风险大于收益；
// 因此这里只做**最小修复** —— 把哨兵在入口归一化，消除「先存后清」的中间态。
func normalizeExternalKeyInput(v string) string {
	s := strings.TrimSpace(v)
	if s == sttClearSentinel {
		return ""
	}
	return s
}

// rejectSentinelKey 防呆：任何路径都不该把哨兵字面量当成真凭据存进去。
func rejectSentinelKey(field, v string) error {
	if strings.TrimSpace(v) == sttClearSentinel {
		return fmt.Errorf("%s 不能是保留值 %q（那是「清空凭据」的信号）", field, sttClearSentinel)
	}
	return nil
}

func (s *Server) sttSecretFor(userID, workspaceID, id string) string {
	if s == nil {
		return ""
	}
	rec, err := s.sttSettingsRepo().Get(userID, workspaceID, sttSettingsNamespace, id)
	if err != nil || rec == nil {
		return ""
	}
	return rec.Secret
}

func (s *Server) saveSTTSettings(userID, workspaceID string, p sttSettingsPayload, externalKey string) error {
	return s.saveSTTSettingsWithKeys(userID, workspaceID, p, externalKey, nil)
}

// saveSTTSettingsWithKeys 保存设置与两把 key。
//
// minimaxKey 传 nil 表示「本次不改动 MiniMax 凭据」。
//
// 为什么用「nil = 不改」而不是「空串 = 清空」：前端 PUT 通常只提交它改过的
// 字段。若把「没提交」解释成「清空」，那么用户只改一下语种就会连带清掉
// 两把 key —— 而「清空」是必须**显式**表达的动作（下面用 "__clear__"）。
// externalKey 保留旧的 string 签名（空串 = 清空），因为既有调用点与
// 测试依赖那个语义；MiniMax 走新的 nil 语义。
func (s *Server) saveSTTSettingsWithKeys(userID, workspaceID string, p sttSettingsPayload, externalKey string, minimaxKey *string) error {
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
	p.Provider = strings.TrimSpace(p.Provider)
	p.MiniMaxBaseURL = strings.TrimRight(strings.TrimSpace(p.MiniMaxBaseURL), "/")
	p.MiniMaxModel = strings.TrimSpace(p.MiniMaxModel)
	if p.Language == "" {
		p.Language = "zh"
	}
	// 未知模板 id 直接拒存，而不是存下去等转写时报错。
	// 理由：转写报错发生在**用户已经开始录音之后**，那时才知道配置错了
	// 是最坏的时机。保存时拒掉能让错误当场暴露。
	if p.Provider != "" && stt.LookupProvider(p.Provider) == nil {
		return fmt.Errorf("未知的转写模板 %q（可选：%s）", p.Provider, strings.Join(stt.ProviderIDs(), "、"))
	}
	if p.ExternalBaseURL != "" {
		if err := validateSTTOutboundURL(p.ExternalBaseURL); err != nil {
			return err
		}
	}
	if p.MiniMaxBaseURL != "" {
		if err := validateSTTOutboundURL(p.MiniMaxBaseURL); err != nil {
			return err
		}
	}
	payload, err := json.Marshal(p)
	if err != nil {
		return err
	}
	extRec := usersetting.Record{
		UserID: userID, WorkspaceID: workspaceID,
		Namespace: sttSettingsNamespace, ID: sttSettingsID,
		Payload: payload, Secret: strings.TrimSpace(externalKey),
		UpdatedAt: time.Now().Unix(),
	}
	// ★ 同上：外部 key 的「清空」也必须走 ClearSecret。
	//   这一处的语义是**既有契约**（空串 = 清空），但那个契约在存储层
	//   一直是失效的 —— 空串被当成「不改动」。真机验证实测：
	//   连发两次 `externalApiKey: "__clear__"`，凭据纹丝不动。
	//   现在把它从「靠巧合工作」变成「显式表达」。
	if strings.TrimSpace(externalKey) == "" {
		extRec.ClearSecret = true
	}
	if _, err := repo.Put(extRec); err != nil {
		return err
	}
	if minimaxKey != nil {
		if err := rejectSentinelKey("minimaxApiKey", *minimaxKey); err != nil {
			return err
		}
		v := strings.TrimSpace(*minimaxKey)
		// ★ 清空必须用 ClearSecret，不能靠 Secret=""。
		//   存储层把 `Secret == ""` 解释为「本次不改动密文」
		//   （MemStore.Put / PG Put 都如此，见 usersetting/types.go 的注释），
		//   所以传空串会**永远清不掉** —— 那正是 2026-10-08 真机验证抓到的
		//   缺陷：用户点「清除」，hasMiniMaxKey 一直 true，转写恒 401。
		//   这个分支是 nil（不改动）与非 nil（显式设置/清空）的分界点，
		//   两个子语义都必须在这里正确落到 ClearSecret 上。
		rec := usersetting.Record{
			UserID: userID, WorkspaceID: workspaceID,
			Namespace: sttSettingsNamespace, ID: sttSettingsMiniMaxID,
			Payload: payload, Secret: v,
			UpdatedAt: time.Now().Unix(),
		}
		if v == "" {
			rec.ClearSecret = true
		}
		if _, err := repo.Put(rec); err != nil {
			return err
		}
	}
	return nil
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

	// MiniMax 直调目标。
	//
	// ★ 这里显式指定 Provider 而不是让 ProviderForTarget 推断：
	// 推断规则里「baseURL 含 minimax 域名」是一条**启发式**，
	// 而这里是用户的明确选择。用显式指定可以保证「用户选了 MiniMax 就一定
	// 按 MiniMax 协议发」，不会因为某个 baseURL 写法差异走到 OpenAI 分支。
	minimaxTarget := func() (*stt.Target, error) {
		key := s.sttMiniMaxKey(scope.UserID, scope.WorkspaceID)
		if key == "" {
			return nil, fmt.Errorf("stt_unavailable: MiniMax 转写未配置 API Key（设置 → 语音转写 → MiniMax）")
		}
		base := p.MiniMaxBaseURL
		if base == "" {
			base = stt.MiniMaxDefaultBaseURL
		}
		model := p.MiniMaxModel
		if model == "" {
			model = "asr-1.0"
		}
		// ★ 互斥在这里落成**模板选择**而不是两个独立开关：
		// 流式与说话人分离不能同时要（上游 400 明确拒绝，实测 (2013)）。
		// 这里给「流式 + 分离」一个明确解 —— 关掉分离，保住流式 ——
		// 而不是把两个都发出去换一个 400。
		// 为什么不反过来保分离？因为开了流式就意味着用户在等「边说边出字」，
		// 拿不到说话人只是少了标签；反之则会让用户以为在流式却拿到一次性结果。
		transport := stt.TransportTranscriptions
		if p.MiniMaxStream {
			transport = stt.TransportSSE
		}
		return &stt.Target{
			BaseURL: base, APIKey: key, Model: model,
			Provider:  stt.ProviderMiniMax,
			Transport: transport, Channel: stt.ChannelMiniMax,
			Language: p.Language,
			Label:    "MiniMax 直调", CostUSDPerHour: stt.KnownUSDPerHour(model),
		}, nil
	}

	switch channel {
	case stt.ChannelGateway:
		return gatewayTarget()
	case stt.ChannelExternal:
		return externalTarget()
	case stt.ChannelMiniMax:
		return minimaxTarget()
	}

	// auto：先网关后外部；两边的错误都要留住，好给用户一个能行动的原因。
	gwTarget, gwErr := gatewayTarget()
	if gwErr == nil {
		return gwTarget, nil
	}
	// ★ auto 里 MiniMax 排在外部**之前**。
	//
	// 理由是成本与能力：MiniMax $0.38/h、500 秒整段、SSE + 说话人分离齐全；
	// 外部默认档 gpt-4o-mini-transcribe $0.18/h 更便宜但**不支持流式**且无分离。
	// 本项目主场景是会议 —— 长录音 + 要说话人标签 + 想要即时出字，
	// 这三件事只有 MiniMax 这一档同时满足。所以当用户已经配了 MiniMax key 时，
	// 那是明确的意图信号（他专门去申请了一把），不该被一个更便宜但能力更弱的
	// 默认档抢走。
	//
	// ⚠ 代价要说清：auto 走 MiniMax 会**花钱**。所以只有「已配置 MiniMax key」
	// 才进这条路，没配时完全不影响原有行为（→ 外部/网关）。
	if mm, mmErr := minimaxTarget(); mmErr == nil {
		return mm, nil
	}
	extTarget, extErr := externalTarget()
	if extErr == nil {
		return extTarget, nil
	}
	// 三条通道都没通：优先报网关侧（更可能是用户想修的那条），并附上其它原因。
	//
	// 多段拼接时每段都带 `stt_unavailable:` 前缀，直接拼会在用户可见的中文句子中间
	// 露出第二个裸错误码（真机 2026-10-01 实测）。前端只剥**首位**前缀，
	// 所以中间那些要去掉前缀。整体前缀保留，HasPrefix 判断与前端窄口径不受影响。
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
//
// 必须用 sttOutboundHTTPClient 而不是 gatewayHTTPClient：后者的私网放行只看
// POCKET_LLM_GATEWAY_ALLOW_PRIVATE，会让 POCKET_STT_ALLOW_PRIVATE 形同虚设
// （设置页能存进去、一转写就被 dialer 拒）。详见 ssrf.go 里的说明。
func (s *Server) sttClient(timeout time.Duration) *http.Client {
	if s.sttHTTPClient != nil {
		return s.sttHTTPClient
	}
	return sttOutboundHTTPClient(timeout)
}

// SetSTTHTTPClient 注入 STT 出网客户端（供测试拒绝出网）。
//
// 存在的理由不是「为了可测性」这种套话，而是 2026-10-01 的实际事故：
// newServer 装上转写器后，server_stt_settings_test.go 里的 meeting/transcribe
// 用例顺着默认网关配置**真的出网打了生产网关**——耗时 2.3 秒、把 6 个候选的
// 503 全打了一遍，还吃到了网关限流 429。测试污染生产流量，且让单测变得随机失败。
func (s *Server) SetSTTHTTPClient(c *http.Client) { s.sttHTTPClient = c }

// ---------------------------------------------------------------- handlers

// sttTemplateViews 把注册表转成设置页要的形状。
//
// 能力标注的依据：
//   - MiniMax：SSE 与 verbose_json 分离**互斥**（实测 400 (2013)），
//     单次上限 500 秒（官方文档，与 ModelOption.MaxSeconds 一致）。
//   - OpenAI 兼容层：不支持 SSE（/audio/transcriptions 无流式），分离由上游
//     决定，所以 exclusive=false。
//   - 智谱：SSE 事件名与前两者都不同，且**本仓尚未实机验证**（见 provider_zhipu.go），
//     所以这里如实标 false，不给用户一个会失败的开关。
func sttTemplateViews() []sttTemplateView {
	ids := stt.ProviderIDs()
	out := make([]sttTemplateView, 0, len(ids))
	for _, id := range ids {
		p := stt.LookupProvider(id)
		if p == nil {
			continue
		}
		v := sttTemplateView{ID: id, Label: p.Label()}
		switch id {
		case stt.ProviderMiniMax:
			v.SupportsStream = true
			v.SupportsDiarization = true
			v.StreamAndDiarizationExclusive = true
			v.MaxSeconds = 500
		case stt.ProviderOpenAI:
			v.SupportsDiarization = true
		}
		out = append(out, v)
	}
	return out
}

func (s *Server) handleSTTConfig(w http.ResponseWriter, r *http.Request) {
	userID := s.userIDFromRequest(r)
	wsID := s.workspaceIDFromRequest(r)
	switch r.Method {
	case http.MethodGet:
		p, _ := s.loadSTTSettings(userID, wsID)
		view := sttSettingsView{
			sttSettingsPayload: p,
			HasExternalKey:     s.sttExternalKey(userID, wsID) != "",
			HasMiniMaxKey:      s.sttMiniMaxKey(userID, wsID) != "",
		}
		gw := s.ResolveGatewayForUser(userID, wsID)
		resp := sttConfigResponse{
			Settings:      view,
			Recommended:   stt.RecommendedModels(),
			GatewayBase:   gw.BaseURL,
			GatewayHasKey: strings.TrimSpace(gw.APIKey) != "",
			Templates:     sttTemplateViews(),
			ChannelHints: map[string]string{
				stt.ChannelAuto:     "优先用网关里探测通过的 ASR 模型，没有再退到 MiniMax/外部服务",
				stt.ChannelGateway:  "只用网关，网关不可用时直接报错（不静默降级）",
				stt.ChannelExternal: "只用外部 OpenAI 兼容转写服务",
				stt.ChannelMiniMax:  "只用 MiniMax 原生 API（/v1/speech_to_text）：SSE 流式 + 说话人分离 + 整段 500 秒",
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
			// MiniMaxAPIKey 独立于 externalApiKey：两把 key 是两个服务的凭据。
			// nil = 本次不改动（前端只提交它改过的字段）。
			MiniMaxAPIKey *string `json:"minimaxApiKey"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		key := normalizeExternalKeyInput(body.ExternalAPIKey)
		mmKey := normalizeMinimaxKeyInput(body.MiniMaxAPIKey)
		if err := s.saveSTTSettingsWithKeys(userID, wsID, body.sttSettingsPayload, key, mmKey); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		// 显式置空 key：允许用户主动清掉外部服务凭据。
		//
		// ★ 2026-10-08 真机验证抓到的缺陷（MiniMax 侧新增、外部侧继承）：
		//   这一段清理逻辑**写在保存之后**，而保存那一步会把字面量
		//   `"__clear__"` 当成真凭据写进存储。于是：
		//     第一次发 __clear__ → 先存进 "__clear__"，再走清理分支
		//                          （保存时读到的已是空串，所以第一次「碰巧」对）
		//     之后任何请求     → body.MiniMaxAPIKey 非 nil（字符串 "__clear__"），
		//                          清理分支仍会执行，但**保存那一步又先写回了
		//                          "__clear__"** ⇒ 永远清不掉。
		//   症状：用户点「清除 MiniMax Key」，界面上 hasMiniMaxKey 一直为 true，
		//   转写继续用那个字符串当凭据 ⇒ 401，且用户怎么点都没用。
		//
		//   修法：把哨兵**在入口就归一化成空串**（normalizeMinimaxKeyInput），
		//   于是「清空」与「普通保存」走的是同一条路径，不需要事后再补一次保存。
		//   外部 key 同理归一化 —— 它原先是靠 `key == "__clear__"` 的**后置**
		//   二次保存兜住的，属于同类脆弱写法，一并收敛。
		saved, _ := s.loadSTTSettings(userID, wsID)
		writeJSON(w, http.StatusOK, sttSettingsView{
			sttSettingsPayload: saved,
			HasExternalKey:     s.sttExternalKey(userID, wsID) != "",
			HasMiniMaxKey:      s.sttMiniMaxKey(userID, wsID) != "",
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
	var override probeOverride
	audio, filename, err := readSTTAudio(w, r, &override)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// multipart 里可能同时带一个 model 字段。
	//
	// ⚠ 条件要写成「multipart 且该字段非空」：JSON 形态下 ParseMultipartForm
	// 会报错（Content-Type 不是 multipart），但如果忽略错误继续跑，
	// r.FormValue 会去解析**已被读完的** body，拿到空值；
	// 而上面 readSTTAudio 已经把 JSON 里的覆盖项填进 override 了。
	// 所以这里只在成功时覆盖，且逐字段判空，不整体替换 ——
	// 否则 JSON 形态里没带的字段会被 FormValue 的空值抹掉。
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
		if v := strings.TrimSpace(r.FormValue("provider")); v != "" {
			override.Provider = v
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
			"provider": target.Provider,
		})
		return
	}
	// provider 一起回传：试转页要告诉用户「你刚才试的是哪条协议」，
	// 否则 MiniMax 与 OpenAI 兼容层返回一模一样的结果，无从分辨。
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "text": res.Text, "model": res.Model, "channel": res.Channel,
		"transport": res.Transport, "label": res.Label, "provider": res.Provider,
		"durationMs": res.DurationMS, "costCents": res.CostCents,
		"diarized": res.Diarized, "segments": res.Segments,
	})
}

// probeOverride 是试转端点允许的临时覆盖项。
//
// 抽成命名类型而不是内联匿名 struct：probeTarget 的签名里已经有一个匿名
// struct，handleSTTProbe 又要构造同样一份。复制两遍的类型一旦字段不同步，
// 症状是「设置页传了 provider 但服务端静默忽略」—— 一个不报错的失效。
type probeOverride struct {
	Model     string `json:"model"`
	Channel   string `json:"channel"`
	BaseURL   string `json:"baseURL"`
	Transport string `json:"transport"`
	// Provider 覆盖转写模板（设置页选「用哪个模板试转」）。
	Provider string `json:"provider"`
}

// probeTarget 用请求里的覆盖项构造试转目标；没覆盖就用已保存的设置解析。
func (s *Server) probeTarget(r *http.Request, userID, wsID string, override probeOverride) (*stt.Target, error) {
	if override.Model == "" && override.Provider == "" {
		return s.resolveSTTTarget(r.Context(), stt.Scope{UserID: userID, WorkspaceID: wsID})
	}
	channel := stt.NormalizeChannel(override.Channel)
	if channel == stt.ChannelMiniMax || stt.ProviderMiniMax == override.Provider {
		key := s.sttMiniMaxKey(userID, wsID)
		if key == "" {
			return nil, fmt.Errorf("MiniMax 转写未配置 API Key（设置 → 语音转写 → MiniMax）")
		}
		base := strings.TrimRight(strings.TrimSpace(override.BaseURL), "/")
		if base == "" {
			p, _ := s.loadSTTSettings(userID, wsID)
			base = p.MiniMaxBaseURL
		}
		if base == "" {
			base = stt.MiniMaxDefaultBaseURL
		}
		model := override.Model
		if model == "" {
			model = "asr-1.0"
		}
		if err := validateSTTOutboundURL(base); err != nil {
			return nil, err
		}
		return &stt.Target{BaseURL: base, APIKey: key, Model: model,
			Provider:  stt.ProviderMiniMax,
			Transport: stt.NormalizeTransport(override.Transport), Channel: stt.ChannelMiniMax,
			Label: "试转（MiniMax）", CostUSDPerHour: stt.KnownUSDPerHour(model)}, nil
	}
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
			Provider:  override.Provider,
			Transport: stt.NormalizeTransport(override.Transport), Channel: stt.ChannelExternal,
			Label: "试转", CostUSDPerHour: stt.KnownUSDPerHour(override.Model)}, nil
	}
	if override.Model == "" {
		// 只给了 provider、没给模型：网关/外部都要靠模型名定位，
		// 这里明确报错而不是随便挑一个 —— 试转的价值就在于「转的是我选的那个」。
		return nil, fmt.Errorf("试转需要同时指定模型（当前只给了转写模板 %q）", override.Provider)
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
//
// overrideFromJSON 是**出参**：JSON 形态里携带的试转覆盖项（模型/通道/模板）
// 只有这个函数看得到 JSON 体，而它同时又是唯一读 JSON 的地方，
// 所以顺带取出来交给调用方。传 nil 表示不需要。
func readSTTAudio(w http.ResponseWriter, r *http.Request, overrideFromJSON *probeOverride) ([]byte, string, error) {
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
	// ★ JSON 形态 {audioBase64, filename}（2026-10-06 真机复现，本分支此前只处理前两种）
	//
	//   缺陷8：前端 sttSettingsApi.probe（api/stt-settings.ts:290，被
	//   SettingsSTT.vue:566 的「试转」按钮调用）发的是
	//     JSON.stringify({ audioBase64, filename, model, channel, baseURL, transport })
	//   而本函数对非 multipart 一律「整个请求体当音频」⇒ 服务端把那段 JSON 文本
	//   当成 WAV 送进转写器，上游回 400「Param Incorrect / invalid audio format」。
	//   ⇒ 设置页的「试转」按钮 100% 失败，而这恰恰是用户判断转写好不通的唯一入口
	//   —— 用户看到的现象与「转写功能整体坏了」完全一样。
	//
	//   同仓另外三个端点（/api/stt/transcribe、/transcribe-full、/transcribe-incremental）
	//   都用 decodeBase64Audio 解析 JSON，这里是唯一漏掉的一个；复用同一个函数而不是
	//   再写一份 base64 解码，是为了让「体量上限」「换行容忍」这些口径只有一处。
	//
	//   实测（同一段 16kHz 4.6s 真实语音，同一个 /api/stt/probe）：
	//     JSON {audioBase64} 形态 → 400 invalid audio format
	//     multipart 形态         → ok=true，文本完全正确
	var payload struct {
		AudioBase64 string `json:"audioBase64"`
		Filename    string `json:"filename"`
		Model       string `json:"model"`
		Channel     string `json:"channel"`
		BaseURL     string `json:"baseURL"`
		Transport   string `json:"transport"`
		Provider    string `json:"provider"`
	}
	if err := json.Unmarshal(data, &payload); err == nil && strings.TrimSpace(payload.AudioBase64) != "" {
		audio, derr := decodeBase64Audio(payload.AudioBase64)
		if derr != nil {
			return nil, "", derr
		}
		filename := strings.TrimSpace(payload.Filename)
		if filename == "" {
			filename = "probe.wav"
		}
		// 覆盖项从 JSON 形态里取出来。必须取：前端 sttSettingsApi.probe 发的是
		// JSON（不是 multipart），所以上面那个 r.ParseMultipartForm 分支**不会执行**，
		// 不取的话用户在设置页选的模板/模型会被静默忽略，
		// 症状是「试转永远转的是我保存的那个模型」——一个不报错的失效。
		overrideFromJSON = &probeOverride{
			Model:     strings.TrimSpace(payload.Model),
			Channel:   strings.TrimSpace(payload.Channel),
			BaseURL:   strings.TrimSpace(payload.BaseURL),
			Transport: strings.TrimSpace(payload.Transport),
			Provider:  strings.TrimSpace(payload.Provider),
		}
		return audio, filename, nil
	}
	return data, audioFilenameForContentType(ct), nil
}
