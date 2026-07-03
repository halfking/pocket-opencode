<template>
  <div class="bottom-nav" :style="{ paddingBottom: safeAreaBottom }">
    <nav class="nav-container">
      <button
        v-for="item in navItems"
        :key="item.id"
        :class="navItemClasses(item.id)"
        @click="handleNavClick(item.id)"
      >
        <div class="nav-icon">
          {{ item.icon }}
        </div>
        <span class="nav-label">{{ item.label }}</span>
      </button>
    </nav>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'

export interface NavItem {
  id: string
  icon: string
  label: string
  path?: string
}

export interface BottomNavProps {
  items: NavItem[]
  active?: string
  onChange?: (id: string) => void
}

const props = defineProps<BottomNavProps>()

const activeId = ref(props.active || props.items[0]?.id)

const navItems = computed(() => props.items)

const safeAreaBottom = computed(() => {
  // 处理 iPhone 等设备的底部安全区域
  return 'env(safe-area-inset-bottom, 0px)'
})

const navItemClasses = (id: string) => {
  return [
    'nav-item',
    {
      'nav-item--active': activeId.value === id,
    },
  ]
}

const handleNavClick = (id: string) => {
  activeId.value = id
  props.onChange?.(id)
}

defineExpose({
  setActive: (id: string) => {
    activeId.value = id
  },
})
</script>

<style scoped>
.bottom-nav {
  position: fixed;
  bottom: 0;
  left: 0;
  right: 0;
  background: var(--color-bg-surface);
  border-top: 1px solid var(--color-border);
  z-index: 1000;
  box-shadow: 0 -2px 10px rgba(0, 0, 0, 0.05);
}

.nav-container {
  display: flex;
  align-items: center;
  justify-content: space-around;
  height: 60px;
  padding: 0 var(--space-2);
}

.nav-item {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 4px;
  flex: 1;
  height: 100%;
  background: none;
  border: none;
  cursor: pointer;
  color: var(--color-text-tertiary);
  transition: all var(--duration-fast) var(--ease-out);
  user-select: none;
  -webkit-tap-highlight-color: transparent;
  padding: var(--space-2);
  position: relative;
}

.nav-item::before {
  content: '';
  position: absolute;
  top: 0;
  left: 50%;
  transform: translateX(-50%) scaleX(0);
  width: 40px;
  height: 3px;
  background: var(--gradient-primary);
  border-radius: 0 0 3px 3px;
  transition: transform var(--duration-base) var(--ease-spring);
}

.nav-item--active::before {
  transform: translateX(-50%) scaleX(1);
}

.nav-item:active {
  transform: scale(0.9);
}

.nav-item--active {
  color: var(--color-primary);
}

.nav-icon {
  font-size: 24px;
  line-height: 1;
  transition: transform var(--duration-base) var(--ease-spring);
}

.nav-item--active .nav-icon {
  transform: scale(1.1);
}

.nav-label {
  font-size: 11px;
  font-weight: var(--font-weight-medium);
  line-height: 1;
  white-space: nowrap;
}

@media (prefers-color-scheme: dark) {
  .bottom-nav {
    box-shadow: 0 -2px 10px rgba(0, 0, 0, 0.3);
  }
}
</style>
