<template>
  <Teleport to="body">
    <div v-if="open" class="overlay" role="dialog" aria-modal="true" aria-label="发票预览" @click="emit('close')">
      <section class="sheet" @click.stop>
        <header class="head">
          <h3>{{ title }}</h3>
          <button type="button" class="icon" aria-label="关闭" @click="emit('close')">
            <span class="material-symbols-outlined">close</span>
          </button>
        </header>
        <div class="body">
          <img v-if="kind === 'image' && src" :src="src" alt="发票全图" class="full-img">
          <iframe v-else-if="kind === 'pdf' && src" :src="src" title="发票文件" class="full-doc" />
          <p v-else class="empty">无法预览此文件，请下载查看</p>
        </div>
        <footer class="foot">
          <button type="button" class="act" @click="emit('open-email')">原邮件</button>
          <button type="button" class="act" @click="emit('download')">下载文件</button>
        </footer>
      </section>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import type { InvoiceFileKind } from './invoice-list'

defineProps<{
  open: boolean
  title: string
  src: string
  kind: InvoiceFileKind
}>()

const emit = defineEmits<{
  close: []
  'open-email': []
  download: []
}>()
</script>

<style scoped>
.overlay {
  position: fixed; inset: 0; z-index: 80;
  background: color-mix(in srgb, #000 45%, transparent);
  display: flex; align-items: flex-end; justify-content: center;
}
.sheet {
  width: 100%; max-width: 640px; max-height: 92vh;
  background: var(--bg-card); border-radius: 16px 16px 0 0;
  display: flex; flex-direction: column;
}
.head {
  display: flex; align-items: center; justify-content: space-between;
  padding: 12px 16px; border-bottom: 1px solid var(--border);
}
.head h3 { margin: 0; font-size: 15px; }
.icon { background: none; border: none; color: var(--text-primary); cursor: pointer; }
.body { flex: 1; min-height: 240px; overflow: auto; background: var(--bg-subtle); }
.full-img { display: block; width: 100%; height: auto; }
.full-doc { width: 100%; height: 70vh; border: 0; background: #fff; }
.empty { padding: 32px; text-align: center; color: var(--text-secondary); }
.foot {
  display: flex; gap: 8px; padding: 12px 16px 20px;
  border-top: 1px solid var(--border);
}
.act {
  flex: 1; padding: 10px; border-radius: 10px; cursor: pointer;
  border: 1px solid var(--border); background: var(--bg-subtle); color: var(--text-primary);
}
</style>
