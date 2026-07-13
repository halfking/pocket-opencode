<!-- S2.3 联系人详情：联系人资料 + 邮件时间线。 -->
<template>
  <AppLayout>
    <div v-if="loading" class="state">加载中…</div>
    <div v-else-if="!contact" class="state">联系人不存在</div>
    <div v-else class="detail-page">
      <header class="profile">
        <div class="avatar">{{ initials(contact.displayName) }}</div>
        <h1>{{ contact.displayName }}</h1>
        <p>{{ contact.email }}</p>
        <p v-if="contact.organization">{{ contact.organization }}{{ contact.title ? ` · ${contact.title}` : '' }}</p>
      </header>

      <section class="card">
        <h2>联系时间线</h2>
        <p v-if="emails.length === 0" class="muted">暂无关联邮件</p>
        <ul v-else class="timeline">
          <li v-for="email in emails" :key="email.id" @click="router.push(`/email/${email.id}`)">
            <span class="dot"></span>
            <div>
              <strong>{{ email.subject || '(无主题)' }}</strong>
              <p>{{ email.snippet || '无摘要' }}</p>
              <small>{{ formatDate(email.date) }}</small>
            </div>
          </li>
        </ul>
      </section>

      <button class="secondary" @click="router.push('/contacts')">返回联系人</button>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AppLayout from '../../app/AppLayout.vue'
import { getContact, getContactEmails, type Contact } from './contacts-store'
import type { LocalEmail } from '../email/emails-store'

const route = useRoute()
const router = useRouter()
const contact = ref<Contact | null>(null)
const emails = ref<LocalEmail[]>([])
const loading = ref(true)

async function load() {
  loading.value = true
  try {
    contact.value = await getContact(route.params.id as string)
    if (contact.value) emails.value = await getContactEmails(contact.value)
  } finally { loading.value = false }
}

function initials(name: string) { return name.trim().slice(0, 2).toUpperCase() || '?' }
function formatDate(ts: number) {
  const date = new Date(ts)
  return `${date.getFullYear()}/${date.getMonth() + 1}/${date.getDate()} ${String(date.getHours()).padStart(2, '0')}:${String(date.getMinutes()).padStart(2, '0')}`
}

onMounted(load)
</script>

<style scoped>
.detail-page { padding: 20px 16px 96px; }
.profile { text-align: center; margin-bottom: 20px; }
.avatar { width: 68px; height: 68px; margin: 0 auto 10px; display: grid; place-items: center; border-radius: 50%; background: var(--brand-primary); color: white; font-size: 22px; font-weight: 700; }
h1 { margin: 0; font-size: 22px; }
.profile p { margin: 4px 0; color: var(--text-secondary); font-size: 12px; }
.card { padding: 14px; background: var(--bg-card); border-radius: 11px; box-shadow: var(--shadow-sm); }
h2 { margin: 0 0 10px; font-size: 16px; }
.muted, .state { color: var(--text-secondary); }
.state { padding: 48px; text-align: center; }
.timeline { list-style: none; padding: 0; margin: 0; }
.timeline li { display: flex; gap: 9px; padding: 11px 0; border-top: 1px solid var(--border); cursor: pointer; }
.dot { width: 8px; height: 8px; margin-top: 5px; border-radius: 50%; background: var(--brand-primary); flex-shrink: 0; }
.timeline div { min-width: 0; }
.timeline strong, .timeline p, .timeline small { display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.timeline p { margin: 4px 0; color: var(--text-secondary); font-size: 12px; }
.timeline small { color: var(--text-muted); font-size: 10px; }
.secondary { width: 100%; margin-top: 14px; padding: 10px; border: 1px solid var(--border); border-radius: 8px; background: var(--bg-card); cursor: pointer; }
</style>
