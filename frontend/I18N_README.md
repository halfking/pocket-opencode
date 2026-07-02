# 多语言国际化使用指南（更新版）

## 概述

本项目已集成 vue-i18n 实现多语言国际化支持，目前支持以下 9 种语言：

- 🇨🇳 简体中文 (zh-CN)
- 🇹🇼 繁體中文 (zh-TW)
- 🇺🇸 English (en-US)
- 🇯🇵 日本語 (ja-JP)
- 🇰🇷 한국어 (ko-KR)
- 🇩🇪 Deutsch (de-DE)
- 🇫🇷 Français (fr-FR)
- 🇪🇸 Español (es-ES)
- 🇧🇷 Português (pt-BR)

## 快速开始

### 在组件中使用翻译

```vue
<template>
  <div>
    <h1>{{ $t('app.welcome') }}</h1>
    <button>{{ $t('common.save') }}</button>
  </div>
</template>
```

### 切换语言

```vue
<script setup>
import { useI18n } from 'vue-i18n'
import { useLocaleStore } from '@/stores/locale'

const { locale } = useI18n()
const localeStore = useLocaleStore()

const switchLanguage = (lang) => {
  locale.value = lang
  localeStore.setLocale(lang)
}
</script>
```

## 验证工具

### 检查语言文件一致性

```bash
node scripts/verify-i18n.js
```

该脚本会自动验证：
- 所有语言文件的键结构是否一致
- 是否有缺失或多余的翻译键
- 输出详细的验证报告

## 维护指南

### 添加新的翻译键

1. 在 `zh-CN.json` 中添加新键
2. 在其他 8 个语言文件中添加相同的键
3. 运行验证脚本确认一致性：`node scripts/verify-i18n.js`

### 添加新语言

1. 创建语言文件：`src/locales/xx-XX.json`
2. 更新 `src/i18n/index.ts`：导入并添加到配置
3. 更新 `LanguageSwitcher.vue`：添加到下拉列表
4. 更新验证脚本中的 localeFiles 数组

## 文档

详细文档请参考：
- **I18N_USAGE.md** - 完整使用指南
- **I18N_VERIFICATION_REPORT.md** - 验证报告
- **I18N_IMPLEMENTATION_SUMMARY.md** - 实施总结

---

**最后更新**: 2026-07-02
**当前语言数**: 9 种
**翻译键数**: 20 个
