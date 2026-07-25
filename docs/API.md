# OpenCode Pocket — API 文档

## 认证

所有 API 端点（除 `/healthz` 外）需要 JWT Token。
```
Authorization: Bearer <token>
```

## 端点列表

### 健康检查
```
GET /healthz
```

### RedClaw 企业集成
```
GET    /api/redclaw/health                    # 连接状态
POST   /api/redclaw/chat                      # LLM 对话
POST   /api/redclaw/knowledge/search          # 知识库检索
```

### 代码片段
```
GET    /api/snippets?language=go&search=sort  # 列表
POST   /api/snippets                          # 创建
GET    /api/snippets/{id}                     # 详情
DELETE /api/snippets/{id}                     # 删除
```

### 会议总结
```
GET    /api/meetings                          # 列表
POST   /api/meetings                          # 创建
GET    /api/meetings/{id}                     # 详情
DELETE /api/meetings/{id}                     # 删除
POST   /api/meetings/{id}/transcribe          # STT 转写
POST   /api/meetings/{id}/summarize           # AI 总结
```

### 聊天总结
```
GET    /api/chat-summaries?channel_id=xxx     # 列表
POST   /api/chat-summaries                    # 创建（含消息聚合+摘要）
GET    /api/chat-summaries/{id}               # 详情
DELETE /api/chat-summaries/{id}               # 删除
```

### 产品方案/PPT
```
POST   /api/presentations                     # 生成方案
POST   /api/presentations/render              # 渲染 PPT (html/markdown)
```

### 笔记分类
```
POST   /api/notes/classify                    # AI 分类
```

### 记账
```
GET    /api/finance                           # 列表
POST   /api/finance                           # 创建
GET    /api/finance/{id}                      # 详情
DELETE /api/finance/{id}                      # 删除
POST   /api/finance/parse                     # 语音解析
GET    /api/finance/stats                     # 统计报表
```

### 审计日志
```
GET    /api/audit/logs?tenant_id=xxx          # 查询审计日志
```

### 认证
```
POST   /api/auth/login                        # 登录获取 JWT
```

### 任务管理
```
GET    /api/tasks                             # 任务列表
POST   /api/tasks                             # 创建任务
```

### WebSocket
```
WS     /ws?token=<jwt>                        # 实时通讯
```

## 请求/响应格式

### 创建代码片段
```json
POST /api/snippets
{
  "title": "排序算法",
  "language": "go",
  "code": "sort.Ints(arr)",
  "tags": ["algorithm"]
}
```

### 创建会议并转写
```json
POST /api/meetings
{ "title": "Sprint Planning" }

POST /api/meetings/{id}/transcribe
<binary audio data>

POST /api/meetings/{id}/summarize
{}

// 响应
{
  "id": "mtg_xxx",
  "title": "Sprint Planning",
  "summary": "会议...共5条消息...",
  "key_decisions": ["决定优先开发会议总结功能"],
  "action_items": [{"task": "完成设计", "owner": "张三"}],
  "status": "done"
}
```

### 生成产品方案
```json
POST /api/presentations
{
  "type": "prd",
  "topic": "移动端AI编程助手",
  "context": "面向程序员的产品"
}

// 渲染 PPT
POST /api/presentations/render
{
  "format": "html",
  "title": "方案展示",
  "slides": [
    {"title": "概述", "content": "项目背景..."},
    {"title": "方案", "content": "技术方案..."}
  ]
}
```

### 语音记账
```json
POST /api/finance/parse
{ "text": "中午吃饭花了38块" }

// 响应
{
  "type": "expense",
  "amount": 38,
  "category": "餐饮",
  "note": "中午吃饭花了38块"
}
```

### 笔记分类
```json
POST /api/notes/classify
{ "content": "用Go实现并发安全缓存" }

// 响应
{
  "type": "tech",
  "tags": ["Go"]
}
```