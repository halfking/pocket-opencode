# OpenCode Pocket 运维指南

**文档版本**: v1.0  
**更新日期**: 2026-07-07  
**适用环境**: 开发 / 测试 / 生产

---

## 📋 目录

1. [快速开始](#快速开始)
2. [环境要求](#环境要求)
3. [部署指南](#部署指南)
4. [日常运维](#日常运维)
5. [监控告警](#监控告警)
6. [故障排查](#故障排查)
7. [性能优化](#性能优化)
8. [备份恢复](#备份恢复)

---

## 🚀 快速开始

### 一键启动开发环境

```bash
cd /path/to/opencode-pocket
./scripts/start-dev.sh
```

### 构建并部署到模拟器

```bash
./scripts/build-deploy.sh
```

### 运行API测试

```bash
./scripts/test-api.sh
```

---

## 💻 环境要求

### Backend服务器

| 组件 | 版本要求 | 备注 |
|------|---------|------|
| Go | 1.22+ | Backend编译和运行 |
| PostgreSQL | 14+ | 生产环境必需 |
| 内存 | 512MB+ | 推荐1GB+ |
| CPU | 1核+ | 推荐2核+ |

### 前端开发

| 组件 | 版本要求 | 备注 |
|------|---------|------|
| Node.js | 18+ | 前端构建 |
| npm | 9+ | 包管理 |

### Android开发

| 组件 | 版本要求 | 备注 |
|------|---------|------|
| JDK | 21 | 必须是Oracle标准版 |
| Android SDK | API 30+ | 推荐API 35 |
| Gradle | 8.14+ | 自动管理 |

---

## 📦 部署指南

### 1. Backend部署

#### 开发环境

```bash
cd backend

# 设置环境变量
export JWT_SECRET="your-secret-key"
export POCKET_DEV_AUTH=true
export POCKET_HTTP_PORT=8088

# 启动服务
./pocketd
```

#### 生产环境

```bash
# 1. 配置环境变量
cat > /etc/opencode-pocket/backend.env << EOF
JWT_SECRET=your-production-secret-key
POCKET_POSTGRES_DSN=postgres://user:pass@localhost:5432/pocket
POCKET_HTTP_PORT=8088
POCKET_DEV_AUTH=false
POCKET_INSTANCE_DISCOVERY_BASE_URL=https://your-discovery-api
POCKET_INSTANCE_DISCOVERY_AUTH_TOKEN=your-token
EOF

# 2. 创建systemd服务
cat > /etc/systemd/system/opencode-pocket.service << EOF
[Unit]
Description=OpenCode Pocket Backend
After=network.target postgresql.service

[Service]
Type=simple
User=pocket
EnvironmentFile=/etc/opencode-pocket/backend.env
ExecStart=/usr/local/bin/pocketd
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
EOF

# 3. 启动服务
sudo systemctl daemon-reload
sudo systemctl enable opencode-pocket
sudo systemctl start opencode-pocket
```

### 2. 前端部署

#### 构建生产版本

```bash
cd frontend

# 构建
npm run build

# 同步到Android
npx cap sync android

# 构建APK (Debug)
cd android
JAVA_HOME=/Library/Java/JavaVirtualMachines/jdk-21.jdk/Contents/Home \
  ./gradlew assembleDebug

# 构建APK (Release)
JAVA_HOME=/Library/Java/JavaVirtualMachines/jdk-21.jdk/Contents/Home \
  ./gradlew assembleRelease
```

#### APK签名（生产环境）

```bash
# 1. 生成密钥库
keytool -genkey -v -keystore pocket-release-key.keystore \
  -alias pocket -keyalg RSA -keysize 2048 -validity 10000

# 2. 配置gradle.properties
echo "POCKET_RELEASE_STORE_FILE=pocket-release-key.keystore" >> gradle.properties
echo "POCKET_RELEASE_STORE_PASSWORD=your_password" >> gradle.properties
echo "POCKET_RELEASE_KEY_ALIAS=pocket" >> gradle.properties
echo "POCKET_RELEASE_KEY_PASSWORD=your_password" >> gradle.properties

# 3. 构建签名版本
./gradlew assembleRelease
```

---

## 🔧 日常运维

### 服务管理

```bash
# 查看服务状态
sudo systemctl status opencode-pocket

# 启动服务
sudo systemctl start opencode-pocket

# 停止服务
sudo systemctl stop opencode-pocket

# 重启服务
sudo systemctl restart opencode-pocket

# 查看日志
sudo journalctl -u opencode-pocket -f
```

### 健康检查

```bash
# Backend健康检查
curl http://localhost:8088/healthz

# 实例列表
curl http://localhost:8088/api/instances

# WebSocket连接测试
wscat -c ws://localhost:8088/ws?token=YOUR_TOKEN
```

### 日志管理

```bash
# 查看Backend日志
tail -f logs/pocketd.log

# 查看Android应用日志
adb logcat | grep -E "opencode|pocket|Capacitor"

# 日志轮转配置
cat > /etc/logrotate.d/opencode-pocket << EOF
/var/log/opencode-pocket/*.log {
    daily
    rotate 30
    compress
    delaycompress
    notifempty
    create 0640 pocket pocket
    postrotate
        systemctl reload opencode-pocket > /dev/null 2>&1 || true
    endscript
}
EOF
```

---

## 📊 监控告警

### 关键指标

| 指标 | 阈值 | 说明 |
|------|------|------|
| CPU使用率 | >80% | Backend负载过高 |
| 内存使用 | >1GB | 可能内存泄漏 |
| 响应时间 | >1s | API性能下降 |
| WebSocket连接数 | >1000 | 连接数过多 |
| 错误率 | >5% | 服务异常 |

### Prometheus监控配置

```yaml
# prometheus.yml
scrape_configs:
  - job_name: 'opencode-pocket'
    static_configs:
      - targets: ['localhost:8088']
    metrics_path: '/metrics'
```

### 告警规则

```yaml
# alerts.yml
groups:
  - name: opencode_pocket
    rules:
      - alert: HighErrorRate
        expr: rate(http_requests_total{code=~"5.."}[5m]) > 0.05
        for: 5m
        annotations:
          summary: "High error rate detected"
          
      - alert: SlowResponse
        expr: http_request_duration_seconds > 1
        for: 5m
        annotations:
          summary: "API response time > 1s"
```

---

## 🔍 故障排查

### 常见问题

#### 1. Backend启动失败

**症状**: pocketd进程无法启动

**排查步骤**:
```bash
# 检查端口占用
lsof -i :8088

# 检查配置文件
env | grep POCKET

# 查看错误日志
tail -100 logs/pocketd.log

# 检查数据库连接
psql -h localhost -U pocket -d pocket
```

**解决方案**:
- 确保端口8088未被占用
- 检查环境变量配置
- 确认PostgreSQL服务运行
- 验证JWT_SECRET已设置

#### 2. WebSocket连接失败

**症状**: 移动应用无法建立WebSocket连接

**排查步骤**:
```bash
# 检查Backend WebSocket端点
curl -i http://localhost:8088/ws

# 测试token验证
wscat -c "ws://localhost:8088/ws?token=YOUR_TOKEN"

# 查看Backend日志
grep "WebSocket" logs/pocketd.log
```

**解决方案**:
- 确认token有效且未过期
- 检查requireAuth中间件配置
- 验证混合内容配置（Android）

#### 3. APK构建失败

**症状**: Gradle构建报错

**排查步骤**:
```bash
# 检查JDK版本
java -version
echo $JAVA_HOME

# 清理缓存
./gradlew clean

# 查看详细错误
./gradlew assembleDebug --stacktrace
```

**解决方案**:
- 确保使用Oracle JDK 21（非GraalVM）
- 清理Gradle缓存: `rm -rf ~/.gradle/caches`
- 检查AndroidX依赖版本

#### 4. 应用崩溃

**症状**: Android应用启动后立即崩溃

**排查步骤**:
```bash
# 查看崩溃日志
adb logcat | grep "FATAL\|crash"

# 检查WebView配置
adb shell dumpsys webview

# 验证APK签名
apksigner verify --verbose app-debug.apk
```

**解决方案**:
- 检查MainActivity配置
- 验证Capacitor插件加载
- 确认Backend可访问

---

## ⚡ 性能优化

### Backend优化

```go
// 1. 启用连接池
db, err := sql.Open("postgres", dsn)
db.SetMaxOpenConns(25)
db.SetMaxIdleConns(5)
db.SetConnMaxLifetime(5 * time.Minute)

// 2. 添加缓存
cache := cache.New(5*time.Minute, 10*time.Minute)

// 3. 启用gzip压缩
e.Use(middleware.Gzip())

// 4. 设置超时
server := &http.Server{
    ReadTimeout:  10 * time.Second,
    WriteTimeout: 10 * time.Second,
    IdleTimeout:  120 * time.Second,
}
```

### 前端优化

```javascript
// 1. 代码分割
const SessionView = () => import('./SessionView.vue')

// 2. 图片优化
<img loading="lazy" src="..." />

// 3. API请求节流
import { debounce } from 'lodash-es'
const search = debounce(searchAPI, 300)

// 4. WebSocket消息批处理
const batchMessages = (messages, interval = 100) => {
  // 批处理逻辑
}
```

### Android优化

```java
// 1. WebView缓存
webView.getSettings().setCacheMode(WebSettings.LOAD_DEFAULT);
webView.getSettings().setDomStorageEnabled(true);

// 2. 硬件加速
webView.setLayerType(View.LAYER_TYPE_HARDWARE, null);

// 3. 图片加载优化
webView.getSettings().setLoadsImagesAutomatically(true);
webView.getSettings().setBlockNetworkImage(false);
```

---

## 💾 备份恢复

### 数据库备份

```bash
# 自动备份脚本
cat > /usr/local/bin/backup-pocket-db.sh << 'EOF'
#!/bin/bash
BACKUP_DIR="/var/backups/opencode-pocket"
DATE=$(date +%Y%m%d_%H%M%S)
BACKUP_FILE="$BACKUP_DIR/pocket_$DATE.sql.gz"

mkdir -p "$BACKUP_DIR"
pg_dump -U pocket -d pocket | gzip > "$BACKUP_FILE"

# 保留最近30天的备份
find "$BACKUP_DIR" -name "pocket_*.sql.gz" -mtime +30 -delete

echo "Backup completed: $BACKUP_FILE"
EOF

chmod +x /usr/local/bin/backup-pocket-db.sh

# 配置cron定时任务
echo "0 2 * * * /usr/local/bin/backup-pocket-db.sh" | crontab -
```

### 数据库恢复

```bash
# 恢复指定备份
gunzip < /var/backups/opencode-pocket/pocket_20260707_020000.sql.gz | \
  psql -U pocket -d pocket

# 恢复最新备份
LATEST_BACKUP=$(ls -t /var/backups/opencode-pocket/pocket_*.sql.gz | head -1)
gunzip < "$LATEST_BACKUP" | psql -U pocket -d pocket
```

### 配置备份

```bash
# 备份配置文件
tar -czf config_backup_$(date +%Y%m%d).tar.gz \
  /etc/opencode-pocket/ \
  backend/.env.production \
  frontend/capacitor.config.ts

# 恢复配置
tar -xzf config_backup_20260707.tar.gz -C /
```

---

## 📞 联系支持

### 问题报告

遇到问题时，请提供以下信息：
1. 错误描述和复现步骤
2. Backend日志 (`logs/pocketd.log`)
3. 系统环境 (OS, Go版本, Node版本)
4. 配置文件 (脱敏后)

### 文档资源

- 技术文档: `docs/`
- API文档: `docs/API.md`
- 架构文档: `docs/ARCHITECTURE.md`
- 测试报告: `*.md`

---

**文档维护**: Kiro AI  
**最后更新**: 2026-07-07
