<template>
  <BottomSheet :open="open" title="会议设置" @close="$emit('close')">
    <div class="meta">
      <label class="field">
        <span>标题</span>
        <div class="row">
          <input v-model="form.title" placeholder="自动生成或手动输入" />
          <button type="button" class="ghost" @click="onCaptureTitle">抓取</button>
        </div>
      </label>
      <label class="field">
        <span>主题</span>
        <input v-model="form.topic" placeholder="本场会议主题" />
      </label>
      <label class="field">
        <span>地点</span>
        <div class="row">
          <input v-model="form.location" placeholder="会议室 / 线上 / 地址" />
          <button type="button" class="ghost" :disabled="locating" @click="onCaptureLocation">
            {{ locating ? '定位…' : '自动' }}
          </button>
        </div>
      </label>
      <label class="field">
        <span>参与人</span>
        <input v-model="form.participants" placeholder="逗号分隔，如：张三, 李四" />
      </label>
      <!--
        §119：告诉用户上面这几个字段**真的**被用上了。
        用户原始诉求是「录音转写的不太准确」，而准确率能由他自己改一把 ——
        但此前界面上**一个字都没提**，他既不知道填了有用，也不知道不填会怎样。

        ⚠ 这段话**只能写 metaTermHint 真正读到的字段**（§30 的不对称原则：
           说错比不说更糟）。门 `meeting-meta-glossary.test.ts` 会把这条
           对齐关系钉住，双向都钉：真读到的必须提，后端没声明的必须不提。

        ⚠ 2026-10-07（§159）**改口**：本段此前写的是「它读 Title /
           Participants / Location，**不读 Topic** ⇒ 这里提「主题」就是撒谎」，
           并据此把 Topic 列进测试的 `UI_ONLY_LABEL`。
           **那条结论已过期**：`meetingMetaIn.Topic` 早已接入（§138.3/§143），
           Go 侧契约夹具现在是 `used=[location participants title topic]`、
           `unused=[]` ⇒ metaTermHint **确实读** Topic。
           ⇒ 方向反转：主题从「不许提」变成「**必须提**」。
        ⚠ 参与人那一句是**有实测依据**的，不是客套：
           §64.6 配对实验量到过模型把转写里正确的人名「对齐」进名单，
           把对的改成错的；提示词第二道禁令已堵，但界面也该把话说在前头。
        ⚠ 「写进标题或主题都能生效」这句的实测依据：§146 的 A/B
           （3 臂 × 3 次交错，真网关）读数 TITLE 3/3、TOPIC 3/3 ——
           两条路一样好，所以**不能**说主题更好，只能说都行。
      -->
      <p class="glossary-note">
        标题、主题、地点、参与人会作为术语表参与录音后的精校：转写里与它们明显对应的错词会被纠正。
        产品名、项目名写进标题或主题都能生效。
        参与人只用于纠正对应的人名，不在转写里的人不会被加进去。
      </p>
      <label class="field">
        <span>标签</span>
        <div class="row">
          <input v-model="form.tags" placeholder="周会, 预算" />
          <button type="button" class="ghost" @click="onSuggestTags">提取</button>
        </div>
      </label>
      <div class="field">
        <span>总结技能</span>
        <div class="chips">
          <button
            v-for="s in MEETING_SKILLS"
            :key="s.id"
            type="button"
            class="chip"
            :class="{ active: form.summarySkill === s.id }"
            @click="form.summarySkill = s.id"
          >{{ s.label }}</button>
        </div>
        <p class="hint">{{ meetingSkillById(form.summarySkill).hint }}</p>
      </div>
      <button type="button" class="save" @click="onSave">保存</button>
    </div>
  </BottomSheet>
</template>

<script setup lang="ts">
import { reactive, ref, watch } from 'vue'
import { BottomSheet } from '@/components'
import { MEETING_SKILLS, meetingSkillById } from './meeting-skills'
import { captureDeviceLocation, formatCapturedTitle, parseTagInput, suggestMeetingTags } from './meeting-meta'

const props = defineProps<{
  open: boolean
  title?: string | null
  topic?: string | null
  location?: string | null
  participants?: string[]
  tags?: string[]
  summarySkill?: string | null
  startedAt?: number
  transcriptHint?: string
}>()

const emit = defineEmits<{
  close: []
  save: [data: {
    title: string
    topic: string
    location: string
    participants: string[]
    tags: string[]
    summarySkill: string
  }]
}>()

const locating = ref(false)
const form = reactive({
  title: '', topic: '', location: '', participants: '', tags: '', summarySkill: 'meeting-minutes',
})

watch(() => props.open, (v) => {
  if (!v) return
  form.title = props.title ?? ''
  form.topic = props.topic ?? ''
  form.location = props.location ?? ''
  form.participants = (props.participants ?? []).join(', ')
  form.tags = (props.tags ?? []).join(', ')
  form.summarySkill = props.summarySkill || 'meeting-minutes'
})

function onCaptureTitle() {
  form.title = formatCapturedTitle({
    startedAt: props.startedAt ?? Date.now(),
    location: form.location,
    topic: form.topic,
    firstUtterance: props.transcriptHint,
  })
}

async function onCaptureLocation() {
  locating.value = true
  try {
    const loc = await captureDeviceLocation()
    if (loc) form.location = loc
  } finally {
    locating.value = false
  }
}

function onSuggestTags() {
  const extra = suggestMeetingTags([form.title, form.topic, props.transcriptHint].filter(Boolean).join(' '))
  form.tags = parseTagInput(`${form.tags},${extra.join(',')}`).join(', ')
}

function onSave() {
  // ⚠⚠ §835/D1：这里此前在 emit('save') 之后**紧跟着 emit('close')**。
  //   Vue 的 emit 是**同步**的，父组件的 `onMetaSave` 是 async 且**没人 await**
  //   ⇒ 面板在写库结果出来之前就关了，form 随之销毁；而父侧那行
  //   `await updateMeeting(...)` 没有 try/catch ⇒ 写失败时是零反馈，
  //   **用户刚填的内容已经从屏幕上消失了**（失败与成功完全不可区分）。
  //   ⇒ 关闭权交给父组件：它只在**写库成功后**才关，失败时面板留着让用户重试。
  emit('save', {
    title: form.title.trim(),
    topic: form.topic.trim(),
    location: form.location.trim(),
    participants: parseTagInput(form.participants.replace(/#/g, '')),
    tags: parseTagInput(form.tags),
    summarySkill: form.summarySkill,
  })
}
</script>

<style scoped>
.meta { padding: 0 var(--space-1) var(--space-3); }
.field { display: flex; flex-direction: column; gap: 6px; margin-bottom: var(--space-3); }
.field > span { font-size: var(--text-sm); color: var(--text-muted); }
.field input {
  flex: 1; padding: 10px 12px; border: 1px solid var(--border); border-radius: var(--radius-md);
  background: var(--bg-base); color: var(--text-primary); font-size: var(--text-base);
}
.row { display: flex; gap: 8px; }
.ghost {
  flex-shrink: 0; padding: 0 10px; border-radius: var(--radius-md);
  border: 1px solid var(--border); background: var(--bg-subtle); font-size: var(--text-sm);
}
.chips { display: flex; flex-wrap: wrap; gap: 6px; }
.chip {
  padding: 6px 12px; border-radius: 999px; border: 1px solid var(--border);
  background: var(--bg-card); color: var(--text-secondary); font-size: var(--text-smd);
}
.chip.active { background: var(--brand-bg); color: var(--brand-primary); border-color: var(--brand-primary); }
.hint { margin: 0; font-size: var(--text-sm); color: var(--text-muted); }
/* §119：术语表说明。放在「参与人」与「标签」之间，对应上面那三个字段。 */
.glossary-note { margin: -2px 0 12px; font-size: var(--text-xs); line-height: 1.6; color: var(--text-muted); }
.save {
  width: 100%; padding: 12px; border: none; border-radius: var(--radius-md);
  background: var(--brand-primary); color: var(--text-inverse); font-weight: 600;
}
</style>
