# Phase 2 API 快速参考指南

## 🚀 快速开始

### 1. 生成Token
```bash
cd backend
export IDENTITY_SHARED_SECRET="92LKtn61FehqIs8dy2TxzEnTcO8CWbtkE26IE1+/60r416AaYizg+4ziLsXFeBpL"
TOKEN=$(go run cmd/gentoken2/main.go)
echo $TOKEN
```

### 2. 验证Token
```bash
go run cmd/verifytoken/main.go "$TOKEN"
```

### 3. 测试API
```bash
# 健康检查
curl -H "Authorization: Bearer $TOKEN" \
  http://localhost:8088/api/advisor/health

# 创建会议
curl -X POST \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"title":"Test Meeting"}' \
  http://localhost:8088/api/meetings
```

---

## 📍 API 端点

### 健康检查
```bash
GET /api/advisor/health    # AI顾问服务状态
GET /api/diarize/health    # 说话人分离服务状态
GET /healthz               # 基础健康检查
```

### 会议管理
```bash
POST /api/meetings                      # 创建会议
GET  /api/meetings                      # 列出会议
GET  /api/meetings/{id}                 # 获取会议详情
DELETE /api/meetings/{id}               # 删除会议
```

### Phase 2 功能
```bash
POST /api/meetings/{id}/advisor         # AI会议顾问
POST /api/meetings/{id}/diarize         # 说话人分离
POST /api/meetings/{id}/multilingual    # 多语言转写
POST /api/meetings/{id}/taskgen         # 任务生成
```

---

## 🔑 认证配置

### 服务器环境变量
```bash
IDENTITY_SHARED_SECRET=92LKtn61FehqIs8dy2TxzEnTcO8CWbtkE26IE1+/60r416AaYizg+4ziLsXFeBpL
IDENTITY_ISSUER_ALLOWLIST=redclaw,memora,llm-gateway,pocket,acc
POCKET_AUTH_LEGACY_ONLY=true
POCKET_POSTGRES_DSN=postgresql://[REDACTED:secret]@localhost:15432/openpocket_test?sslmode=disable
```

### Token Claims
```json
{
  "iss": "pocket",
  "sub": "testuser",
  "user_id": "testuser",
  "aud": "pocket-api",
  "tenant_id": "ws_test",
  "roles": ["admin"],
  "scope": "admin",
  "workspace_id": "ws_test"
}
```

---

## 🧪 测试示例

### Advisor测试
```bash
MEETING_ID="mtg_1794669772431887000_1"
curl -X POST \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"analysis_type":"summary"}' \
  "http://localhost:8088/api/meetings/$MEETING_ID/advisor"

# 预期响应（kxmemory未配置时）:
# {"enabled":false,"suggestions":[]}
```

### Diarize测试
```bash
curl -X POST \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"audio_url":"test.wav"}' \
  "http://localhost:8088/api/meetings/$MEETING_ID/diarize"

# 预期响应（pyannote未配置时）:
# {"error":"diarization service not configured","ok":false}
```

---

## 🛠️ 工具位置

```
backend/cmd/
├── gentoken2/main.go      # Token生成器（正确实现）
└── verifytoken/main.go    # Token验证器

internal/server/
├── server_advisor.go      # AI顾问实现
├── server_diarization.go  # 说话人分离实现
├── server_multilang.go    # 多语言转写实现
└── server_meeting_taskgen.go # 任务生成实现
```

---

## 🐛 故障排查

### Token被拒绝（401）
```bash
# 1. 检查secret长度
echo -n "$IDENTITY_SHARED_SECRET" | wc -c  # 应该是64

# 2. 验证token格式
go run cmd/verifytoken/main.go "$TOKEN"

# 3. 检查服务器环境变量
ps eww $(pgrep pocketd) | tr ' ' '\n' | grep IDENTITY
```

### 服务无响应
```bash
# 检查服务状态
ps aux | grep pocketd
curl http://localhost:8088/healthz

# 检查日志
tail -f /tmp/pocketd.log

# 重启服务
pkill pocketd
cd backend && ./pocketd &
```

### 数据库连接失败
```bash
# 检查PostgreSQL
docker ps | grep postgres

# 测试连接
docker exec llm-gateway-week0-test-postgres-1 \
  psql -U test openpocket_test -c "SELECT 1"
```

---

## 📊 预期响应

### 成功响应
```json
// 会议创建
{
  "id": "mtg_1794669772431887000_1",
  "owner_id": "testuser",
  "workspace_id": "ws_test",
  "title": "Test Meeting",
  "status": "active"
}

// Advisor（服务未配置）
{
  "enabled": false,
  "suggestions": []
}

// Diarize（服务未配置）
{
  "error": "diarization service not configured",
  "ok": false
}
```

### 错误响应
```json
// 认证失败
{
  "code": "unauthenticated",
  "error": "invalid or expired token"
}

// 会议不存在
{
  "error": "meeting not found"
}

// 无效请求
{
  "error": "invalid request body: ..."
}
```

---

## 📚 相关文档

- 完整报告: `docs/PHASE2_VERIFICATION_COMPLETE.md`
- 原始规划: `docs/PHASE2_COMPLETION_STATUS_AND_NEXT_STEPS.md`
- 认证实现: `internal/auth/identity.go`
- API路由: `internal/server/server.go`

---

**更新时间**: 2026-11-14 23:30  
**服务PID**: 6846  
**服务端口**: 8088
