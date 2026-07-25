# OpenCode Pocket — 部署文档

## 环境要求

- **Go**: 1.22+
- **Node.js**: 18+
- **JDK**: 21 (Android 构建)
- **Xcode**: 15+ (iOS 构建)
- **PostgreSQL**: 16 (可选，笔记模块需要)

## 环境变量

| 变量 | 说明 | 默认值 | 必填 |
|------|------|--------|------|
| `POCKET_HTTP_PORT` | HTTP 服务端口 | `8088` | 否 |
| `JWT_SECRET` | JWT 签名密钥 | — | **是** |
| `POCKET_DEV_AUTH` | 开发模式跳过认证 | `false` | 否 |
| `POCKET_GROQ_API_KEY` | Groq Whisper API Key | — | 否（STT 用） |
| `POCKET_REDCLAW_BASE_URL` | RedClaw 企业后端地址 | — | 否 |
| `POCKET_REDCLAW_SECRET` | RedClaw 共享密钥 | — | 否 |
| `POCKET_REDCLAW_TENANT_ID` | 默认租户 ID | `default` | 否 |
| `POCKET_DATABASE_URL` | PostgreSQL 连接串 | — | 否 |

## 快速启动（开发模式）

```bash
# 1. 启动后端
cd backend
export JWT_SECRET="your-secret-key"
export POCKET_DEV_AUTH=true
go run ./cmd/pocketd

# 后端将在 http://localhost:8088 启动
```

## Docker 部署

```bash
# 构建镜像
docker build -t opencode-pocket .

# 运行
docker run -p 8088:8088 \
  -e JWT_SECRET="your-secret" \
  -e POCKET_DEV_AUTH=true \
  opencode-pocket
```

## 移动端构建

### Android

```bash
cd frontend
npm install
npm run build
npx cap sync android
cd android && ./gradlew assembleDebug

# APK 位于: android/app/build/outputs/apk/debug/app-debug.apk
```

### iOS

```bash
cd frontend
npm install
npm run build
npx cap sync ios
npx cap open ios

# 在 Xcode 中选择模拟器或真机运行
```

## 生产部署建议

1. **安全**: 始终设置 `JWT_SECRET` 为强随机字符串
2. **HTTPS**: 使用反向代理（Nginx/Caddy）终止 TLS
3. **数据库**: 配置 PostgreSQL 确保持久化
4. **监控**: 启用审计日志 (`/api/audit/logs`)
5. **备份**: 定期备份 SQLite/PostgreSQL 数据

## 故障排查

### 后端无法启动
```bash
# 检查端口占用
lsof -i :8088

# 检查环境变量
echo $JWT_SECRET
```

### STT 转写失败
```bash
# 确认 Groq API Key 已配置
echo $POCKET_GROQ_API_KEY

# 测试 API 连通性
curl -X POST http://localhost:8088/api/meetings
```

### RedClaw 连接失败
```bash
# 确认 RedClaw 网关运行中
curl http://localhost:8092/health

# 确认 Pocket 配置正确
curl http://localhost:8088/api/redclaw/health
```