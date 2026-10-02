<!--
  SettingsSTT — 语音转写配置（2026-10-01）。

  路由：/settings/stt

  为什么单独一页而不是塞进「AI 网关」：录音转写有自己的约束（长耗时、大体积
  上传、需要成本核算），外部 ASR 服务还有独立的一把 key。混在一起会导致
  「换对话模型」时顺手改掉转写目标。

  三块内容：
  1. 通道：auto（优先网关）/ 仅网关 / 仅外部服务
  2. 网关模型：自动发现结论（每个候选都标了真实探测状态）+ 可手工改
  3. 外部服务：预置 3 个高精度/低成本模型（附成本与精度依据）+ 地址与 key

  关键设计：网关候选**不假装可用**。2026-10-01 实测 llm.kxpms.cn 的
  gpt-audio / gpt-audio-mini / mimo-v2.5-asr 全部 503 no_candidate，
  所以这里展示的是探测结论，不是「已配置可用」。
-->
<template>
  <div class="stt-view">
    <header class="top-bar">
      <button class="back-btn" @click="goBack" aria-label="返回">
        <span class="material-symbols-outlined">arrow_back</span>
      </button>
      <h1 class="title">语音转写</h1>
      <div class="top-spacer"></div>
    </header>

    <div v-if="status" :class="['status-bar', `status-${status.kind}`]" role="status" aria-live="polite">
      {{ status.text }}
    </div>

    <main class="form-container">
      <!-- 1. 通道 -->
      <section class="form-section">
        <label class="form-label" for="stt-channel">转写通道</label>
        <select id="stt-channel" v-model="form.channel" class="form-input">
          <option v-for="c in channelOptions" :key="c.value" :value="c.value">
            {{ c.label }}
          </option>
        </select>
        <div class="form-hint">{{ channelHint }}</div>
        <div class="effective" data-testid="stt-effective">
          <span class="effective-label">当前生效</span>
          <span class="effective-value">{{ effectiveText }}</span>
        </div>
      </section>

      <!-- 2. 网关模型 -->
      <section class="form-section">
        <div class="section-head">
          <label class="form-label" for="stt-gateway-model">网关转写模型</label>
          <button
            type="button"
            class="ghost-btn"
            :disabled="discovering"
            data-testid="stt-discover"
            @click="onDiscover"
          >
            {{ discovering ? '扫描中…' : '重新扫描网关' }}
          </button>
        </div>

        <div v-if="!gatewayHasKey" class="warn-box" data-testid="stt-gateway-nokey">
          网关未配置 API Key。请先在「设置 → AI 模型」配置网关，或改用外部转写服务。
        </div>

        <div v-else-if="discovery && discovery.error" class="warn-box">
          网关扫描失败：{{ discovery.error }}
        </div>

        <template v-else>
          <div class="meta-line" data-testid="stt-gateway-meta">
            {{ gatewayBaseURL }} · 共 {{ discovery?.totalModels ?? 0 }} 个模型 ·
            {{ usableCandidates.length }} 个可用于转写
          </div>

          <ul v-if="discovery?.candidates?.length" class="candidate-list" data-testid="stt-candidates">
            <li
              v-for="c in discovery.candidates"
              :key="c.model"
              class="candidate"
              :class="{ usable: isUsable(c), selected: form.gatewayModel === c.model }"
              :data-testid="`stt-candidate-${c.model}`"
            >
              <label class="candidate-main">
                <input
                  type="radio"
                  name="stt-gateway-model"
                  :value="c.model"
                  v-model="form.gatewayModel"
                  :disabled="!isUsable(c)"
                />
                <span class="candidate-model">{{ c.model }}</span>
                <span :class="['badge', `badge-${c.status}`]">{{ describeStatus(c) }}</span>
              </label>
              <div v-if="candidateDetail(c) && !isUsable(c)" class="candidate-detail">
                {{ candidateDetail(c) }}
              </div>
            </li>
          </ul>
          <div v-else class="form-hint">
            尚未扫描。点「重新扫描网关」会真实出网探测每个候选（限流 12 次/分钟，需数十秒）。
          </div>

          <div class="auto-row">
            <label class="auto-check">
              <input type="radio" name="stt-gateway-model" value="" v-model="form.gatewayModel" />
              <span>自动（用探测到的第一个可用模型）</span>
            </label>
          </div>

          <label class="form-label sub" for="stt-gateway-custom">手工指定网关模型</label>
          <input
            id="stt-gateway-custom"
            v-model.trim="form.gatewayModel"
            class="form-input"
            type="text"
            placeholder="留空 = 自动；也可直接填任意模型名"
            autocapitalize="off"
            autocorrect="off"
            spellcheck="false"
            data-testid="stt-gateway-custom"
          />
        </template>
      </section>

      <!-- 3. 外部服务 -->
      <section class="form-section">
        <label class="form-label" for="stt-ext-base">外部转写服务地址</label>
        <input
          id="stt-ext-base"
          v-model.trim="form.externalBaseURL"
          class="form-input"
          type="text"
          placeholder="https://api.openai.com/v1"
          autocapitalize="off"
          autocorrect="off"
          spellcheck="false"
          data-testid="stt-ext-base"
        />
        <div class="form-hint">任何 OpenAI 兼容 /audio/transcriptions 的服务（OpenAI / Groq / 硅基流动 等）</div>

        <label class="form-label sub" for="stt-ext-key">外部服务 API Key</label>
        <div class="key-row">
          <input
            id="stt-ext-key"
            v-model="form.externalApiKey"
            class="form-input"
            :type="showKey ? 'text' : 'password'"
            :placeholder="form.hasExternalKey ? '已设置（留空保留）' : 'sk-...'"
            autocapitalize="off"
            autocorrect="off"
            spellcheck="false"
            data-testid="stt-ext-key"
          />
          <button class="key-toggle" type="button" :aria-label="showKey ? '隐藏' : '显示'" @click="showKey = !showKey">
            <span aria-hidden="true">{{ showKey ? '🙈' : '👁' }}</span>
          </button>
        </div>
        <div v-if="form.hasExternalKey" class="form-hint">Key 已保存在服务端（不回显）。留空 = 保留。</div>

        <label class="form-label sub" for="stt-ext-model">外部转写模型</label>
        <input
          id="stt-ext-model"
          v-model.trim="form.externalModel"
          class="form-input"
          type="text"
          placeholder="gpt-4o-mini-transcribe"
          autocapitalize="off"
          autocorrect="off"
          spellcheck="false"
          data-testid="stt-ext-model"
        />
      </section>

      <!-- 录音语音提示的可用性：播报失败是静默的，必须让用户看得见 -->
      <section class="form-section">
        <label class="form-label">录音语音提示</label>
        <div class="form-hint">
          录音开始/结束时用扬声器播报一句语音（不是警告声）。播报期间会静音麦克风，
          提示语不会被录进录音内容。
        </div>
        <p class="voice-prompt-state" :class="{ 'is-bad': !voicePrompt.supported }">
          {{ voicePrompt.reason }}
        </p>
      </section>

      <!-- 推荐模型：网关组 + 外部组，分组展示，点一下即选中 -->
      <section class="form-section">
        <label class="form-label">推荐模型</label>
        <div class="form-hint">
          来自 2026-10 网络调研的精度/成本对比。都可手工改成任意模型名。
        </div>

        <template v-for="group in recommendedGroups" :key="group.key">
          <div class="group-head">
            <span class="group-name">{{ group.label }}</span>
          </div>
          <ul class="rec-list">
            <li
              v-for="m in group.models"
              :key="m.model"
              class="rec"
              :data-testid="`stt-rec-${m.model}`"
              @click="pickRecommended(m)"
            >
              <div class="rec-head">
                <span class="rec-model">{{ m.model }}</span>
                <span v-if="m.usdPerHour" class="rec-cost">{{ formatCost(m.usdPerHour) }}</span>
                <span v-else class="rec-cost">无公开报价</span>
              </div>
              <div class="rec-note">{{ m.note }}</div>
              <div
                v-if="streamingHint(m)"
                class="rec-badge"
                :class="{ 'rec-badge--ok': m.streaming }"
              >{{ streamingHint(m) }}</div>
              <div v-if="maxSecondsHint(m.maxSeconds)" class="rec-acc">
                {{ maxSecondsHint(m.maxSeconds) }}
              </div>
              <div v-if="m.accuracy" class="rec-acc">{{ m.accuracy }}</div>
              <div v-if="gatewayStatusOf(m.model)" class="rec-probe">
                网关探测：{{ gatewayStatusOf(m.model) }}
              </div>
            </li>
          </ul>
        </template>
      </section>

      <!-- 试转：用真实录音验证识别质量 -->
      <section class="form-section">
        <label class="form-label">试转验证</label>
        <div class="form-hint">
          自动发现只验证「能不能连上」，识别准不准必须用真实语音判断。录 3 秒再试转。
        </div>
        <div class="probe-row">
          <button
            type="button"
            class="primary-btn"
            :disabled="recording"
            data-testid="stt-probe-record"
            @click="onRecordProbe"
          >
            {{ recording ? '录音中…点击结束' : '录 3 秒试转' }}
          </button>
          <span v-if="probeElapsedMs > 0" class="probe-timer">{{ (probeElapsedMs / 1000).toFixed(1) }}s</span>
        </div>
        <div v-if="probeState === 'running'" class="form-hint">正在转写…</div>
        <div v-else-if="probeState === 'ok'" class="ok-box" data-testid="stt-probe-result">
          <div class="ok-head">
            {{ probeResult?.model }} · {{ probeResult?.label }}
            <span v-if="probeResult?.costCents"> · 约 {{ probeResult.costCents.toFixed(2) }} 美分</span>
          </div>
          <div class="ok-text">{{ probeResult?.text }}</div>
        </div>
        <div v-else-if="probeState === 'error'" class="warn-box" data-testid="stt-probe-error">
          {{ probeError }}
        </div>
      </section>

      <div class="actions">
        <button class="primary-btn" :disabled="saving" data-testid="stt-save" @click="onSave">
          {{ saving ? '保存中…' : '保存' }}
        </button>
      </div>
    </main>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import {
  sttSettingsApi,
  describeSttProbeStatus,
  isSttCandidateUsable,
  type SttChannel,
  type SttConfigResponse,
  type SttDiscoveryResult,
  type SttGatewayCandidate,
  type SttProbeResult,
  type SttRecommendedModel,
} from '../../api/stt-settings'
import { formatCost, maxSecondsHint, streamingHint } from '../../api/stt-presentation'
import { probeVoicePromptSupport } from '../../native/recording-voice-prompt'
import { useApiError } from '../../composables/useApiError'

const router = useRouter()
const apiError = useApiError()

/**
 * 录音语音提示的引擎可用性。
 *
 * 播报失败是静默的（没有 TTS 引擎时 announce 直接返回），用户听不到声音只会
 * 认为功能没做。国内 ROM 常移除 Google TTS，所以这不是理论风险——把结论
 * 摆在设置页，用户才知道是设备问题还是应用问题。
 */
const voicePrompt = probeVoicePromptSupport()

const form = reactive({
  channel: 'auto' as SttChannel,
  gatewayModel: '',
  externalBaseURL: '',
  externalModel: '',
  externalApiKey: '',
  hasExternalKey: false,
})

const recommended = ref<SttRecommendedModel[]>([])
const discovery = ref<SttDiscoveryResult | null>(null)
const gatewayBaseURL = ref('')
const gatewayHasKey = ref(false)
const channelHints = ref<Record<string, string>>({})
const effectiveModel = ref('')
const effectiveNote = ref('')

const showKey = ref(false)
const saving = ref(false)
const discovering = ref(false)
const recording = ref(false)
const probeElapsedMs = ref(0)
const probeState = ref<'idle' | 'running' | 'ok' | 'error'>('idle')
const probeResult = ref<SttProbeResult | null>(null)
const probeError = ref('')

type StatusKind = 'info' | 'success' | 'error'
const status = ref<{ kind: StatusKind; text: string } | null>(null)

function setStatus(kind: StatusKind, text: string, ttl = 6000) {
  status.value = { kind, text }
  if (ttl > 0) {
    setTimeout(() => {
      if (status.value?.text === text) status.value = null
    }, ttl)
  }
}

const channelOptions = computed(() => [
  { value: 'auto' as SttChannel, label: '自动（优先网关，回退外部）' },
  { value: 'gateway' as SttChannel, label: '仅网关' },
  { value: 'external' as SttChannel, label: '仅外部服务' },
])

const channelHint = computed(
  () => channelHints.value[form.channel] || '优先用网关里探测通过的 ASR 模型，没有再退到外部服务',
)

// 外部服务「真的能用」缺哪一项。空串 = 齐了。
//
// 为什么不直接看 form.externalModel：地址和 key 缺一个，外部通道照样转不了，
// 而 2026-10-01 真机上就出现过「明明配了外部服务，页面却写『外部服务未配置』」
// 这种反着说的提示——用户会以为自己没配，去反复检查一个已经配好的东西。
const externalMissing = computed(() => {
  if (!form.externalBaseURL) return '未配置外部转写服务地址'
  if (!form.hasExternalKey && !form.externalApiKey) return '外部服务未配置 API Key'
  if (!form.externalModel) return '未选择外部转写模型'
  return ''
})

const effectiveText = computed(() => {
  if (effectiveModel.value) {
    return effectiveNote.value
      ? `${effectiveModel.value}（${effectiveNote.value}）`
      : effectiveModel.value
  }
  if (form.channel === 'gateway') {
    // 仅网关：外部配得再好也用不上，所以不能说「回退到外部」。
    return '尚未确定（网关暂无可用模型，可点「重新扫描网关」）'
  }
  if (form.channel === 'external') {
    return externalMissing.value || form.externalModel
  }
  // auto：网关没有可用模型时会**回退到外部服务**（后端 resolveSTTTarget 就是
  // 这个顺序），所以这里必须说清会落到哪个模型，而不是笼统一句「尚未确定」。
  if (!externalMissing.value) {
    return `${form.externalModel}（网关暂无可用模型，将回退到外部服务）`
  }
  return `尚未确定（网关暂无可用模型，且${externalMissing.value}）`
})

const usableCandidates = computed(() =>
  (discovery.value?.candidates ?? []).filter(isSttCandidateUsable),
)

const recommendedGroups = computed(() => {
  const gateway = recommended.value.filter((m) => m.group === 'gateway')
  const external = recommended.value.filter((m) => m.group === 'external')
  return [
    { key: 'gateway', label: '网关模型（llm.kxpms.cn 模型目录）', models: gateway },
    { key: 'external', label: '外部服务模型（网络调研推荐）', models: external },
  ].filter((g) => g.models.length > 0)
})

function isUsable(c: SttGatewayCandidate): boolean {
  return isSttCandidateUsable(c)
}

function describeStatus(c: SttGatewayCandidate): string {
  return describeSttProbeStatus(c)
}

/**
 * 候选卡片上的补充说明。
 *
 * 2026-10-01 真机发现：后端 `providerDetail` 把上游 503 的响应体截断到 300 字符
 * 原样回传（`{"error":{"alternatives":{"requested_model":"mimo-v2.5-asr",…}}`），
 * 设置页直接渲染后，一个候选就刷出五六行半截 JSON，把整张卡片撑爆，而且因为
 * 是按字节截断，断在 JSON 中间反而更难读。
 *
 * 徽章已经把「网关无上游 provider」这类**状态**说清楚了，原始响应对用户的
 * 决策没有增量价值（要查细节看服务端日志）。所以这里只放行**人话**细节：
 *  - 整体是 JSON/数组 → 直接不显示（交给徽章）
 *  - 含长 URL / 路径的技术串 → 同样不显示
 *  - 其余短句 → 截到 80 字
 */
function candidateDetail(c: SttGatewayCandidate): string {
  const raw = (c.detail ?? '').trim()
  if (!raw) return ''
  // 上游响应体：{…} / […] 开头，或以 "{" / "[" 结尾（截断在 JSON 中间）
  if (/^[[{]/.test(raw) || /[\]}]$/.test(raw)) return ''
  if (/https?:\/\/\S{40,}/.test(raw)) return ''
  const oneLine = raw.replace(/\s+/g, ' ')
  return oneLine.length > 80 ? oneLine.slice(0, 80) + '…' : oneLine
}

/** 网关预置模型在本次扫描里的真实探测结论。 */
function gatewayStatusOf(model: string): string {
  const hit = (discovery.value?.candidates ?? []).find((c) => c.model === model)
  return hit ? describeSttProbeStatus(hit) : ''
}

function pickRecommended(m: SttRecommendedModel) {
  if (m.group === 'gateway') {
    form.gatewayModel = m.model
  } else {
    form.channel = 'external'
    form.externalModel = m.model
    if (m.baseURL && !form.externalBaseURL) form.externalBaseURL = m.baseURL
  }
}

function applyConfig(cfg: SttConfigResponse) {
  form.channel = cfg.settings.channel || 'auto'
  form.gatewayModel = cfg.settings.gatewayModel || ''
  form.externalBaseURL = cfg.settings.externalBaseURL || ''
  form.externalModel = cfg.settings.externalModel || ''
  form.hasExternalKey = !!cfg.settings.hasExternalKey
  form.externalApiKey = ''
  recommended.value = cfg.recommended ?? []
  discovery.value = cfg.gateway ?? null
  gatewayBaseURL.value = cfg.gatewayBaseURL || ''
  gatewayHasKey.value = !!cfg.gatewayHasKey
  channelHints.value = cfg.channelHints ?? {}
  effectiveModel.value = cfg.settings.effectiveModel || ''
  effectiveNote.value = cfg.settings.effectiveNote || ''
}

onMounted(async () => {
  try {
    applyConfig(await sttSettingsApi.getConfig())
  } catch (err) {
    setStatus('error', `读取语音转写设置失败：${apiError(err, 'errors.loadFailed')}`, 0)
  }
})

async function onDiscover() {
  discovering.value = true
  setStatus('info', '正在扫描网关并逐个探测候选模型（可能需要几十秒）…', 0)
  try {
    discovery.value = await sttSettingsApi.discover()
    const usable = usableCandidates.value.length
    setStatus(
      usable > 0 ? 'success' : 'error',
      usable > 0
        ? `扫描完成：${usable} 个模型可用于转写`
        : `扫描完成：网关 ${discovery.value.totalModels} 个模型里没有可用的语音转写模型`,
      0,
    )
  } catch (err) {
    setStatus('error', `扫描失败：${apiError(err, 'errors.gatewayUnreachable')}`, 0)
  } finally {
    discovering.value = false
  }
}

async function onSave() {
  saving.value = true
  try {
    const saved = await sttSettingsApi.saveConfig({
      channel: form.channel,
      gatewayModel: form.gatewayModel,
      externalBaseURL: form.externalBaseURL,
      externalModel: form.externalModel,
      externalTransport: 'auto',
      externalApiKey: form.externalApiKey || undefined,
    })
    form.hasExternalKey = !!saved.hasExternalKey
    form.externalApiKey = ''
    setStatus('success', '已保存', 3000)
  } catch (err) {
    setStatus('error', `保存失败：${apiError(err, 'errors.saveFailed')}`, 0)
  } finally {
    saving.value = false
  }
}

// ---- 试转：录 3 秒真实语音 ----

let mediaRecorder: MediaRecorder | null = null
let chunks: BlobPart[] = []
let probeTimer: ReturnType<typeof setInterval> | null = null
let probeDeadline: ReturnType<typeof setTimeout> | null = null

function clearProbeTimers() {
  if (probeTimer) clearInterval(probeTimer)
  if (probeDeadline) clearTimeout(probeDeadline)
  probeTimer = null
  probeDeadline = null
}

async function onRecordProbe() {
  if (recording.value) {
    mediaRecorder?.stop()
    return
  }
  try {
    const stream = await navigator.mediaDevices.getUserMedia({ audio: true })
    const mime = MediaRecorder.isTypeSupported('audio/webm') ? 'audio/webm' : ''
    mediaRecorder = new MediaRecorder(stream, mime ? { mimeType: mime } : undefined)
    chunks = []
    mediaRecorder.ondataavailable = (e) => {
      if (e.data.size > 0) chunks.push(e.data)
    }
    mediaRecorder.onstop = () => {
      stream.getTracks().forEach((t) => t.stop())
      recording.value = false
      clearProbeTimers()
      const blob = new Blob(chunks, { type: mediaRecorder?.mimeType || 'audio/webm' })
      void runProbe(blob)
    }
    mediaRecorder.start()
    recording.value = true
    probeState.value = 'idle'
    probeError.value = ''
    probeElapsedMs.value = 0
    probeTimer = setInterval(() => {
      probeElapsedMs.value += 100
    }, 100)
    // 3 秒自动停：让「试转」这件事不需要用户精确掌握时长。
    probeDeadline = setTimeout(() => mediaRecorder?.stop(), 3000)
  } catch (err) {
    recording.value = false
    clearProbeTimers()
    probeState.value = 'error'
    probeError.value = `无法录音：${apiError(err, 'errors.micDenied')}`
  }
}

async function runProbe(blob: Blob) {
  probeState.value = 'running'
  probeResult.value = null
  probeError.value = ''
  try {
    // 先按当前表单值试（不落盘），用户改完模型能立刻看到效果。
    const res = await sttSettingsApi.probe(blob, {
      channel: form.channel,
      ...(form.channel === 'external'
        ? { baseURL: form.externalBaseURL, model: form.externalModel }
        : { model: form.gatewayModel }),
    })
    if (res.ok) {
      probeState.value = 'ok'
      probeResult.value = res
    } else {
      probeState.value = 'error'
      probeError.value = res.error || '试转失败（上游没有返回原因）'
    }
  } catch (err) {
    probeState.value = 'error'
    probeError.value = `试转失败：${apiError(err, 'errors.gatewayUnreachable')}`
  }
}

onBeforeUnmount(() => {
  clearProbeTimers()
  if (recording.value) mediaRecorder?.stop()
})

function goBack() {
  router.back()
}
</script>

<style scoped>
.stt-view {
  min-height: 100vh;
  background: var(--bg, #f6f7f9);
  color: var(--text, #1c1c1e);
}
.top-bar {
  position: sticky;
  top: 0;
  z-index: 10;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 12px 16px;
  background: var(--surface, #fff);
  border-bottom: 1px solid var(--border, #e3e5e8);
}
.title {
  flex: 1;
  /* 与 SettingsLLMGateway / SettingsView / SettingsPermissionsView 的页头对齐。
     原来这里是 18px、那边是 16px，同一个 top-bar 里的同一个 h1 在两个设置
     子页渲染出不同字号；而且两处都写死 px，token 体系管不到它们。 */
  font-size: var(--text-lg);
  font-weight: 600;
  color: var(--text-primary);
  margin: 0;
}
.top-spacer {
  width: 40px;
}
.back-btn {
  background: none;
  border: none;
  cursor: pointer;
  padding: 4px;
}
.form-container {
  padding: 16px 16px 96px;
  display: flex;
  flex-direction: column;
  gap: 20px;
  max-width: 720px;
  margin: 0 auto;
}
.form-section {
  background: var(--surface, #fff);
  border-radius: 12px;
  padding: 16px;
  border: 1px solid var(--border, #e3e5e8);
}
.form-label {
  display: block;
  font-size: var(--text-base);
  font-weight: 600;
  margin-bottom: 8px;
}
.form-label.sub {
  margin-top: 16px;
  font-weight: 500;
}
.form-input {
  width: 100%;
  box-sizing: border-box;
  padding: 10px 12px;
  font-size: var(--text-md);
  border-radius: 8px;
  border: 1px solid var(--border, #d0d3d8);
  background: var(--surface, #fff);
  color: inherit;
}
.form-hint {
  /* 走 token：渲染值不变（--text-sm 就是 12px），与 SettingsLLMGateway /
     SettingsPermissionsView 的说明文字同源。此前三处一个写 12px、一个写
     var(--text-xs)、一个写 var(--text-sm)，同一角色两个值。 */
  font-size: var(--text-sm);
  color: var(--text-secondary, #6b7280);
  margin-top: 6px;
  line-height: 1.5;
}
/* 语音提示可用性：不可用时用警示色，让「听不到声音」有据可查。 */
.voice-prompt-state {
  margin: 6px 0 0;
  font-size: var(--text-sm);
  line-height: 1.5;
  color: var(--success, #16a34a);
}
.voice-prompt-state.is-bad {
  color: var(--danger, #dc2626);
}
.section-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 8px;
}
.section-head .form-label {
  margin-bottom: 0;
}
.ghost-btn {
  background: none;
  border: 1px solid var(--border, #d0d3d8);
  border-radius: 8px;
  padding: 6px 10px;
  font-size: var(--text-smd);
  cursor: pointer;
}
.ghost-btn:disabled {
  opacity: 0.5;
}
.meta-line {
  font-size: var(--text-sm);
  color: var(--text-secondary, #6b7280);
  margin-bottom: 8px;
  word-break: break-all;
}
.effective {
  margin-top: 12px;
  padding: 10px 12px;
  border-radius: 8px;
  background: var(--bg, #f0f2f5);
  font-size: var(--text-smd);
}
.effective-label {
  color: var(--text-secondary, #6b7280);
  margin-right: 8px;
}
.effective-value {
  font-weight: 600;
}
.candidate-list,
.rec-list {
  list-style: none;
  margin: 8px 0 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.candidate {
  border: 1px solid var(--border, #e3e5e8);
  border-radius: 8px;
  padding: 10px 12px;
}
.candidate.usable {
  border-color: #2f9e5e;
}
.candidate.selected {
  background: rgba(47, 158, 94, 0.08);
}
.candidate-main {
  display: flex;
  align-items: center;
  gap: 8px;
  cursor: pointer;
  font-size: var(--text-base);
}
.candidate-model {
  font-family: var(--font-mono);
  word-break: break-all;
}
.candidate-detail {
  font-size: var(--text-sm);
  color: #b45309;
  margin-top: 6px;
  word-break: break-all;
  /* 双保险：即便 candidateDetail 漏判，detail 也最多占两行，不会把卡片撑爆。 */
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  overflow: hidden;
}
.badge {
  margin-left: auto;
  font-size: var(--text-2xs);
  padding: 2px 8px;
  border-radius: 999px;
  background: #e5e7eb;
  white-space: nowrap;
}
.badge-ok {
  background: #d1fae5;
  color: #065f46;
}
.badge-no_provider,
.badge-audio_ignored,
.badge-failed {
  background: #fee2e2;
  color: #991b1b;
}
.auto-row {
  margin-top: 10px;
}
.auto-check {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: var(--text-smd);
  cursor: pointer;
}
.key-row {
  display: flex;
  gap: 8px;
  align-items: center;
}
.key-toggle {
  background: none;
  border: none;
  cursor: pointer;
  font-size: var(--text-xl);
}
.group-head {
  margin-top: 14px;
  margin-bottom: 4px;
}
.group-name {
  font-size: var(--text-smd);
  font-weight: 600;
  color: var(--text-secondary, #6b7280);
}
.rec {
  border: 1px solid var(--border, #e3e5e8);
  border-radius: 8px;
  padding: 10px 12px;
  cursor: pointer;
}
.rec:hover {
  border-color: var(--accent, #2563eb);
}
.rec-head {
  display: flex;
  align-items: baseline;
  gap: 8px;
}
.rec-model {
  font-family: var(--font-mono);
  font-size: var(--text-smd);
  word-break: break-all;
}
.rec-cost {
  margin-left: auto;
  font-size: var(--text-sm);
  color: var(--text-secondary, #6b7280);
  white-space: nowrap;
}
/* 即时出字能力标记：让「省钱 vs 逐字」的取舍在选模型时就看得见。 */
.rec-badge {
  display: inline-block;
  margin-top: 2px;
  padding: 1px 6px;
  border: 1px solid var(--border, #e5e7eb);
  border-radius: 999px;
  font-size: var(--text-2xs);
  color: var(--text-secondary, #6b7280);
  width: fit-content;
}
.rec-badge--ok {
  border-color: var(--success, #16a34a);
  color: var(--success, #16a34a);
}
.rec-note,
.rec-acc,
.rec-probe {
  font-size: var(--text-sm);
  color: var(--text-secondary, #6b7280);
  margin-top: 4px;
  line-height: 1.5;
}
.rec-probe {
  color: #b45309;
}
.warn-box {
  background: #fff7ed;
  border: 1px solid #fdba74;
  color: #9a3412;
  border-radius: 8px;
  padding: 10px 12px;
  font-size: var(--text-smd);
  line-height: 1.5;
  margin-top: 8px;
  word-break: break-word;
}
.ok-box {
  background: #ecfdf5;
  border: 1px solid #6ee7b7;
  border-radius: 8px;
  padding: 10px 12px;
  margin-top: 8px;
}
.ok-head {
  font-size: var(--text-sm);
  color: #065f46;
  margin-bottom: 6px;
}
.ok-text {
  font-size: var(--text-base);
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-word;
}
.probe-row {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-top: 10px;
}
.probe-timer {
  font-size: var(--text-smd);
  color: var(--text-secondary, #6b7280);
}
.primary-btn {
  background: var(--accent, #2563eb);
  color: #fff;
  border: none;
  border-radius: 8px;
  padding: 12px 20px;
  font-size: var(--text-md);
  cursor: pointer;
}
.primary-btn:disabled {
  opacity: 0.6;
}
.actions {
  position: sticky;
  bottom: 0;
  padding: 12px 0;
}
.status-bar {
  padding: 10px 16px;
  font-size: var(--text-smd);
  line-height: 1.5;
  word-break: break-word;
}
.status-info {
  background: #eff6ff;
  color: #1d4ed8;
}
.status-success {
  background: #ecfdf5;
  color: #065f46;
}
.status-error {
  background: #fef2f2;
  color: #991b1b;
}
</style>
