# 🦞 龙虾后端任务清单 — kxmemory API 端点实现 + llm-gateway 对接

**日期**: 2026-07-02
**作者**: pocketd 团队
**目标**: 为 kxmemory 开发者提供精确的 API 端点实现规范，pocketd 已集成客户端（`backend/internal/kxmemory/client.go`），等待 kxmemory 服务端实现。

> **使用方式**: 把下面对应的"提示词"部分复制给 kxmemory 项目的 AI agent 或开发者，即可生成对应端点。

---

## 一、kxmemory 需实现的 3 个 API 端点

pocketd 的 Go 客户端（`backend/internal/kxmemory/client.go`）已定义了请求/响应类型。kxmemory（FastAPI）需要实现以下端点，**类型签名必须与 Go 客户端匹配**。

### 端点 1: `POST /v1/notes/classify` — 笔记 AI 分类

### 端点 2: `POST /v1/emails/classify` — 邮件批量分类

### 端点 3: `POST /v1/emails/daily-summary` — 每日邮件总结

---

## 二、通用规范

| 维度 | 规范 |
|------|------|
| 框架 | FastAPI + Pydantic |
| 数据格式 | JSON (UTF-8) |
| 认证 | `Authorization: Bearer <jwt>`（可选，pocketd 传 JWT_SECRET） |
| LLM 调用 | kxmemory 内部调 llm-gateway-go（`LLM_GATEWAY_URL` + `LLM_GATEWAY_API_KEY`） |
| 超时 | 服务端 25s（pocketd 客户端 30s） |
| 错误格式 | `{"error": "string", "code": "invalid_request\|internal", "detail": {}}` |
| 日志 | 只记 `{ts, endpoint, user_id, latency_ms, tokens}`，**绝不记请求内容** |

---

## 三、实现提示词（复制给 kxmemory 项目的 agent）

### 提示词 1: 笔记分类端点

```
请在 kxmemory FastAPI 项目中实现 POST /v1/notes/classify 端点。

功能：接收一条笔记的文本片段，调用 LLM 进行分类、SSOT 冲突检测、smart_links 关联、todo 提取。

请求体（Pydantic model）：
```python
class ClassifyNoteRequest(BaseModel):
    content: str           # 笔记文本片段（前 ~2000 字）
    title: str | None = None
    content_type: str | None = None  # voice / text
    domain: str | None = None        # work / study / life / idea
    tags: list[str] = []
```

响应体：
```python
class Classification(BaseModel):
    domain: str            # work / study / life / idea
    category: str          # meeting / plan / idea / log / reference / ...
    tags: list[str]
    suggested_title: str
    confidence: float      # 0.0 - 1.0

class SmartLink(BaseModel):
    target_id: str
    link_type: str         # references / updates / contradicts / complements / related_to
    confidence: float

class ExtractedTodo(BaseModel):
    text: str
    due_date: str | None = None   # YYYY-MM-DD
    priority: str | None = None   # low / medium / high / urgent

class SSOTConflict(BaseModel):
    existing_note_id: str
    conflict_type: str     # contradiction / update / duplicate
    snippet: str
    confidence: float

class ClassifyNoteResponse(BaseModel):
    status: str            # "success" 或 "conflict_detected"
    classification: Classification
    smart_links: list[SmartLink] = []
    todos: list[ExtractedTodo] = []
    ssot_conflicts: list[SSOTConflict] = []
```

LLM prompt 设计要点：
1. 分类：用 system prompt 定义 domain/category 分类体系，让 LLM 返回 JSON
2. Todo 提取：prompt 中要求识别 "需要在 X 时间前做 Y" 的句子
3. SSOT 检测：对比同 domain 的现有笔记（用 embedding 余弦相似度 > 0.85 检索），让 LLM 判断是否冲突
4. confidence：让 LLM 自评分类置信度

LLM 调用方式：kxmemory 通过 httpx 调 llm-gateway-go 的 OpenAI 兼容接口：
```python
# 环境变量 LLM_GATEWAY_URL + LLM_GATEWAY_API_KEY
import httpx
resp = httpx.post(f"{LLM_GATEWAY_URL}/v1/chat/completions",
    headers={"Authorization": f"Bearer {LLM_GATEWAY_API_KEY}"},
    json={"model": "gpt-4o-mini", "messages": [...], "response_format": {"type": "json_object"}},
    timeout=20)
```

请实现 router + service + prompt template 三层。prompt 用 Jinja2 模板存 prompts/notes_classify.j2。
```

---

### 提示词 2: 邮件批量分类端点

```
请在 kxmemory FastAPI 项目中实现 POST /v1/emails/classify 端点。

功能：接收一批邮件的 snippet（前 ~500 字），批量调用 LLM 分类。

请求体：
```python
class EmailForClassification(BaseModel):
    email_id: str
    subject: str
    snippet: str           # 正文前 ~500 字（不含完整正文）
    from_address: str
    from_name: str | None = None

class ClassifyEmailsRequest(BaseModel):
    emails: list[EmailForClassification]
```

响应体：
```python
class EmailClassificationResult(BaseModel):
    email_id: str
    category: str          # work / bill / notification / personal / marketing / spam
    importance: str        # high / medium / low
    summary: str           # 一句话总结
    suggested_action: str | None = None  # 如 "回复确认" / "归档" / "忽略"

class ClassifyEmailsResponse(BaseModel):
    results: list[EmailClassificationResult]
```

分类体系：
- work: 工作邮件（同事/客户/项目）
- bill: 账单/支付
- notification: 系统通知（GitHub/监控/CI）
- personal: 私人
- marketing: 营销
- spam: 垃圾

重要度规则：
- high: 需要回复/有 deadline/来自重要联系人
- medium: 有用但非紧急
- low: 通知类/营销

批量优化：如果邮件数 > 10，分批调 LLM（每批 10 封），用一条 prompt 同时分类多封（省 token）。

请实现 router + service。prompt 存 prompts/emails_classify.j2。
```

---

### 提示词 3: 每日邮件总结端点

```
请在 kxmemory FastAPI 项目中实现 POST /v1/emails/daily-summary 端点。

功能：对一天的所有邮件生成结构化日报。

请求体：
```python
class DailySummaryRequest(BaseModel):
    date: str                    # YYYY-MM-DD
    emails: list[EmailForClassification]  # 当天所有邮件（复用分类的输入类型）
```

响应体：
```python
class CategoryBreakdown(BaseModel):
    category: str
    count: int
    snippet: str          # 该类别的代表性摘要

class DailySummaryResponse(BaseModel):
    date: str
    summary: str          # 2-3 段自然语言总结
    breakdown: list[CategoryBreakdown]
    todos: list[ExtractedTodo]   # 复用笔记的 ExtractedTodo
```

Prompt 要点：
1. 按类别分组统计
2. 高重要度邮件单独列出
3. 提取需要行动的 todo（回复/deadline）
4. 总结语气简洁，聚焦"今天需要关注的"

请实现 router + service + prompt templates/emails_daily_summary.j2。
```

---

## 四、llm-gateway-go 对接配置说明

### pocketd 侧配置（已完成）

pocketd 支持两种 LLM 路由模式（自动选择）：

| 模式 | 触发条件 | 说明 |
|------|---------|------|
| 企业网关 | `POCKET_LLM_GATEWAY_URL` + `POCKET_LLM_GATEWAY_API_KEY` 均设置 | 代理到 llm-gateway-go（流量治理/审计/限流）|
| 直连 | 上述未设置，但 `POCKET_LLM_API_KEY` 设置 | 直接转发 OpenAI/Groq |

### .env 配置示例（pocketd 部署到 184 服务器）

```bash
# === llm-gateway-go 企业网关（推荐生产环境）===
POCKET_LLM_GATEWAY_URL=http://llm-gateway.kxpms.cn     # 或 http://localhost:8080
POCKET_LLM_GATEWAY_API_KEY=pocketd-tenant-key          # llm-gateway 签发的租户 key

# === 嵌入（llm-gateway 未配置时用直连）===
POCKET_EMBED_MODEL=text-embedding-3-small

# === kxmemory AI 编排 ===
POCKET_KXMEMORY_BASE_URL=http://kxmemory.kxpms.cn      # 或 http://localhost:8000
POCKET_JWT_SECRET=<共享密钥>                            # kxmemory 用相同 secret 校验 JWT
```

### kxmemory 侧配置

kxmemory 调 llm-gateway-go（OpenAI 兼容）：

```bash
# kxmemory 的 .env
LLM_GATEWAY_URL=http://llm-gateway.kxpms.cn
LLM_GATEWAY_API_KEY=kxmemory-tenant-key
LLM_MODEL_DEFAULT=gpt-4o-mini          # 分类用（快+便宜）
LLM_MODEL_ADVANCED=gpt-4o              # 总结/复杂推理用
```

### llm-gateway-go 侧配置

在 llm-gateway-go 管理界面为 pocketd 和 kxmemory 分别创建租户 API key：
- `pocketd-tenant`：用于 /api/embed、/api/llm/chat 代理
- `kxmemory-tenant`：用于分类/总结 LLM 调用

---

## 五、验证检查清单

kxmemory 端点实现后，验证：

- [ ] `POST /v1/notes/classify` 返回 `status: success` + classification JSON
- [ ] `POST /v1/emails/classify` 返回 `results` 数组
- [ ] `POST /v1/emails/daily-summary` 返回 `summary` + `breakdown` + `todos`
- [ ] kxmemory 日志不含请求 content/snippet（隐私）
- [ ] kxmemory 调 llm-gateway-go 成功（检查 Authorization header）
- [ ] pocketd 启动时日志显示 `kxmemory AI orchestrator enabled`
- [ ] 创建笔记后 pocketd 日志显示 `[kxmemory] note xxx classified`

pocketd 集成验证：
```bash
# 设置环境变量启动 pocketd
export POCKET_KXMEMORY_BASE_URL=http://localhost:8000
export POCKET_LLM_GATEWAY_URL=http://localhost:8080
export POCKET_LLM_GATEWAY_API_KEY=test-key
./pocketd

# 创建笔记（触发异步分类）
curl -X POST http://localhost:8088/api/notes \
  -H "Content-Type: application/json" \
  -d '{"content":"明天下午3点和产品团队开评审会","contentType":"text"}'

# 查看日志确认分类
# 预期: [kxmemory] note xxx classified: domain=work category=meeting
```
