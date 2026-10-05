<template>
  <!--
    新建/编辑日程事件的底部面板。

    放在页面内而不是独立路由：新建日历条目的最短路径是「选一天 → 填标题 →
    保存」，跳路由会打断这个动作，也会丢掉刚选中的日期。
  -->
  <div class="sheet-backdrop" @click.self="close">
    <section class="sheet" role="dialog" :aria-label="title" @keydown.esc="close">
      <h2 class="sheet-title">{{ title }}</h2>

      <label class="field">
        <span class="field-label">{{ t('calendar.field.title') }}</span>
        <input
          ref="titleInput"
          v-model="form.title"
          type="text"
          :placeholder="t('calendar.placeholder.title')"
          maxlength="120"
        />
      </label>

      <label class="field">
        <span class="field-label">{{ t('calendar.field.start') }}</span>
        <input v-model="form.start" type="datetime-local" />
      </label>

      <label class="field">
        <span class="field-label">{{ t('calendar.field.end') }}</span>
        <input v-model="form.end" type="datetime-local" />
      </label>

      <label class="field checkbox">
        <input v-model="form.allDay" type="checkbox" />
        <span>{{ t('calendar.field.allDay') }}</span>
      </label>

      <label class="field">
        <span class="field-label">{{ t('calendar.field.location') }}</span>
        <input v-model="form.location" type="text" maxlength="120" />
      </label>

      <label class="field">
        <span class="field-label">{{ t('calendar.field.note') }}</span>
        <textarea v-model="form.description" rows="2" maxlength="1000"></textarea>
      </label>

      <p v-if="store.error" class="error" role="alert">
        {{ apiError(store.error, 'errors.saveCalendarFailed') }}
      </p>

      <div class="sheet-actions">
        <button type="button" class="ghost" @click="close">{{ t('common.cancel') }}</button>
        <button type="button" class="primary" :disabled="!canSave || store.saving" @click="save">
          {{ store.saving ? t('common.loading') : t('common.save') }}
        </button>
      </div>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useCalendarStore } from './store'
import type { CalendarEntry, CalendarEventInput } from './types'
import {
  deviceTimeZone,
  fromLocalInputValue,
  toLocalInputValue,
  unixAtLocalDayKey,
  type DayKey,
} from './calendar-math.ts'
import { useApiError } from '../../composables/useApiError'

const props = defineProps<{
  /** 打开时预填的日期（来自月视图选中格）。 */
  initialDay: DayKey
  /** 传入即为编辑模式。 */
  event?: CalendarEntry | null
}>()

const emit = defineEmits<{ (e: 'close'): void }>()

const { t } = useI18n()
const apiError = useApiError()
const store = useCalendarStore()
const titleInput = ref<HTMLInputElement | null>(null)

const tz = deviceTimeZone()

// 默认落在选中日的下一个整点：新建日程最常见的意图是「今天下午/明天开会」，
// 默认 09:00 落在上午会让人多改一次。
function defaultStart(day: DayKey): number {
  const nowUnix = Math.floor(Date.now() / 1000)
  const candidate = unixAtLocalDayKey(day, 9, 0, tz)
  return candidate > nowUnix ? candidate : unixAtLocalDayKey(day, 14, 0, tz)
}

const form = ref({
  title: '',
  start: toLocalInputValue(defaultStart(props.initialDay), tz),
  end: '',
  allDay: false,
  location: '',
  description: '',
})

const title = computed(() =>
  props.event ? t('calendar.action.editEvent') : t('calendar.action.newEvent'),
)

const canSave = computed(() => form.value.title.trim().length > 0)

onMounted(() => {
  if (props.event && props.event.source === 'event') {
    const e = props.event
    form.value = {
      title: e.title,
      start: toLocalInputValue(e.startAt, tz),
      end: toLocalInputValue(e.endAt || e.startAt, tz),
      allDay: e.allDay,
      location: e.location ?? '',
      description: e.description ?? '',
    }
  }
  // 直接聚焦标题：新建日程时用户几乎总是第一个碰标题输入框。
  titleInput.value?.focus()
})

async function save(): Promise<void> {
  const startAt = fromLocalInputValue(form.value.start, tz)
  if (!startAt) return
  const endAt = form.value.allDay
    ? startAt
    : Math.max(startAt, fromLocalInputValue(form.value.end, tz))

  const input: CalendarEventInput = {
    title: form.value.title.trim(),
    description: form.value.description.trim(),
    location: form.value.location.trim(),
    startAt,
    endAt,
    allDay: form.value.allDay,
    timezone: tz,
    visibility: 'private',
  }

  const ok = props.event
    ? await store.updateEvent(props.event.refId, input)
    : await store.createEvent(input)
  if (ok) emit('close')
}

function close(): void {
  store.clearError()
  emit('close')
}
</script>

<style scoped>
.sheet-backdrop {
  position: fixed;
  /* 键盘弹起时视口高度不变（overlay 路径），若锚视口，底部「保存」会落到
     键盘下面点不到。改成让出键盘高度，与 BottomSheet.vue 同款做法。 */
  inset: 0 0 var(--kb-inset, 0px) 0;
  background: var(--overlay);
  display: flex;
  align-items: flex-end;
  justify-content: center;
  z-index: 40;
}

.sheet {
  width: 100%;
  max-width: 520px;
  max-height: 88vh;
  overflow-y: auto;
  background: var(--bg-elevated);
  border-radius: 16px 16px 0 0;
  padding: 16px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.sheet-title { margin: 0; font-size: 1rem; }

.field { display: flex; flex-direction: column; gap: 4px; }
.field.checkbox { flex-direction: row; align-items: center; gap: 8px; }
.field-label { font-size: 0.75rem; color: var(--text-secondary); }

.field input[type='text'],
.field input[type='datetime-local'],
.field textarea {
  /* 用 token 而不是硬编码 16px（check:styles 会拦「数值等于 token 却写死」）：
     iOS Safari 在小于 16px 时会自动放大页面，日历输入框会抖 */
  font-size: var(--text-lg);
  padding: 8px;
  border-radius: 8px;
  border: 1px solid var(--border-strong);
  background: var(--bg-card);
  color: var(--text-primary);
  font-family: inherit;
}

.sheet-actions { display: flex; gap: 8px; justify-content: flex-end; margin-top: 4px; }

.ghost, .primary {
  min-height: 42px;
  padding: 0 18px;
  border-radius: 10px;
  font-size: 0.9rem;
  cursor: pointer;
}

.ghost { background: transparent; border: 1px solid var(--border-strong); color: var(--text-primary); }
.primary { background: var(--brand-primary); border: none; color: var(--text-inverse); }
.primary:disabled { opacity: 0.5; cursor: not-allowed; }

.error { color: var(--error); font-size: 0.82rem; margin: 0; }
</style>