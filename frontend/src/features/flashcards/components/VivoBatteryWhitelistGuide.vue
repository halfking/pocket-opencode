<template>
  <aside v-if="visible" class="vivo-guide" role="complementary" aria-label="OriginOS 后台保活引导">
    <header class="head">
      <div>
        <h3>让每日复习提醒稳定送达</h3>
        <p class="hint">检测到设备运行 vivo OriginOS；需要开启两项系统设置才能保证定时通知不被后台清理。</p>
      </div>
      <button type="button" class="close" aria-label="关闭" @click="dismiss">×</button>
    </header>

    <ol class="steps">
      <li>
        <span class="num">1</span>
        <div class="body">
          <p class="title">设置 → 电池 → 后台高耗电</p>
          <p class="desc">找到「OpenPocket」并设为「<b>无限制</b>」（较旧 OriginOS 显示为「高后台耗电」）。</p>
        </div>
      </li>
      <li>
        <span class="num">2</span>
        <div class="body">
          <p class="title">返回设置 → 自启动</p>
          <p class="desc">允许「OpenPocket」自启动，确保锁屏或长时间不打开应用时仍能拉起定时提醒。</p>
        </div>
      </li>
      <li>
        <span class="num">3</span>
        <div class="body">
          <p class="title">完成后点击下方按钮</p>
          <p class="desc">我们会将本次引导标记为完成，不再重复弹出。</p>
        </div>
      </li>
    </ol>

    <footer class="actions">
      <button type="button" class="primary" @click="dismiss">我已完成</button>
    </footer>
  </aside>
</template>

<script setup lang="ts">
/**
 * VivoBatteryWhitelistGuide — vivo OriginOS 后台保活引导。
 *
 * 显示条件（全部命中才显示）：
 *   - 平台为 Android（来自 frontend/src/native/runtime-platform.ts）
 *   - navigator.userAgent 含 'vivo' 或 'OriginOS'（启发式）
 *   - localStorage key `openpocket.flashcards.vivoGuideDismissed.v1` 未设置
 *
 * 由 subagent B 的 view 嵌入；本组件本身只渲染提示卡 + 关闭按钮。
 *
 * 涉及触发点（与 localNotifications.onAlarmDenied 协同）：
 *   - 用户首次调用 scheduleDailyReview() 若 SCHEDULE_EXACT_ALARM 被拒，
 *     业务 view 应同时挂载本组件，让用户走一遍 OriginOS 电池白名单。
 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { Capacitor } from '@capacitor/core'
import { onAlarmDenied } from '../../../native/localNotifications'

const STORAGE_KEY = 'openpocket.flashcards.vivoGuideDismissed.v1'

/** Pure helper — expose 给 devtools 调试用（script setup 不能 export，见 defineExpose） */
function isVivoOriginOS(ua: string = (typeof navigator !== 'undefined' ? navigator.userAgent : '')): boolean {
  if (!ua) return false
  // OriginOS WebView 自报 'OriginOS'；旧版 Funtouch 含 'vivo'。大小写都接受。
  return /vivo|OriginOS/i.test(ua)
}

function readDismissed(): boolean {
  try {
    return typeof localStorage !== 'undefined' && localStorage.getItem(STORAGE_KEY) === '1'
  } catch {
    // 隐私模式 / 禁用 localStorage → 当作未关闭，每次进入都提示（保守策略）
    return false
  }
}

function writeDismissed(): void {
  try { localStorage?.setItem(STORAGE_KEY, '1') } catch { /* ignore */ }
}

const dismissed = ref(readDismissed())
const showOnAlarmDenied = ref(false)

const visible = computed(() => {
  if (dismissed.value) return false
  if (typeof Capacitor === 'undefined') return false
  if (Capacitor.getPlatform?.() !== 'android') return false
  if (!isVivoOriginOS()) return false
  return true
})

function dismiss() {
  dismissed.value = true
  writeDismissed()
}

let unsubscribe: (() => void) | null = null
onMounted(() => {
  // 仅在 Android + vivo 上挂 alarm-denied 监听；其它平台跳过节省开销
  if (!visible.value) return
  unsubscribe = onAlarmDenied((reason) => {
    if (reason === 'exact-denied' || reason === 'display-denied') {
      showOnAlarmDenied.value = true
    }
  })
})
onBeforeUnmount(() => {
  unsubscribe?.()
  unsubscribe = null
})

defineExpose({ isVivoOriginOS, dismiss, visible })
</script>

<style scoped>
.vivo-guide {
  margin: var(--space-3);
  padding: var(--space-4);
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.04);
}
.head {
  display: flex;
  align-items: flex-start;
  gap: 8px;
}
.head h3 {
  margin: 0 0 6px;
  font-size: 15px;
  color: var(--text-primary);
}
.hint {
  margin: 0;
  font-size: 12px;
  line-height: 1.55;
  color: var(--text-secondary);
}
.close {
  border: 0;
  background: transparent;
  color: var(--text-secondary);
  font-size: 18px;
  line-height: 1;
  padding: 0 6px;
  cursor: pointer;
}
.steps {
  margin: var(--space-3) 0 var(--space-4);
  padding: 0;
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}
.steps li {
  display: flex;
  gap: 12px;
  align-items: flex-start;
}
.num {
  flex-shrink: 0;
  width: 24px;
  height: 24px;
  border-radius: 999px;
  background: var(--brand-primary, var(--brand-gradient, #4f6df5));
  color: var(--text-inverse, #fff);
  font-size: 12px;
  font-weight: 600;
  display: inline-flex;
  align-items: center;
  justify-content: center;
}
.body .title {
  margin: 0 0 4px;
  font-size: 13px;
  color: var(--text-primary);
  font-weight: 500;
}
.body .desc {
  margin: 0;
  font-size: 12px;
  line-height: 1.55;
  color: var(--text-secondary);
}
.actions {
  display: flex;
  gap: 8px;
  justify-content: flex-end;
}
.primary {
  border: 0;
  background: var(--brand-primary, var(--brand-gradient, #4f6df5));
  color: var(--text-inverse, #fff);
  padding: 8px 16px;
  border-radius: var(--radius-sm);
  font-size: 13px;
  cursor: pointer;
}
</style>