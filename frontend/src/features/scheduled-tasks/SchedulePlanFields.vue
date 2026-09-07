<template>
  <fieldset class="plan">
    <legend>执行计划</legend>
    <div class="chips">
      <button
        v-for="item in MODES"
        :key="item.value"
        type="button"
        :class="{ active: plan.mode === item.value }"
        @click="setMode(item.value)"
      >{{ item.label }}</button>
    </div>
    <p v-if="plan.custom" class="custom-hint" role="status">当前是不常见的自定义计划。改下面选项后会换成常用设置。</p>

    <div v-if="plan.mode === 'once'" class="grid">
      <label>日期<input v-model="plan.date" type="date" required @change="clearCustom" /></label>
      <label>时间<input v-model="plan.time" type="time" required @change="clearCustom" /></label>
    </div>

    <template v-else>
      <div class="chips wrap">
        <button
          v-for="item in FREQS"
          :key="item.value"
          type="button"
          :class="{ active: plan.repeatKind === item.value }"
          @click="setRepeat(item.value)"
        >{{ item.label }}</button>
      </div>
      <div v-if="plan.repeatKind === 'weekly'" class="chips wrap" role="group" aria-label="星期">
        <button
          v-for="day in WEEK_DAYS"
          :key="day.value"
          type="button"
          :class="{ active: plan.weekdays.includes(day.value) }"
          @click="toggleWeekday(day.value)"
        >{{ day.label }}</button>
      </div>
      <label v-if="plan.repeatKind === 'monthly'">每月几号
        <input v-model.number="plan.monthDay" type="number" min="1" max="31" @change="clearCustom" />
      </label>
      <div v-if="plan.repeatKind === 'interval'" class="grid">
        <label>每隔
          <input v-model.number="plan.intervalValue" type="number" min="1" @change="clearCustom" />
        </label>
        <label>单位
          <select v-model="plan.intervalUnit" @change="clearCustom">
            <option value="m">分钟</option>
            <option value="h">小时</option>
            <option value="d">天</option>
          </select>
        </label>
      </div>
      <label v-else>时间<input v-model="plan.time" type="time" required @change="clearCustom" /></label>
    </template>

    <p class="summary" aria-live="polite">{{ describeSchedule(plan) }}</p>
    <slot />
  </fieldset>
</template>

<script setup lang="ts">
import { describeSchedule, type RepeatKind, type SchedulePlan, type PlanMode } from './schedule-plan'

const plan = defineModel<SchedulePlan>({ required: true })

const MODES: Array<{ value: PlanMode; label: string }> = [
  { value: 'once', label: '一次性' },
  { value: 'repeat', label: '周期性' },
]
const FREQS: Array<{ value: RepeatKind; label: string }> = [
  { value: 'daily', label: '每天' },
  { value: 'weekdays', label: '工作日' },
  { value: 'weekly', label: '每周' },
  { value: 'monthly', label: '每月' },
  { value: 'interval', label: '每隔一段时间' },
]
const WEEK_DAYS = [
  { value: 1, label: '一' },
  { value: 2, label: '二' },
  { value: 3, label: '三' },
  { value: 4, label: '四' },
  { value: 5, label: '五' },
  { value: 6, label: '六' },
  { value: 0, label: '日' },
]

function clearCustom() {
  plan.value.custom = false
}
function setMode(mode: PlanMode) {
  plan.value.mode = mode
  clearCustom()
}
function setRepeat(kind: RepeatKind) {
  plan.value.repeatKind = kind
  clearCustom()
}
function toggleWeekday(day: number) {
  clearCustom()
  const next = plan.value.weekdays.includes(day)
    ? plan.value.weekdays.filter((item) => item !== day)
    : [...plan.value.weekdays, day]
  plan.value.weekdays = next.length ? next.sort((a, b) => a - b) : [day]
}
</script>

<style scoped>
.plan { border: 1px solid var(--border); border-radius: var(--radius-md); padding: var(--space-3); display: flex; flex-direction: column; gap: var(--space-2); }
legend { font-size: 13px; font-weight: 600; color: var(--text-secondary); }
label { display: flex; flex-direction: column; gap: 6px; font-size: 13px; font-weight: 600; color: var(--text-secondary); }
input, select { width: 100%; box-sizing: border-box; padding: 10px 12px; border: 1px solid var(--border); border-radius: var(--radius-sm); background: var(--bg-card); color: var(--text-primary); font: inherit; font-size: 14px; }
.chips { display: flex; gap: 7px; }
.chips.wrap { flex-wrap: wrap; }
.chips button { flex: 1; padding: 9px 12px; border: 1px solid var(--border); border-radius: var(--radius-sm); background: var(--bg-card); color: var(--text-primary); cursor: pointer; }
.chips.wrap button { flex: none; min-width: 44px; }
.chips button.active { background: var(--brand-primary); color: var(--text-inverse); border-color: var(--brand-primary); }
.grid { display: grid; grid-template-columns: 1fr 1fr; gap: var(--space-3); }
.summary { margin: 0; font-size: 13px; font-weight: 600; color: var(--text-primary); }
.custom-hint { margin: 0; font-size: 12px; color: var(--text-muted); }
</style>
