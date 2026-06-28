# OpenCode 模型配置管理方案

实现手机端管理 OpenCode 模型配置，并支持热加载。

---

## 架构设计

```text
[手机 App - 配置管理 UI]
    ↓ HTTPS
[Pocket Backend]
    ↓ OpenCode Config API
[OpenCode Instance]
    ├─ GET /api/config/models      # 获取当前配置
    ├─ PUT /api/config/models      # 更新配置
    └─ POST /api/config/reload     # 热加载配置
```

---

## Phase 1: OpenCode Config API 规范

### 1.1 配置数据结构

```typescript
// OpenCode 模型配置格式
interface ModelConfig {
  providers: Provider[]
  defaultProvider?: string
  timeout?: number
}

interface Provider {
  id: string                    // "openai", "anthropic", "deepseek"
  name: string                  // 显示名称
  enabled: boolean              // 是否启用
  apiKey?: string              // API Key (加密传输)
  baseURL?: string             // 自定义 API Base URL
  models: ModelDefinition[]     // 该提供商的模型列表
  priority?: number            // 优先级
}

interface ModelDefinition {
  id: string                   // "gpt-4", "claude-3-opus"
  displayName: string          // 显示名称
  enabled: boolean             // 是否启用
  maxTokens?: number          // 最大 token 数
  temperature?: number        // 温度参数
  contextWindow?: number      // 上下文窗口大小
  pricing?: {
    input: number             // 输入价格 (per 1M tokens)
    output: number            // 输出价格 (per 1M tokens)
  }
}
```

### 1.2 API 端点

```bash
# 获取当前模型配置
GET /api/config/models
Response: { config: ModelConfig }

# 更新模型配置
PUT /api/config/models
Body: { config: ModelConfig }
Response: { success: boolean, message: string }

# 热加载配置
POST /api/config/reload
Response: { success: boolean, reloadedAt: string }

# 测试模型连接
POST /api/config/models/test
Body: { providerId: string, modelId: string }
Response: { success: boolean, latency: number, error?: string }
```

---

## Phase 2: Pocket Backend 适配器

### 2.1 OpenCode Config Adapter

```go
// backend/internal/adapter/opencode_config.go
package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"bytes"
)

type ModelConfig struct {
	Providers       []Provider `json:"providers"`
	DefaultProvider string     `json:"defaultProvider,omitempty"`
	Timeout         int        `json:"timeout,omitempty"`
}

type Provider struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Enabled  bool              `json:"enabled"`
	APIKey   string            `json:"apiKey,omitempty"`
	BaseURL  string            `json:"baseURL,omitempty"`
	Models   []ModelDefinition `json:"models"`
	Priority int               `json:"priority,omitempty"`
}

type ModelDefinition struct {
	ID            string         `json:"id"`
	DisplayName   string         `json:"displayName"`
	Enabled       bool           `json:"enabled"`
	MaxTokens     int            `json:"maxTokens,omitempty"`
	Temperature   float64        `json:"temperature,omitempty"`
	ContextWindow int            `json:"contextWindow,omitempty"`
	Pricing       *ModelPricing  `json:"pricing,omitempty"`
}

type ModelPricing struct {
	Input  float64 `json:"input"`
	Output float64 `json:"output"`
}

type OpenCodeConfigAdapter interface {
	GetModelConfig(ctx context.Context, instanceBaseURL string) (*ModelConfig, error)
	UpdateModelConfig(ctx context.Context, instanceBaseURL string, config *ModelConfig) error
	ReloadConfig(ctx context.Context, instanceBaseURL string) error
	TestModel(ctx context.Context, instanceBaseURL, providerID, modelID string) error
}

type OpenCodeConfigHTTPAdapter struct {
	client  *http.Client
	timeout time.Duration
}

func NewOpenCodeConfigHTTPAdapter(timeoutMS int) *OpenCodeConfigHTTPAdapter {
	return &OpenCodeConfigHTTPAdapter{
		client:  &http.Client{},
		timeout: time.Duration(timeoutMS) * time.Millisecond,
	}
}

func (a *OpenCodeConfigHTTPAdapter) GetModelConfig(ctx context.Context, instanceBaseURL string) (*ModelConfig, error) {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	url := fmt.Sprintf("%s/api/config/models", instanceBaseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get model config failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get model config returned %d", resp.StatusCode)
	}

	var result struct {
		Config ModelConfig `json:"config"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode config failed: %w", err)
	}

	return &result.Config, nil
}

func (a *OpenCodeConfigHTTPAdapter) UpdateModelConfig(ctx context.Context, instanceBaseURL string, config *ModelConfig) error {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	body, err := json.Marshal(map[string]interface{}{"config": config})
	if err != nil {
		return fmt.Errorf("marshal config failed: %w", err)
	}

	url := fmt.Sprintf("%s/api/config/models", instanceBaseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("update model config failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("update model config returned %d", resp.StatusCode)
	}

	return nil
}

func (a *OpenCodeConfigHTTPAdapter) ReloadConfig(ctx context.Context, instanceBaseURL string) error {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	url := fmt.Sprintf("%s/api/config/reload", instanceBaseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return fmt.Errorf("create request failed: %w", err)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("reload config failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("reload config returned %d", resp.StatusCode)
	}

	return nil
}

func (a *OpenCodeConfigHTTPAdapter) TestModel(ctx context.Context, instanceBaseURL, providerID, modelID string) error {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	body, _ := json.Marshal(map[string]string{
		"providerId": providerID,
		"modelId":    modelID,
	})

	url := fmt.Sprintf("%s/api/config/models/test", instanceBaseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("test model failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("test model returned %d", resp.StatusCode)
	}

	return nil
}
```

### 2.2 Pocket Server 端点

```go
// backend/internal/server/config_handlers.go
package server

func (s *Server) handleModelConfig(w http.ResponseWriter, r *http.Request) {
	instanceID := r.URL.Query().Get("instance_id")
	if instanceID == "" {
		http.Error(w, "missing instance_id", http.StatusBadRequest)
		return
	}

	apiBaseURL, err := s.registry.GetInstanceAPIBase(instanceID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	switch r.Method {
	case http.MethodGet:
		config, err := s.configAdapter.GetModelConfig(r.Context(), apiBaseURL)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"config": config})

	case http.MethodPut:
		var req struct {
			Config adapter.ModelConfig `json:"config"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if err := s.configAdapter.UpdateModelConfig(r.Context(), apiBaseURL, &req.Config); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]bool{"success": true})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleConfigReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	instanceID := r.URL.Query().Get("instance_id")
	if instanceID == "" {
		http.Error(w, "missing instance_id", http.StatusBadRequest)
		return
	}

	apiBaseURL, err := s.registry.GetInstanceAPIBase(instanceID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	if err := s.configAdapter.ReloadConfig(r.Context(), apiBaseURL); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":    true,
		"reloadedAt": time.Now().UTC().Format(time.RFC3339),
	})
}
```

---

## Phase 3: 手机端配置 UI

### 3.1 路由配置

```typescript
// frontend/src/router/routes.ts
export const routes = [
  { path: '/', component: TaskBoard },
  { path: '/task/:id', component: TaskDetail },
  { path: '/config', component: ConfigManager },           // 新增
  { path: '/config/instance/:id', component: ModelConfig }, // 新增
]
```

### 3.2 配置管理主界面

```vue
<!-- frontend/src/features/config/ConfigManager.vue -->
<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { api } from '../../api/client'

const instances = ref([])
const loading = ref(true)

onMounted(async () => {
  instances.value = await api.getInstances()
  loading.value = false
})

function navigateToConfig(instanceId: string) {
  // 跳转到具体实例的配置页
}
</script>

<template>
  <div class="config-manager">
    <header class="px-6 py-4 border-b">
      <h1 class="text-2xl font-bold">模型配置管理</h1>
      <p class="text-gray-600">管理 OpenCode 实例的模型配置</p>
    </header>

    <div v-if="loading" class="p-6 text-center">
      <div class="text-gray-500">加载中...</div>
    </div>

    <div v-else class="p-6 space-y-4">
      <div
        v-for="instance in instances"
        :key="instance.id"
        @click="navigateToConfig(instance.id)"
        class="bg-white rounded-lg shadow p-4 cursor-pointer hover:shadow-md transition"
      >
        <div class="flex items-center justify-between">
          <div>
            <h3 class="font-semibold text-lg">{{ instance.displayName }}</h3>
            <p class="text-sm text-gray-500">{{ instance.environment }}</p>
          </div>
          <svg class="w-6 h-6 text-gray-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5l7 7-7 7" />
          </svg>
        </div>
      </div>
    </div>
  </div>
</template>
```

### 3.3 模型配置编辑界面

```vue
<!-- frontend/src/features/config/ModelConfig.vue -->
<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { api, type ModelConfig, type Provider } from '../../api/client'

const route = useRoute()
const instanceId = route.params.id as string

const config = ref<ModelConfig | null>(null)
const loading = ref(true)
const saving = ref(false)
const reloading = ref(false)

onMounted(async () => {
  await loadConfig()
})

async function loadConfig() {
  loading.value = true
  try {
    config.value = await api.getModelConfig(instanceId)
  } catch (e: any) {
    alert(`加载配置失败: ${e.message}`)
  } finally {
    loading.value = false
  }
}

async function saveConfig() {
  if (!config.value) return
  
  saving.value = true
  try {
    await api.updateModelConfig(instanceId, config.value)
    alert('配置已保存')
  } catch (e: any) {
    alert(`保存失败: ${e.message}`)
  } finally {
    saving.value = false
  }
}

async function reloadConfig() {
  reloading.value = true
  try {
    await api.reloadConfig(instanceId)
    alert('配置已热加载')
  } catch (e: any) {
    alert(`热加载失败: ${e.message}`)
  } finally {
    reloading.value = false
  }
}

async function testProvider(provider: Provider) {
  if (!provider.models || provider.models.length === 0) return
  
  try {
    await api.testModel(instanceId, provider.id, provider.models[0].id)
    alert(`${provider.name} 连接测试成功`)
  } catch (e: any) {
    alert(`测试失败: ${e.message}`)
  }
}

function toggleProvider(provider: Provider) {
  provider.enabled = !provider.enabled
}

function toggleModel(model: any) {
  model.enabled = !model.enabled
}
</script>

<template>
  <div class="model-config">
    <!-- Header -->
    <header class="px-6 py-4 border-b sticky top-0 bg-white z-10">
      <div class="flex items-center justify-between">
        <div>
          <button @click="$router.back()" class="text-blue-600 mb-2">
            ← 返回
          </button>
          <h1 class="text-xl font-bold">模型配置</h1>
        </div>
        <div class="flex gap-2">
          <button
            @click="reloadConfig"
            :disabled="reloading"
            class="px-4 py-2 bg-yellow-600 text-white rounded hover:bg-yellow-700 disabled:opacity-50"
          >
            {{ reloading ? '热加载中...' : '🔄 热加载' }}
          </button>
          <button
            @click="saveConfig"
            :disabled="saving"
            class="px-4 py-2 bg-green-600 text-white rounded hover:bg-green-700 disabled:opacity-50"
          >
            {{ saving ? '保存中...' : '💾 保存' }}
          </button>
        </div>
      </div>
    </header>

    <!-- Loading -->
    <div v-if="loading" class="p-6 text-center">
      <div class="text-gray-500">加载配置中...</div>
    </div>

    <!-- Config Content -->
    <div v-else-if="config" class="p-6 space-y-6">
      <!-- Provider 列表 -->
      <div
        v-for="provider in config.providers"
        :key="provider.id"
        class="bg-white rounded-lg shadow"
      >
        <!-- Provider Header -->
        <div class="p-4 border-b flex items-center justify-between">
          <div class="flex items-center gap-3">
            <input
              type="checkbox"
              :checked="provider.enabled"
              @change="toggleProvider(provider)"
              class="w-5 h-5"
            />
            <div>
              <h3 class="font-semibold text-lg">{{ provider.name }}</h3>
              <p class="text-sm text-gray-500">{{ provider.id }}</p>
            </div>
          </div>
          <button
            @click="testProvider(provider)"
            class="px-3 py-1 bg-blue-100 text-blue-700 rounded text-sm hover:bg-blue-200"
          >
            测试连接
          </button>
        </div>

        <!-- Provider Settings -->
        <div v-if="provider.enabled" class="p-4 bg-gray-50 space-y-3">
          <div>
            <label class="block text-sm font-medium mb-1">API Key</label>
            <input
              v-model="provider.apiKey"
              type="password"
              placeholder="输入 API Key"
              class="w-full px-3 py-2 border rounded"
            />
          </div>
          <div>
            <label class="block text-sm font-medium mb-1">Base URL (可选)</label>
            <input
              v-model="provider.baseURL"
              type="text"
              placeholder="自定义 API 地址"
              class="w-full px-3 py-2 border rounded"
            />
          </div>
        </div>

        <!-- Models -->
        <div v-if="provider.enabled" class="divide-y">
          <div
            v-for="model in provider.models"
            :key="model.id"
            class="p-4 flex items-start gap-3"
          >
            <input
              type="checkbox"
              :checked="model.enabled"
              @change="toggleModel(model)"
              class="w-5 h-5 mt-1"
            />
            <div class="flex-1">
              <div class="font-medium">{{ model.displayName }}</div>
              <div class="text-sm text-gray-500">{{ model.id }}</div>
              <div v-if="model.contextWindow" class="text-xs text-gray-400 mt-1">
                上下文: {{ model.contextWindow }} tokens
              </div>
              <div v-if="model.pricing" class="text-xs text-gray-400">
                价格: ${{ model.pricing.input }}/M 输入, ${{ model.pricing.output }}/M 输出
              </div>
            </div>
          </div>
        </div>
      </div>

      <!-- 全局设置 -->
      <div class="bg-white rounded-lg shadow p-4">
        <h3 class="font-semibold mb-3">全局设置</h3>
        <div class="space-y-3">
          <div>
            <label class="block text-sm font-medium mb-1">默认提供商</label>
            <select
              v-model="config.defaultProvider"
              class="w-full px-3 py-2 border rounded"
            >
              <option value="">自动选择</option>
              <option v-for="p in config.providers" :key="p.id" :value="p.id">
                {{ p.name }}
              </option>
            </select>
          </div>
          <div>
            <label class="block text-sm font-medium mb-1">超时时间 (秒)</label>
            <input
              v-model.number="config.timeout"
              type="number"
              class="w-full px-3 py-2 border rounded"
            />
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.model-config {
  min-height: 100vh;
  background: #f7f7f8;
}
</style>
```

### 3.4 API 客户端扩展

```typescript
// frontend/src/api/client.ts (扩展)

export interface ModelConfig {
  providers: Provider[]
  defaultProvider?: string
  timeout?: number
}

export interface Provider {
  id: string
  name: string
  enabled: boolean
  apiKey?: string
  baseURL?: string
  models: ModelDefinition[]
  priority?: number
}

export interface ModelDefinition {
  id: string
  displayName: string
  enabled: boolean
  maxTokens?: number
  temperature?: number
  contextWindow?: number
  pricing?: {
    input: number
    output: number
  }
}

export const api = {
  // ... 现有方法 ...

  async getModelConfig(instanceId: string): Promise<ModelConfig> {
    const res = await fetch(`${API_BASE}/api/config/models?instance_id=${instanceId}`)
    if (!res.ok) throw new Error(`Failed to get config: ${res.statusText}`)
    const data = await res.json()
    return data.config
  },

  async updateModelConfig(instanceId: string, config: ModelConfig): Promise<void> {
    const res = await fetch(`${API_BASE}/api/config/models?instance_id=${instanceId}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ config }),
    })
    if (!res.ok) throw new Error(`Failed to update config: ${res.statusText}`)
  },

  async reloadConfig(instanceId: string): Promise<void> {
    const res = await fetch(`${API_BASE}/api/config/reload?instance_id=${instanceId}`, {
      method: 'POST',
    })
    if (!res.ok) throw new Error(`Failed to reload config: ${res.statusText}`)
  },

  async testModel(instanceId: string, providerId: string, modelId: string): Promise<void> {
    const res = await fetch(`${API_BASE}/api/config/models/test?instance_id=${instanceId}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ providerId, modelId }),
    })
    if (!res.ok) throw new Error(`Failed to test model: ${res.statusText}`)
  },
}
```

---

## Phase 4: OpenCode 侧实现

OpenCode 需要实现配置 API 端点。在 `packages/opencode/src/server/routes` 添加：

```typescript
// packages/opencode/src/server/routes/instance/httpapi/groups/config.ts
import { Router } from 'express'
import { getModelConfig, updateModelConfig, reloadModelConfig } from '../../../config/manager'

export const configRouter = Router()

// 获取模型配置
configRouter.get('/models', async (req, res) => {
  try {
    const config = await getModelConfig()
    res.json({ config })
  } catch (error) {
    res.status(500).json({ error: error.message })
  }
})

// 更新模型配置
configRouter.put('/models', async (req, res) => {
  try {
    const { config } = req.body
    await updateModelConfig(config)
    res.json({ success: true })
  } catch (error) {
    res.status(500).json({ error: error.message })
  }
})

// 热加载配置
configRouter.post('/reload', async (req, res) => {
  try {
    await reloadModelConfig()
    res.json({
      success: true,
      reloadedAt: new Date().toISOString(),
    })
  } catch (error) {
    res.status(500).json({ error: error.message })
  }
})

// 测试模型
configRouter.post('/models/test', async (req, res) => {
  try {
    const { providerId, modelId } = req.body
    const startTime = Date.now()
    
    // 测试模型连接
    await testModelConnection(providerId, modelId)
    
    const latency = Date.now() - startTime
    res.json({ success: true, latency })
  } catch (error) {
    res.status(500).json({ success: false, error: error.message })
  }
})
```

---

## 部署和验证

### 1. 更新 Pocket Backend

```bash
cd backend
# 添加新的 config adapter 和 handlers
go build -o pocketd cmd/pocketd/main.go
sudo systemctl restart opencode-pocket
```

### 2. 更新前端并构建 Android

```bash
cd frontend
npm run build
npx cap sync android
cd android
./gradlew assembleDebug
```

### 3. 安装到手机测试

```bash
adb install app/build/outputs/apk/debug/app-debug.apk
```

### 4. 验证流程

1. 打开 App
2. 进入"配置管理"
3. 选择一个 OpenCode 实例
4. 查看当前模型配置
5. 修改配置（如启用/禁用模型、更新 API Key）
6. 点击"保存"
7. 点击"热加载"
8. 返回 OpenCode，验证配置已生效

---

## 安全考虑

1. **API Key 加密传输**: 使用 HTTPS
2. **敏感信息脱敏**: API Key 在 UI 显示为 `****`
3. **权限控制**: 只有管理员可以修改配置
4. **操作审计**: 记录所有配置变更

---

## 下一步增强

1. 配置版本历史和回滚
2. 批量配置多个实例
3. 配置模板和预设
4. 模型使用统计和成本分析
