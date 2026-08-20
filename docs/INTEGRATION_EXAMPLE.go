// ============================================================================
// OpenCode 管理系统 - 完整集成示例
// ============================================================================

// ============================================================================
// 1. 后端集成示例 (backend/cmd/pocketd/main.go)
// ============================================================================

package main

import (
	"context"
	"database/sql"
	"log"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/adapter"
	"github.com/halfking/pocket-opencode/backend/internal/config"
	"github.com/halfking/pocket-opencode/backend/internal/opencode"
	"github.com/halfking/pocket-opencode/backend/internal/registry"
	"github.com/halfking/pocket-opencode/backend/internal/server"
	_ "github.com/lib/pq"
)

func main() {
	// 1. 加载配置
	cfg := config.Load()

	// 2. 连接数据库
	db, err := sql.Open("postgres", cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	// 3. 初始化数据库表（包括 OpenCode 相关表）
	if err := initDatabase(db); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	// 4. 创建适配器和服务
	npsAdapter := adapter.NewStaticNPSAdapter()
	opencodeAdapter := adapter.NewOpenCodeHTTPAdapter(5000) // 5秒超时
	configAdapter := adapter.NewOpenCodeConfigHTTPAdapter(5000)

	// 5. 创建 Registry
	reg := registry.NewRegistry()
	
	// 6. 注册默认实例（可选，也可以通过 API 动态注册）
	registerDefaultInstances(reg)

	// 7. 创建 OpenCode 历史存储
	historyStore := opencode.NewPostgresHistoryStore(db)
	log.Println("✅ OpenCode 历史存储已创建")

	// 8. 创建 OpenCode 管理器
	opencodeManager := opencode.NewManager(
		reg,
		opencodeAdapter,
		historyStore,
	)
	log.Println("✅ OpenCode 管理器已创建")

	// 9. 启动状态监控（每 30 秒轮询一次）
	ctx := context.Background()
	go opencodeManager.StartStatusMonitoring(ctx, 30*time.Second)
	log.Println("✅ OpenCode 状态监控已启动（30秒间隔）")

	// 10. 创建 Server（传入 opencodeManager）
	srv := server.New(
		cfg,
		npsAdapter,
		opencodeAdapter,
		nil, // taskStore - 可选
		reg,
		configAdapter,
		nil, // notesStore - 可选
		nil, // emailStore - 可选
		nil, // vaultStore - 可选
		nil, // transcriber - 可选
		nil, // mcpClient - 可选
		nil, // embedder - 可选
		nil, // llm - 可选
		nil, // kxmemory - 可选
		opencodeManager, // ⭐ OpenCode 管理器
	)

	log.Println("✅ Server 已创建，OpenCode 管理功能已启用")

	// 11. 启动 HTTP 服务器
	log.Printf("🚀 服务器启动在 %s", cfg.ServerAddr)
	if err := http.ListenAndServe(cfg.ServerAddr, srv.Handler()); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

// initDatabase 初始化数据库表
func initDatabase(db *sql.DB) error {
	log.Println("🔧 初始化数据库表...")

	// 初始化 OpenCode 相关表
	if err := opencode.InitSchema(db); err != nil {
		return fmt.Errorf("init opencode schema failed: %w", err)
	}
	log.Println("✅ OpenCode 数据库表已创建：")
	log.Println("   - opencode_sessions")
	log.Println("   - opencode_session_history")

	// 其他表初始化...
	// if err := initOtherTables(db); err != nil {
	//     return err
	// }

	return nil
}

// registerDefaultInstances 注册默认实例（示例）
func registerDefaultInstances(reg *registry.Registry) {
	// 示例：注册两个 OpenCode 实例
	instances := []struct {
		ID          string
		DisplayName string
		BaseURL     string
		NPSClientID int
	}{
		{
			ID:          "kaixuan-71",
			DisplayName: "开发机 71",
			BaseURL:     "http://14.103.169.71:3000",
			NPSClientID: 1,
		},
		{
			ID:          "kaixuan-252",
			DisplayName: "测试机 252",
			BaseURL:     "http://14.103.169.252:3000",
			NPSClientID: 2,
		},
	}

	for _, inst := range instances {
		if err := reg.RegisterInstance(&model.PocketInstance{
			ID:           inst.ID,
			DisplayName:  inst.DisplayName,
			NPSClientID:  inst.NPSClientID,
			Environment:  "development",
			Capabilities: []string{"session", "summary"},
			Health:       "healthy",
		}); err != nil {
			log.Printf("⚠️ 注册实例 %s 失败: %v", inst.ID, err)
		} else {
			log.Printf("✅ 已注册实例: %s", inst.DisplayName)
		}
	}
}

// ============================================================================
// 2. 前端集成示例 (frontend/src/main.ts 或 app 初始化)
// ============================================================================

/*
// main.ts
import { createApp } from 'vue'
import { createPinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'
import App from './App.vue'

// 导入 OpenCode 路由
import { opencodeRoutes } from './features/opencode/routes'

// 其他路由
const routes = [
  {
    path: '/',
    component: () => import('./views/Home.vue')
  },
  {
    path: '/instances',
    component: () => import('./features/instances/InstanceListView.vue')
  },
  // ... 其他路由

  // ⭐ 添加 OpenCode 路由
  ...opencodeRoutes
]

const router = createRouter({
  history: createWebHistory(),
  routes
})

const app = createApp(App)
app.use(createPinia())
app.use(router)
app.mount('#app')
*/

// ============================================================================
// 3. 在现有界面添加入口 (frontend/src/features/instances/InstanceListView.vue)
// ============================================================================

/*
<template>
  <div class="instance-list-view">
    <!-- 现有内容 -->
    <div class="top-bar">
      <h1>实例管理</h1>
      
      <!-- ⭐ 添加 OpenCode Hub 入口按钮 -->
      <button class="opencode-hub-btn" @click="$router.push('/opencode/hub')">
        🎯 OpenCode 管理中心
      </button>
    </div>

    <!-- 现有实例列表 -->
    <!-- ... -->
  </div>
</template>

<style scoped>
.opencode-hub-btn {
  padding: 10px 20px;
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
  color: white;
  border: none;
  border-radius: 8px;
  font-size: 14px;
  font-weight: 600;
  cursor: pointer;
  transition: all 0.3s;
}

.opencode-hub-btn:hover {
  transform: translateY(-2px);
  box-shadow: 0 4px 12px rgba(102, 126, 234, 0.4);
}
</style>
*/

// ============================================================================
// 4. 测试连接 (test.sh)
// ============================================================================

/*
#!/bin/bash
# test-opencode-integration.sh

echo "🧪 测试 OpenCode 集成..."

# 测试后端健康检查
echo -n "1. 测试后端健康检查... "
if curl -s http://localhost:9010/healthz > /dev/null; then
    echo "✅"
else
    echo "❌ 后端未运行"
    exit 1
fi

# 测试实例列表 API
echo -n "2. 测试实例列表 API... "
RESPONSE=$(curl -s http://localhost:9010/api/opencode/instances)
if [ $? -eq 0 ]; then
    echo "✅"
    echo "   响应: $RESPONSE"
else
    echo "❌"
    exit 1
fi

# 测试会话列表 API
echo -n "3. 测试会话列表 API... "
RESPONSE=$(curl -s "http://localhost:9010/api/opencode/sessions?instance_id=kaixuan-71")
if [ $? -eq 0 ]; then
    echo "✅"
    echo "   响应: $RESPONSE"
else
    echo "❌"
fi

# 测试数据库表
echo -n "4. 检查数据库表... "
TABLE_COUNT=$(psql -U pocket_user -d pocket_db -t -c "SELECT COUNT(*) FROM information_schema.tables WHERE table_name LIKE 'opencode_%';")
if [ "$TABLE_COUNT" -ge 2 ]; then
    echo "✅ (找到 $TABLE_COUNT 张表)"
else
    echo "❌ (仅找到 $TABLE_COUNT 张表，预期 2 张)"
fi

# 测试前端路由
echo -n "5. 测试前端路由... "
if curl -s http://localhost:3000/opencode/hub > /dev/null; then
    echo "✅"
else
    echo "❌ 前端路由不可访问"
fi

echo ""
echo "🎉 集成测试完成！"
*/

// ============================================================================
// 5. Nginx 配置示例 (如果使用 Nginx 反向代理)
// ============================================================================

/*
# /etc/nginx/sites-available/opencode-pocket

server {
    listen 80;
    server_name pocket.example.com;

    # 前端静态文件
    location / {
        root /var/www/opencode-pocket/frontend/dist;
        try_files $uri $uri/ /index.html;
    }

    # 后端 API
    location /api/ {
        proxy_pass http://localhost:9010;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }

    # ⭐ WebSocket 支持（用于实时状态更新）
    location /ws {
        proxy_pass http://localhost:9010;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        
        # WebSocket 超时设置
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
    }
}
*/

// ============================================================================
// 6. Docker Compose 示例（可选）
// ============================================================================

/*
# docker-compose.yml

version: '3.8'

services:
  postgres:
    image: postgres:14
    environment:
      POSTGRES_DB: pocket_db
      POSTGRES_USER: pocket_user
      POSTGRES_PASSWORD: your_password
    volumes:
      - postgres_data:/var/lib/postgresql/data
    ports:
      - "5432:5432"

  backend:
    build: ./backend
    environment:
      DATABASE_URL: postgres://pocket_user:your_password@postgres:5432/pocket_db
      SERVER_ADDR: :9010
    ports:
      - "9010:9010"
    depends_on:
      - postgres
    volumes:
      - ./backend:/app

  frontend:
    build: ./frontend
    ports:
      - "3000:80"
    depends_on:
      - backend

volumes:
  postgres_data:
*/

// ============================================================================
// 7. 环境变量配置示例 (.env)
// ============================================================================

/*
# .env.production

# 数据库配置
DATABASE_URL=postgres://pocket_user:your_password@localhost:5432/pocket_db

# 服务器配置
SERVER_ADDR=:9010
SERVER_ENV=production

# OpenCode 配置
OPENCODE_POLLING_INTERVAL=30s
OPENCODE_CACHE_TTL=5m

# 日志配置
LOG_LEVEL=info
LOG_FILE=/var/log/opencode-pocket/app.log
*/

// ============================================================================
// 8. 快速启动脚本
// ============================================================================

/*
#!/bin/bash
# start.sh

set -e

echo "🚀 启动 OpenCode Pocket 服务..."

# 1. 检查数据库
echo "📦 检查数据库连接..."
if ! psql -U pocket_user -d pocket_db -c "SELECT 1" > /dev/null 2>&1; then
    echo "❌ 数据库连接失败，请检查 PostgreSQL 是否运行"
    exit 1
fi
echo "✅ 数据库连接正常"

# 2. 初始化数据库表
echo "🔧 初始化数据库表..."
psql -U pocket_user -d pocket_db < backend/sql/init_opencode.sql
echo "✅ 数据库表已初始化"

# 3. 启动后端
echo "🔨 编译并启动后端..."
cd backend
go build -o pocketd cmd/pocketd/main.go
./pocketd > /var/log/opencode-pocket/backend.log 2>&1 &
BACKEND_PID=$!
echo "✅ 后端已启动 (PID: $BACKEND_PID)"

# 4. 启动前端
echo "🎨 启动前端..."
cd ../frontend
npm run build
npm run preview > /var/log/opencode-pocket/frontend.log 2>&1 &
FRONTEND_PID=$!
echo "✅ 前端已启动 (PID: $FRONTEND_PID)"

# 5. 等待服务启动
echo "⏳ 等待服务启动..."
sleep 5

# 6. 健康检查
echo "🔍 执行健康检查..."
if curl -s http://localhost:9010/healthz > /dev/null; then
    echo "✅ 后端健康检查通过"
else
    echo "❌ 后端健康检查失败"
    exit 1
fi

if curl -s http://localhost:3000 > /dev/null; then
    echo "✅ 前端健康检查通过"
else
    echo "❌ 前端健康检查失败"
    exit 1
fi

echo ""
echo "🎉 OpenCode Pocket 服务已成功启动！"
echo ""
echo "📱 访问地址："
echo "   前端: http://localhost:3000"
echo "   后端: http://localhost:9010"
echo "   OpenCode Hub: http://localhost:3000/opencode/hub"
echo ""
echo "📊 进程信息："
echo "   后端 PID: $BACKEND_PID"
echo "   前端 PID: $FRONTEND_PID"
echo ""
echo "📝 日志文件："
echo "   后端: /var/log/opencode-pocket/backend.log"
echo "   前端: /var/log/opencode-pocket/frontend.log"
*/

// ============================================================================
// 9. 停止脚本
// ============================================================================

/*
#!/bin/bash
# stop.sh

echo "🛑 停止 OpenCode Pocket 服务..."

# 停止后端
echo "停止后端..."
pkill -f "pocketd" && echo "✅ 后端已停止" || echo "⚠️ 后端未运行"

# 停止前端
echo "停止前端..."
pkill -f "vite preview" && echo "✅ 前端已停止" || echo "⚠️ 前端未运行"

echo "✅ 服务已停止"
*/

// ============================================================================
// 10. 使用说明
// ============================================================================

/*
## 快速开始

### 1. 安装依赖
```bash
# 后端
cd backend && go mod download

# 前端
cd frontend && npm install
```

### 2. 配置环境
```bash
# 复制环境变量配置
cp .env.example .env

# 编辑配置
vim .env
```

### 3. 初始化数据库
```bash
# 创建数据库
createdb -U postgres pocket_db

# 创建用户
psql -U postgres -c "CREATE USER pocket_user WITH PASSWORD 'your_password';"
psql -U postgres -c "GRANT ALL PRIVILEGES ON DATABASE pocket_db TO pocket_user;"
```

### 4. 启动服务
```bash
# 使用启动脚本
chmod +x start.sh
./start.sh

# 或手动启动
# 后端
cd backend && go run cmd/pocketd/main.go

# 前端
cd frontend && npm run dev
```

### 5. 访问应用
打开浏览器访问：http://localhost:3000/opencode/hub

## 故障排查

查看日志：
```bash
# 后端日志
tail -f /var/log/opencode-pocket/backend.log

# 前端日志
tail -f /var/log/opencode-pocket/frontend.log
```

重启服务：
```bash
./stop.sh && ./start.sh
```
*/
