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
  background: var(--color-bg-surface);
  border-radius: var(--radius-lg);
  transition: all var(--duration-base) var(--ease-out);
}

/* Default 变体 */
.card--default {
  box-shadow: var(--shadow-md);
}

/* Outlined 变体 */
.card--outlined {
  border: 1px solid var(--color-border);
  box-shadow: none;
}

/* Elevated 变体 */
.card--elevated {
  box-shadow: var(--shadow-lg);
}

/* Hoverable 效果 */
.card--hoverable:hover {
  transform: translateY(-2px);
  box-shadow: var(--shadow-lg);
}

/* Clickable 效果 */
.card--clickable {
  cursor: pointer;
}

.card--clickable:active {
  transform: scale(0.98);
}
</style>
