<!-- S2.3 联系人列表：从本地邮件发件人聚合联系人。 -->
<template>
  <AppLayout>
    <div class="contacts-page">
      <header class="page-header">
        <div>
          <h1>联系人</h1>
          <p>邮件、会议和任务中的联系人</p>
        </div>
        <button class="primary" :disabled="syncing" @click="sync">{{ syncing ? '同步中…' : '↻ 聚合' }}</button>
      </header>

      <input v-model="query" class="search" placeholder="搜索姓名、邮箱或组织…" />

      <div v-if="loading" class="state">加载中…</div>
      <div v-else-if="filtered.length === 0" class="empty">
        <div>👥</div>
        <p>暂无联系人</p>
        <button class="primary" @click="sync">从邮件生成联系人</button>
      </div>
      <ul v-else class="contact-list">
        <li v-for="contact in filtered" :key="contact.id" @click="open(contact.id)">
          <div class="avatar">{{ initials(contact.displayName) }}</div>
          <div class="main">
            <strong>{{ contact.displayName }}</strong>
            <span>{{ contact.email }}</span>
            <small v-if="contact.organization">{{ contact.organization }}{{ contact.title ? ` · ${contact.title}` : '' }}</small>
          </div>
          <span class="count">{{ contact.sourceEmailIds.length }} 封</span>
        </li>
      </ul>
      <p v-if="message" class="message">{{ message }}</p>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import AppLayout from '../../app/AppLayout.vue'
import { useAuthStore } from '../../stores/auth'
import { listContacts, syncContactsFromEmails, type Contact } from './contacts-store'

const router = useRouter()
const auth = useAuthStore()
const workspaceId = auth.workspaceId || 'default'
const contacts = ref<Contact[]>([])
const query = ref('')
const loading = ref(true)
const syncing = ref(false)
const message = ref('')

const filtered = computed(() => {
  const q = query.value.trim().toLowerCase()
  if (!q) return contacts.value
  return contacts.value.filter((contact) =>
    [contact.displayName, contact.email, contact.organization, contact.title]
      .filter(Boolean)
      .some((value) => String(value).toLowerCase().includes(q)),
  )
})

async function load() {
  loading.value = true
  try { contacts.value = await listContacts(workspaceId) }
  finally { loading.value = false }
}

async function sync() {
  syncing.value = true
  message.value = ''
  try {
    contacts.value = await syncContactsFromEmails(workspaceId)
    message.value = `已聚合 ${contacts.value.length} 位联系人`
  } catch (error: any) {
    message.value = error?.message || '聚合失败，请先同步邮件'
  } finally {
    syncing.value = false
  }
}

function open(id: string) { router.push(`/contacts/${id}`) }
function initials(name: string) { return name.trim().slice(0, 2).toUpperCase() || '?' }

onMounted(load)
</script>

<style scoped>
.contacts-page { padding: 16px; padding-bottom: 96px; }
.page-header { display: flex; justify-content: space-between; gap: 10px; align-items: flex-start; margin-bottom: 15px; }
h1 { margin: 0; font-size: 24px; }
.page-header p { margin: 5px 0 0; font-size: 12px; color: var(--text-secondary); }
.primary { border: 0; border-radius: 8px; background: var(--brand-primary); color: #fff; padding: 9px 12px; cursor: pointer; font-size: 12px; }
.primary:disabled { opacity: .55; }
.search { width: 100%; box-sizing: border-box; padding: 10px 12px; border: 1px solid var(--border); border-radius: 9px; margin-bottom: 12px; background: var(--bg-card); }
.contact-list { list-style: none; padding: 0; margin: 0; display: grid; gap: 7px; }
.contact-list li { display: flex; align-items: center; gap: 10px; padding: 11px; background: var(--bg-card); border-radius: 11px; cursor: pointer; }
.avatar { width: 38px; height: 38px; border-radius: 50%; display: grid; place-items: center; background: var(--brand-primary); color: #fff; font-size: 13px; font-weight: 700; flex-shrink: 0; }
.main { min-width: 0; flex: 1; display: grid; gap: 3px; }
.main strong, .main span, .main small { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.main span, .main small { color: var(--text-secondary); font-size: 11px; }
.count { color: var(--text-muted); font-size: 11px; }
.state, .empty { color: var(--text-secondary); text-align: center; padding: 45px 12px; }
.empty div { font-size: 42px; }
.message { color: var(--text-secondary); font-size: 12px; text-align: center; }
</style>
