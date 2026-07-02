# 国际化使用文档

## 概述

本项目已集成 vue-i18n 实现多语言国际化支持，目前支持以下 9 种语言：

- 简体中文 (zh-CN)
- 繁體中文 (zh-TW)
- English (en-US)
- 日本語 (ja-JP)
- 한국어 (ko-KR)
- Deutsch (de-DE)
- Français (fr-FR)
- Español (es-ES)
- Português (pt-BR)

## 项目结构

```
frontend/src/
├── i18n/
│   ├── index.ts          # i18n 配置文件
│   └── locales/          # 语言文件目录
│       ├── zh-CN.json
│       ├── en-US.json
│       ├── ja-JP.json
│       ├── ko-KR.json
│       ├── de-DE.json
│       ├── fr-FR.json
│       ├── es-ES.json
│       └── pt-BR.json
├── stores/
│   └── locale.ts         # 语言管理 Store
└── components/
    └── LanguageSwitcher.vue  # 语言切换组件示例
```

## 基本使用

### 1. 在组件中使用翻译

#### 在模板中使用

```vue
<template>
  <div>
    <!-- 使用 $t 函数 -->
    <h1>{{ $t('common.welcome') }}</h1>
    <p>{{ $t('common.description') }}</p>
    
    <!-- 使用插值 -->
    <p>{{ $t('user.greeting', { name: userName }) }}</p>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'

const userName = ref('张三')
</script>
```

#### 在脚本中使用

```vue
<script setup lang="ts">
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

// 使用 t 函数获取翻译
const message = t('common.welcome')

// 带参数的翻译
const greeting = t('user.greeting', { name: '张三' })

// 在函数中使用
const showMessage = () => {
  alert(t('common.success'))
}
</script>
```

### 2. 切换语言

#### 使用语言切换组件

```vue
<template>
  <div>
    <LanguageSwitcher />
  </div>
</template>

<script setup lang="ts">
import LanguageSwitcher from '@/components/LanguageSwitcher.vue'
</script>
```

#### 使用 Store 切换语言

```vue
<script setup lang="ts">
import { useLocaleStore } from '@/stores/locale'

const localeStore = useLocaleStore()

// 切换到英文
const switchToEnglish = () => {
  localeStore.setLocale('en-US')
}

// 获取当前语言
const currentLocale = localeStore.currentLocale
</script>
```

#### 直接使用 i18n 实例

```vue
<script setup lang="ts">
import { useI18n } from 'vue-i18n'

const { locale } = useI18n()

// 切换语言
const changeLanguage = (lang: string) => {
  locale.value = lang
}
</script>
```

### 3. 添加新的翻译内容

在相应的语言文件中添加键值对：

**zh-CN.json**
```json
{
  "common": {
    "welcome": "欢迎",
    "description": "这是一个示例应用"
  },
  "user": {
    "greeting": "你好，{name}！"
  }
}
```

**en-US.json**
```json
{
  "common": {
    "welcome": "Welcome",
    "description": "This is a sample application"
  },
  "user": {
    "greeting": "Hello, {name}!"
  }
}
```

### 4. 复数处理

```json
{
  "items": "no items | one item | {count} items"
}
```

```vue
<template>
  <p>{{ $t('items', 0) }}</p>  <!-- no items -->
  <p>{{ $t('items', 1) }}</p>  <!-- one item -->
  <p>{{ $t('items', 10) }}</p> <!-- 10 items -->
</template>
```

### 5. 日期和数字格式化

```vue
<script setup lang="ts">
import { useI18n } from 'vue-i18n'

const { n, d } = useI18n()

// 格式化数字
const price = n(1234.56, 'currency')

// 格式化日期
const today = d(new Date(), 'long')
</script>
```

## 最佳实践

### 1. 组织翻译键

建议按功能模块组织翻译键：

```json
{
  "common": {
    "button": {
      "save": "保存",
      "cancel": "取消",
      "delete": "删除"
    },
    "message": {
      "success": "操作成功",
      "error": "操作失败"
    }
  },
  "user": {
    "profile": {
      "title": "个人资料",
      "name": "姓名",
      "email": "邮箱"
    }
  }
}
```

### 2. 避免硬编码文本

❌ 不推荐：
```vue
<button>保存</button>
```

✅ 推荐：
```vue
<button>{{ $t('common.button.save') }}</button>
```

### 3. 使用命名空间

对于大型项目，可以使用命名空间来组织翻译：

```vue
<script setup lang="ts">
import { useI18n } from 'vue-i18n'

const { t } = useI18n({
  useScope: 'local',
  messages: {
    'zh-CN': {
      hello: '你好'
    },
    'en-US': {
      hello: 'Hello'
    }
  }
})
</script>
```

### 4. 类型安全

项目已配置 TypeScript 类型支持，可以获得更好的类型提示：

```typescript
import type { Locale } from '@/i18n'

const supportedLocales: Locale[] = [
  'zh-CN', 'en-US', 'ja-JP', 'ko-KR',
  'de-DE', 'fr-FR', 'es-ES', 'pt-BR'
]
```

## 常见问题

### 1. 如何添加新语言？

1. 在 `src/i18n/locales/` 目录下创建新的语言文件，如 `it-IT.json`
2. 在 `src/i18n/index.ts` 中导入并添加到 messages 对象
3. 在 `src/i18n/index.ts` 的 LocaleType 类型和相关数组中添加新语言代码
4. 更新 `LanguageSwitcher.vue` 组件的 availableLocales 列表
5. 运行 `node scripts/verify-i18n.js` 验证键结构一致性

### 2. 如何处理动态内容？

使用插值：
```vue
<template>
  <p>{{ $t('message.welcome', { name: userName, count: itemCount }) }}</p>
</template>
```

语言文件：
```json
{
  "message": {
    "welcome": "欢迎 {name}，你有 {count} 条消息"
  }
}
```

### 3. 如何在路由守卫或非组件中使用？

```typescript
import i18n from '@/i18n'

const { t } = i18n.global

// 使用翻译
const message = t('common.welcome')
```

### 4. 如何实现语言持久化？

语言偏好已通过 `stores/locale.ts` 自动保存到 localStorage，刷新页面后会自动恢复用户选择的语言。

## 维护指南

### 添加新翻译

1. 在 `zh-CN.json` (主语言文件) 中添加新的键值对
2. 使用翻译工具或人工翻译到其他 7 种语言
3. 确保所有语言文件的键结构保持一致

### 检查缺失翻译

可以编写脚本比对不同语言文件的键，确保完整性：

```typescript
// 示例：比对脚本
import zhCN from './locales/zh-CN.json'
import enUS from './locales/en-US.json'

const checkMissingKeys = (source: any, target: any, prefix = '') => {
  for (const key in source) {
    const fullKey = prefix ? `${prefix}.${key}` : key
    if (!(key in target)) {
      console.warn(`Missing key: ${fullKey}`)
    } else if (typeof source[key] === 'object') {
      checkMissingKeys(source[key], target[key], fullKey)
    }
  }
}

checkMissingKeys(zhCN, enUS)
```

## 参考资源

- [Vue I18n 官方文档](https://vue-i18n.intlify.dev/)
- [i18n 最佳实践](https://vue-i18n.intlify.dev/guide/essentials/syntax.html)
- [Pinia Store 文档](https://pinia.vuejs.org/)
