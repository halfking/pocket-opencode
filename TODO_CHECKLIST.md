# 📋 OpenCode Pocket 待办事项清单

**创建时间:** 2026-06-29  
**版本:** v1.1.0  
**状态:** 基于审计报告生成

---

## 🔥 P0 - 立即执行 (本周内完成)

### 1. ✅ HTTPS 支持 (预计 2 小时)

**任务:**
- [ ] 在 56 服务器安装 certbot
- [ ] 申请 Let's Encrypt 证书
- [ ] 配置 Nginx HTTPS
- [ ] 配置 HTTP → HTTPS 重定向
- [ ] 更新 WebSocket 为 WSS
- [ ] 测试 HTTPS 访问
- [ ] 配置自动续期

**命令:**
```bash
# 在 56 服务器执行
apt install certbot python3-certbot-nginx
certbot --nginx -d pocket.kxpms.cn
certbot renew --dry-run
```

**验证:**
```bash
curl https://pocket.kxpms.cn/healthz
```

---

### 2. ⏳ 配置真实 OpenCode 实例 (预计 4 小时)

**任务:**
- [ ] 识别 kaixuan-1/2/3 上的 OpenCode 实例
- [ ] 在 NPS 中配置 HTTP tunnels
- [ ] 更新 .env 配置文件
- [ ] 重启 Pocket Backend
- [ ] 测试实例连接
- [ ] 验证会话查询功能

**配置示例:**
```bash
POCKET_OPENCODE_INSTANCES='[
  {
    "id": "opencode-kx1",
    "displayName": "OpenCode (kaixuan-1)",
    "npsClientId": 733,
    "npsHost": "opencode.kxpms.cn",
    "apiBaseURL": "https://opencode.kxpms.cn",
    "environment": "production"
  }
]'
```

**验证:**
```bash
curl http://14.103.169.56:8088/api/instances
curl "http://14.103.169.56:8088/api/sessions/?instance_id=opencode-kx1"
```

---

### 3. ⏳ 自动化备份 (预计 2 小时)

**任务:**
- [ ] 创建数据库备份脚本
- [ ] 创建配置文件备份脚本
- [ ] 配置 crontab 定时任务
- [ ] 设置备份保留策略 (7 天)
- [ ] 测试备份和恢复流程
- [ ] 文档化备份流程

**备份脚本:**
```bash
#!/bin/bash
# /data/services/opencode-pocket/scripts/backup.sh

DATE=$(date +%Y%m%d_%H%M%S)
BACKUP_DIR="/data/backups/pocket"

# 备份数据库
cp /data/services/opencode-pocket/data/pocket.sqlite \
   $BACKUP_DIR/pocket_$DATE.sqlite

# 备份配置
tar czf $BACKUP_DIR/config_$DATE.tar.gz \
   /data/services/opencode-pocket/.env \
   /etc/nginx/conf.d/00-pocket.kxpms.cn.conf

# 清理 7 天前的备份
find $BACKUP_DIR -name "*.sqlite" -mtime +7 -delete
find $BACKUP_DIR -name "*.tar.gz" -mtime +7 -delete
```

**Crontab:**
```bash
# 每天凌晨 2 点备份
0 2 * * * /data/services/opencode-pocket/scripts/backup.sh
```

---

### 4. ⏳ JWT 认证系统 (预计 1 天)

**任务:**
- [ ] 设计用户数据模型
- [ ] 实现 JWT 生成和验证
- [ ] 创建登录 API
- [ ] 创建注册 API
- [ ] 实现认证中间件
- [ ] 保护需要认证的 API
- [ ] 实现 Token 刷新
- [ ] 前端登录界面
- [ ] 前端 Token 存储
- [ ] 测试认证流程

**数据模型:**
```sql
CREATE TABLE users (
    id TEXT PRIMARY KEY,
    username TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    email TEXT,
    role TEXT DEFAULT 'user',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
```

**API 端点:**
```
POST /api/auth/register
POST /api/auth/login
POST /api/auth/refresh
GET  /api/auth/me
POST /api/auth/logout
```

---

## 🟡 P1 - 近期执行 (2 周内)

### 5. ⏳ Android APK 打包和测试 (预计 2 小时)

**任务:**
- [ ] 配置 Android 签名密钥
- [ ] 构建 Release APK
- [ ] 在实际设备上安装测试
- [ ] 测试所有核心功能
- [ ] 修复发现的问题
- [ ] 生成安装文档

**命令:**
```bash
cd frontend
npm run build
npx cap sync android
cd android
./gradlew assembleRelease
```

**测试清单:**
- [ ] 任务列表加载
- [ ] 创建任务
- [ ] 任务详情
- [ ] 附加会话
- [ ] 实时更新
- [ ] 触摸交互
- [ ] 网络错误处理

---

### 6. ⏳ 完整的模型配置 UI (预计 1 天)

**任务:**
- [ ] 创建完整的 ModelConfig.vue 组件
- [ ] Provider 列表展示
- [ ] Provider 启用/禁用
- [ ] Model 列表展示
- [ ] Model 启用/禁用
- [ ] API Key 输入框
- [ ] 测试连接按钮
- [ ] 保存配置功能
- [ ] 热加载功能
- [ ] 错误处理和提示

**组件路由:**
```
/config/:instanceId/models
```

---

### 7. ⏳ 监控和告警系统 (预计 1 天)

**任务:**
- [ ] 选择监控工具 (Prometheus/Grafana 或简单脚本)
- [ ] 配置健康检查监控
- [ ] 配置性能指标收集
- [ ] 配置 WebSocket 连接数监控
- [ ] 配置磁盘空间监控
- [ ] 配置告警通知 (邮件/webhook)
- [ ] 创建 Grafana 仪表板
- [ ] 文档化监控系统

**监控指标:**
- 服务可用性
- API 响应时间
- WebSocket 连接数
- 数据库大小
- 磁盘使用率
- 内存使用率
- CPU 使用率

---

### 8. ⏳ API 安全加固 (预计 4 小时)

**任务:**
- [ ] 实现 API 速率限制
- [ ] 添加输入验证
- [ ] 收紧 CORS 配置
- [ ] 添加安全响应头
- [ ] SQL 注入防护测试
- [ ] XSS 防护测试
- [ ] 错误消息脱敏
- [ ] 日志敏感信息过滤

**速率限制:**
```go
// 每个 IP 每分钟 60 次请求
type RateLimiter struct {
    requests map[string][]time.Time
}
```

**安全头:**
```nginx
add_header X-Frame-Options "SAMEORIGIN";
add_header X-Content-Type-Options "nosniff";
add_header X-XSS-Protection "1; mode=block";
```

---

## 🟢 P2 - 中期执行 (1 个月内)

### 9. ⏳ 任务树和并行执行 (预计 3 天)

**任务:**
- [ ] 设计任务关系数据模型
- [ ] 创建 task_relations 表
- [ ] 实现任务分解 API
- [ ] 集成 Handoff 技能
- [ ] 实现并行任务调度
- [ ] 创建任务树可视化组件
- [ ] 实现任务依赖管理
- [ ] 测试复杂任务树

---

### 10. ⏳ 双 NPS 完整支持 (预计 1 天)

**任务:**
- [ ] 实现 MultiNPSAdapter
- [ ] 实现实例聚合逻辑
- [ ] 实现去重逻辑
- [ ] 实现优先级选择
- [ ] 实现故障切换
- [ ] 测试双 NPS 场景

---

### 11. ⏳ 性能优化 (预计 2 天)

**任务:**
- [ ] 数据库索引优化
- [ ] API 响应缓存
- [ ] Gzip 压缩
- [ ] 前端 Code Splitting
- [ ] 图片懒加载
- [ ] Service Worker
- [ ] CDN 配置
- [ ] 性能测试

---

### 12. ⏳ 自动化测试 (预计 2 天)

**任务:**
- [ ] Backend 单元测试 (目标 80%)
- [ ] Frontend 单元测试
- [ ] API 集成测试
- [ ] E2E 自动化测试 (Playwright/Cypress)
- [ ] CI/CD 集成
- [ ] 测试报告

---

## 📝 待讨论的功能

### 需要确认的需求

1. **折叠屏双栏布局**
   - 需求确认: 是否有折叠屏设备？
   - 优先级: 取决于设备可用性

2. **OpenCode 远程控制**
   - 需求确认: 需要哪些控制功能？
   - 安全考虑: 重启等危险操作的权限控制

3. **池前主机集成**
   - 需求确认: 池前主机的网络可达性？
   - 技术方案: NPC 配置方案

4. **Push 通知**
   - 需求确认: 通知的场景和内容？
   - 技术方案: FCM 还是其他？

5. **任务分组**
   - 需求确认: 分组的使用场景？
   - UI 设计: 分组的展示方式？

---

## 📊 进度追踪

### 本周目标 (Week 1)
- [ ] HTTPS 支持
- [ ] 真实实例配置
- [ ] 自动化备份
- [ ] JWT 认证 (开始)

### 两周目标 (Week 2)
- [ ] JWT 认证 (完成)
- [ ] Android APK
- [ ] 模型配置 UI
- [ ] 监控系统

### 一个月目标 (Month 1)
- [ ] API 安全加固
- [ ] 任务树功能
- [ ] 性能优化
- [ ] 自动化测试

---

## 🎯 验收标准

### HTTPS 支持
- [ ] 可以通过 https://pocket.kxpms.cn 访问
- [ ] WebSocket 使用 wss:// 协议
- [ ] HTTP 自动重定向到 HTTPS
- [ ] 证书有效期 > 30 天

### JWT 认证
- [ ] 用户可以注册和登录
- [ ] Token 正确生成和验证
- [ ] 未认证用户无法访问保护的 API
- [ ] Token 刷新机制正常

### 真实实例配置
- [ ] 至少配置 1 个真实 OpenCode 实例
- [ ] 可以查询该实例的会话列表
- [ ] 可以附加会话到任务
- [ ] 实时更新正常工作

### 自动化备份
- [ ] 每天自动备份数据库
- [ ] 备份文件正确生成
- [ ] 可以从备份恢复
- [ ] 超过 7 天的备份自动删除

---

## 📞 负责人和协作

### 需要协调的工作

**HTTPS 配置:**
- 负责人: DevOps
- 协助: 需要域名解析配置

**真实实例配置:**
- 负责人: Backend 开发
- 协助: 需要 NPS 管理员配置 tunnels

**JWT 认证:**
- 负责人: Full Stack 开发
- 协助: UI/UX 设计登录界面

**监控系统:**
- 负责人: DevOps
- 协助: 需要告警渠道配置

---

## 🎊 总结

### 当前状态
```
已完成功能:     85%
待完成功能:     15%
代码质量:       优秀
文档完整性:     优秀
生产就绪度:     良好
```

### 优先级
1. 🔥 **安全** (HTTPS + JWT)
2. 🟡 **可用性** (真实实例 + 备份)
3. 🟢 **增强** (监控 + 优化 + 新功能)

### 预计完成时间
- P0 任务: 1 周
- P1 任务: 2 周
- P2 任务: 1 个月

---

**让我们开始执行！** 🚀
