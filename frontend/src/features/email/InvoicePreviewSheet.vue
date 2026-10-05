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

        <!-- Android 原生 PdfRenderer 渲染：WebView 内核不认 PDF，<iframe> 是全白 -->
        <div v-if="kind === 'pdf' && pdf.supported" ref="pdfHost" class="body">
          <p v-if="pdf.loading.value" class="hint">正在渲染…</p>
          <p v-else-if="pdf.error.value" class="empty">无法预览此文件：{{ pdf.error.value }}</p>
          <img v-else-if="pdf.image.value" :src="pdf.image.value" alt="发票文件" class="full-img">
          <p v-else class="empty">无法预览此文件，请下载查看</p>
        </div>

        <div v-else-if="kind === 'image' && src" class="body">
          <img :src="src" alt="发票全图" class="full-img">
        </div>

        <!-- web / iOS：系统 WebView 能渲染 PDF，保持 iframe -->
        <div v-else-if="kind === 'pdf' && src" class="body">
          <iframe :src="src" title="发票文件" class="full-doc" />
        </div>

        <div v-else class="body">
          <p class="empty">无法预览此文件，请下载查看</p>
        </div>

        <footer v-if="kind === 'pdf' && pdf.supported && pdf.pageCount.value > 1" class="pager">
          <button type="button" class="pg" :disabled="pdf.page.value <= 0" @click="pdf.prev()">
            <span class="material-symbols-outlined">chevron_left</span>
          </button>
          <span class="pg-label">{{ pdf.page.value + 1 }} / {{ pdf.pageCount.value }}</span>
          <button type="button" class="pg" :disabled="pdf.page.value >= pdf.pageCount.value - 1" @click="pdf.next()">
            <span class="material-symbols-outlined">chevron_right</span>
          </button>
        </footer>

        <footer class="foot">
          <button type="button" class="act" @click="emit('open-email')">原邮件</button>
          <button type="button" class="act" @click="emit('download')">下载文件</button>
        </footer>
      </section>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import type { InvoiceFileKind } from './invoice-list'
import { usePdfViewer } from '../../composables/usePdfViewer'

const props = defineProps<{
  open: boolean
  title: string
  /** 图片类附件与 web/iOS 的 PDF 走这里；Android PDF 由 PdfRenderer 渲染 */
  src: string
  /** Android PDF 预览用的原始字节 */
  blob: Blob | null
  /** 稳定标识，用于 Cache 文件名去重 */
  docKey: string
  kind: InvoiceFileKind
}>()

const emit = defineEmits<{
  close: []
  'open-email': []
  download: []
}>()

const pdf = usePdfViewer()
const pdfHost = ref<HTMLElement | null>(null)
pdf.container = pdfHost

watch(
  () => [props.open, props.docKey] as const,
  async ([isOpen]) => {
    if (isOpen && props.kind === 'pdf') await pdf.open(props.blob, props.docKey)
    else pdf.reset()
  },
  // flush:'post' —— 默认的 'pre' 在 DOM 更新前就跑，此时 Teleport 里的
  // pdfHost 还没挂上，container 量不到宽度，首屏会退化成 window.innerWidth。
  { immediate: true, flush: 'post' },
)
</script>

<style scoped>
.overlay {
  position: fixed; inset: 0; z-index: var(--z-sheet);
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
.head h3 { margin: 0; font-size: var(--text-md); }
.icon { background: none; border: none; color: var(--text-primary); cursor: pointer; }
.body { flex: 1; min-height: 240px; overflow: auto; background: var(--bg-subtle); }
.full-img { display: block; width: 100%; height: auto; }
.full-doc { width: 100%; height: 70vh; border: 0; background: #fff; }
.hint, .empty { padding: 32px; text-align: center; color: var(--text-secondary); }
.pager {
  display: flex; align-items: center; justify-content: center; gap: 16px;
  padding: 6px 16px; border-top: 1px solid var(--border);
}
.pg {
  background: none; border: 1px solid var(--border); border-radius: 8px;
  color: var(--text-primary); cursor: pointer; padding: 4px 10px;
}
.pg:disabled { opacity: 0.35; cursor: default; }
.pg-label { font-size: var(--text-smd); color: var(--text-secondary); font-variant-numeric: tabular-nums; }
.foot {
  display: flex; gap: 8px; padding: 12px 16px 20px;
  border-top: 1px solid var(--border);
}
.act {
  flex: 1; padding: 10px; border-radius: 10px; cursor: pointer;
  border: 1px solid var(--border); background: var(--bg-subtle); color: var(--text-primary);
}
</style>
