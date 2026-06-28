<script setup lang="ts">
import { ref, onMounted, computed } from "vue"
import { api, type Task, type SessionLink, type Instance } from "../../api/client"

const props = defineProps<{
  taskId: string
}>()

const emit = defineEmits<{
  back: []
}>()

const task = ref<Task | null>(null)
const sessions = ref<SessionLink[]>([])
const loading = ref(true)
const error = ref<string | null>(null)

// Attach session modal state
const showAttachModal = ref(false)
const instances = ref<Instance[]>([])
const selectedInstanceId = ref<string>("")
const selectedSessionId = ref<string>("")
const selectedRole = ref<string>("primary")
const attachError = ref<string | null>(null)
const attaching = ref(false)

async function loadTaskDetail() {
  loading.value = true
  error.value = null
  try {
    task.value = await api.getTask(props.taskId)
    sessions.value = await api.getTaskSessions(props.taskId)
  } catch (e: any) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}

async function openAttachModal() {
  showAttachModal.value = true
  attachError.value = null
  try {
    instances.value = await api.getInstances()
    if (instances.value.length > 0) {
      selectedInstanceId.value = instances.value[0].id
    }
  } catch (e: any) {
    attachError.value = `Failed to load instances: ${e.message}`
  }
}

function closeAttachModal() {
  showAttachModal.value = false
  selectedInstanceId.value = ""
  selectedSessionId.value = ""
  selectedRole.value = "primary"
  attachError.value = null
}

async function handleAttachSession() {
  if (!selectedInstanceId.value || !selectedSessionId.value) {
    attachError.value = "Please select both instance and session"
    return
  }

  attaching.value = true
  attachError.value = null

  try {
    await api.attachSession(props.taskId, selectedInstanceId.value, selectedSessionId.value, selectedRole.value)
    await loadTaskDetail()
    closeAttachModal()
  } catch (e: any) {
    attachError.value = e.message
  } finally {
    attaching.value = false
  }
}

function getStatusColor(status: string): string {
  switch (status) {
    case "active":
      return "bg-blue-100 text-blue-800"
    case "completed":
      return "bg-green-100 text-green-800"
    case "blocked":
      return "bg-red-100 text-red-800"
    default:
      return "bg-gray-100 text-gray-800"
  }
}

function getPriorityColor(priority: string): string {
  switch (priority) {
    case "high":
      return "text-red-600"
    case "medium":
      return "text-yellow-600"
    case "low":
      return "text-green-600"
    default:
      return "text-gray-600"
  }
}

function getRoleBadge(role: string): string {
  switch (role) {
    case "primary":
      return "bg-blue-100 text-blue-700"
    case "supporting":
      return "bg-green-100 text-green-700"
    case "exploratory":
      return "bg-purple-100 text-purple-700"
    case "duplicate":
      return "bg-gray-100 text-gray-700"
    default:
      return "bg-gray-100 text-gray-700"
  }
}

onMounted(() => {
  loadTaskDetail()
})
</script>

<template>
  <div class="max-w-4xl mx-auto p-6">
    <button
      @click="emit('back')"
      class="mb-6 flex items-center gap-2 text-blue-600 hover:text-blue-800"
    >
      <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 19l-7-7 7-7" />
      </svg>
      Back to Tasks
    </button>

    <div v-if="loading" class="text-center py-12">
      <div class="text-gray-500">Loading task details...</div>
    </div>

    <div v-else-if="error" class="bg-red-50 border border-red-200 rounded-lg p-4">
      <p class="text-red-800">{{ error }}</p>
    </div>

    <div v-else-if="task" class="space-y-6">
      <!-- Task Header -->
      <div class="bg-white rounded-lg shadow-sm border border-gray-200 p-6">
        <div class="flex items-start justify-between mb-4">
          <div class="flex-1">
            <h1 class="text-2xl font-bold text-gray-900 mb-2">{{ task.title }}</h1>
            <p v-if="task.description" class="text-gray-600">{{ task.description }}</p>
          </div>
          <span
            :class="['ml-4 px-3 py-1 rounded-full text-sm font-medium', getStatusColor(task.status)]"
          >
            {{ task.status }}
          </span>
        </div>

        <div class="grid grid-cols-2 md:grid-cols-4 gap-4 pt-4 border-t border-gray-100">
          <div>
            <div class="text-sm text-gray-500 mb-1">Priority</div>
            <div :class="['font-semibold', getPriorityColor(task.priority)]">{{ task.priority }}</div>
          </div>
          <div>
            <div class="text-sm text-gray-500 mb-1">Sessions</div>
            <div class="font-semibold text-gray-900">{{ task.sessionCount }}</div>
          </div>
          <div>
            <div class="text-sm text-gray-500 mb-1">Pending Approvals</div>
            <div class="font-semibold text-gray-900">{{ task.pendingApprovals }}</div>
          </div>
          <div>
            <div class="text-sm text-gray-500 mb-1">Updated</div>
            <div class="text-sm text-gray-700">{{ new Date(task.updatedAt).toLocaleDateString() }}</div>
          </div>
        </div>
      </div>

      <!-- Attached Sessions -->
      <div class="bg-white rounded-lg shadow-sm border border-gray-200 p-6">
        <h2 class="text-lg font-semibold mb-4">Attached Sessions</h2>

        <div v-if="sessions.length === 0" class="text-center py-8 text-gray-400">
          <svg class="w-12 h-12 mx-auto mb-3" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z" />
          </svg>
          <p>No sessions attached yet</p>
          <p class="text-sm mt-1">Click "Attach Session" below to add one</p>
        </div>

        <div v-else class="space-y-3">
          <div
            v-for="session in sessions"
            :key="`${session.instanceId}-${session.sessionId}`"
            class="flex items-center justify-between p-4 bg-gray-50 rounded-lg border border-gray-200"
          >
            <div class="flex-1">
              <div class="font-medium text-gray-900">Session {{ session.sessionId }}</div>
              <div class="text-sm text-gray-600 mt-1">Instance: {{ session.instanceId }}</div>
            </div>
            <span :class="['px-3 py-1 rounded-full text-xs font-medium', getRoleBadge(session.role)]">
              {{ session.role }}
            </span>
          </div>
        </div>
      </div>

      <!-- Quick Actions -->
      <div class="bg-gray-50 rounded-lg border border-gray-200 p-4">
        <h3 class="text-sm font-semibold text-gray-700 mb-3">Quick Actions</h3>
        <div class="flex gap-3">
          <button
            @click="loadTaskDetail"
            class="px-4 py-2 bg-white border border-gray-300 text-gray-700 rounded-lg hover:bg-gray-50"
          >
            Refresh
          </button>
          <button
            @click="openAttachModal"
            class="px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700"
          >
            Attach Session
          </button>
        </div>
      </div>
    </div>

    <!-- Attach Session Modal -->
    <div
      v-if="showAttachModal"
      class="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center z-50"
      @click.self="closeAttachModal"
    >
      <div class="bg-white rounded-lg shadow-xl max-w-md w-full mx-4 p-6">
        <h3 class="text-lg font-semibold mb-4">Attach Session to Task</h3>

        <div v-if="attachError" class="mb-4 bg-red-50 border border-red-200 rounded p-3 text-sm text-red-800">
          {{ attachError }}
        </div>

        <div class="space-y-4">
          <div>
            <label class="block text-sm font-medium text-gray-700 mb-1">Instance</label>
            <select
              v-model="selectedInstanceId"
              class="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-blue-500"
            >
              <option value="">Select an instance</option>
              <option v-for="instance in instances" :key="instance.id" :value="instance.id">
                {{ instance.displayName }}
              </option>
            </select>
          </div>

          <div>
            <label class="block text-sm font-medium text-gray-700 mb-1">Session ID</label>
            <input
              v-model="selectedSessionId"
              type="text"
              placeholder="e.g., sess-001"
              class="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-blue-500"
            />
            <p class="text-xs text-gray-500 mt-1">Enter the OpenCode session ID</p>
          </div>

          <div>
            <label class="block text-sm font-medium text-gray-700 mb-1">Role</label>
            <select
              v-model="selectedRole"
              class="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-blue-500"
            >
              <option value="primary">Primary</option>
              <option value="supporting">Supporting</option>
              <option value="exploratory">Exploratory</option>
              <option value="duplicate">Duplicate</option>
            </select>
          </div>
        </div>

        <div class="flex gap-3 mt-6">
          <button
            @click="closeAttachModal"
            :disabled="attaching"
            class="flex-1 px-4 py-2 border border-gray-300 text-gray-700 rounded-lg hover:bg-gray-50 disabled:opacity-50"
          >
            Cancel
          </button>
          <button
            @click="handleAttachSession"
            :disabled="attaching || !selectedInstanceId || !selectedSessionId"
            class="flex-1 px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 disabled:opacity-50"
          >
            {{ attaching ? "Attaching..." : "Attach" }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>
