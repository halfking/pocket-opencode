<template>
  <div class="skeleton-loader">
    <div v-for="i in count" :key="i" class="skeleton-item">
      <div v-if="avatar" class="skeleton-avatar"></div>
      <div class="skeleton-content">
        <div class="skeleton-line skeleton-line--title"></div>
        <div class="skeleton-line skeleton-line--text"></div>
        <div v-if="rows > 2" class="skeleton-line skeleton-line--text skeleton-line--short"></div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
export interface SkeletonProps {
  count?: number
  avatar?: boolean
  rows?: number
}

withDefaults(defineProps<SkeletonProps>(), {
  count: 3,
  avatar: false,
  rows: 2,
})
</script>

<style scoped>
.skeleton-loader {
  padding: var(--space-4);
}

.skeleton-item {
  display: flex;
  gap: var(--space-3);
  margin-bottom: var(--space-4);
}

.skeleton-avatar {
  flex-shrink: 0;
  width: 48px;
  height: 48px;
  border-radius: 50%;
  background: linear-gradient(
    90deg,
    var(--color-border) 25%,
    var(--color-bg-surface) 50%,
    var(--color-border) 75%
  );
  background-size: 200% 100%;
  animation: shimmer 1.5s ease-in-out infinite;
}

.skeleton-content {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.skeleton-line {
  height: 16px;
  border-radius: var(--radius-sm);
  background: linear-gradient(
    90deg,
    var(--color-border) 25%,
    var(--color-bg-surface) 50%,
    var(--color-border) 75%
  );
  background-size: 200% 100%;
  animation: shimmer 1.5s ease-in-out infinite;
}

.skeleton-line--title {
  width: 60%;
  height: 20px;
}

.skeleton-line--text {
  width: 100%;
}

.skeleton-line--short {
  width: 80%;
}

@keyframes shimmer {
  0% {
    background-position: 200% 0;
  }
  100% {
    background-position: -200% 0;
  }
}
</style>
