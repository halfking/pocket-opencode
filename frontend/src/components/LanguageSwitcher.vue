<template>
  <div class="language-switcher">
    <select v-model="currentLocale" @change="handleLocaleChange" class="locale-select">
      <option v-for="locale in availableLocales" :key="locale.code" :value="locale.code">
        {{ locale.name }}
      </option>
    </select>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useLocaleStore } from '@/stores/locale'
import type { LocaleType } from '@/i18n'

const { locale } = useI18n()
const localeStore = useLocaleStore()

const availableLocales = [
  { code: 'zh-CN', name: '简体中文' },
  { code: 'zh-TW', name: '繁體中文' },
  { code: 'en-US', name: 'English' },
  { code: 'ja-JP', name: '日本語' },
  { code: 'ko-KR', name: '한국어' },
  { code: 'de-DE', name: 'Deutsch' },
  { code: 'fr-FR', name: 'Français' },
  { code: 'es-ES', name: 'Español' },
  { code: 'pt-BR', name: 'Português' }
]

const currentLocale = computed({
  get: () => locale.value,
  set: (value) => {
    localeStore.setLocale(value as LocaleType)
  }
})

const handleLocaleChange = () => {
  // 可以在这里添加额外的处理逻辑，比如重新加载数据等
}
</script>

<style scoped>
.language-switcher {
  display: inline-block;
}

.locale-select {
  padding: 8px 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background-color: var(--bg-card);
  color: var(--text-primary);
  font-size: 14px;
  cursor: pointer;
  transition: border-color 0.3s;
}

.locale-select:hover {
  border-color: var(--brand-primary);
}

.locale-select:focus {
  outline: none;
  border-color: var(--brand-primary);
}
</style>
