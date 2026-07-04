<template>
  <div :class="cardClasses" @click="handleClick">
    <slot />
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'

export interface CardProps {
  variant?: 'default' | 'outlined' | 'elevated'
  hoverable?: boolean
  clickable?: boolean
}

const props = withDefaults(defineProps<CardProps>(), {
  variant: 'default',
  hoverable: false,
  clickable: false,
})

const emit = defineEmits<{
  (e: 'click', event: MouseEvent): void
}>()

const cardClasses = computed(() => {
  return [
    'card',
    `card--${props.variant}`,
    {
      'card--hoverable': props.hoverable,
      'card--clickable': props.clickable,
    },
  ]
})

const handleClick = (event: MouseEvent) => {
  if (props.clickable) {
    emit('click', event)
  }
}
</script>

<style scoped>
.card {
  background: var(--bg-card);
  border-radius: var(--radius-md);  /* 修改：lg → md (8px) */
  transition: all var(--duration-base) var(--ease-out);
  border: 1px solid var(--border);  /* 新增：边框替代阴影 */
}

/* Default 变体 - 移除阴影 */
.card--default {
  /* 无阴影，更轻盈 */
}

/* Outlined 变体 */
.card--outlined {
  border: 1px solid var(--border);
  box-shadow: none;
}

/* Elevated 变体 */
.card--elevated {
  box-shadow: var(--shadow-sm);  /* 修改：lg → sm */
}

/* Hoverable 效果 */
.card--hoverable:hover {
  transform: translateY(-1px);  /* 修改：-2px → -1px */
  box-shadow: var(--shadow-md); /* 修改：lg → md */
}

/* Clickable 效果 */
.card--clickable {
  cursor: pointer;
}

.card--clickable:active {
  transform: scale(0.98);
}
</style>
