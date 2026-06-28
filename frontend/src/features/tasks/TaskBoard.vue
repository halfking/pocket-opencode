<script setup lang="ts">
import { ref, onMounted, onUnmounted } from "vue"
import { api, type Task } from "../../api/client"
import wsClient from "../../api/websocket"

const emit = defineEmits<{
  viewTask: [taskId: string]
}>()

const tasks = ref<Task[]>([])
const loading = ref(true)
const error = ref<string | null>(null)

// Create task modal state
const showCreateModal = ref(false)
const newTask = ref({
  id: "",
  title: "",
  description: "",
  status: "active",
  priority: "medium",
})
const createError = ref<string | null>(null)
const creating = ref(false)

async function loadTasks() {
  loading.value = true
  error.value = null
  try {
    tasks.value = await api.getTasks()
  } catch (e: any) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}

function openCreateModal() {
  showCreateModal.value = true
  newTask.value = {
    id: `task-${Date.now()}`,
    title: "",
    description: "",
    status: "active",
    priority: "medium",
  }
  createError.value = null
}

function closeCreateModal() {
  showCreateModal.value = false
  createError.value = null
}

async function handleCreateTask() {
  if (!newTask.value.title) {
    createError.value = "Title is required"
    return
  }

  creating.value = true
  createError.value = null

  try {
    await api.createTask(newTask.value)
    await loadTasks()
    closeCreateModal()
  } catch (e: any) {
    createError.value = e.message
  } finally {
    creating.value = false
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

onMounted(() => {
  loadTasks()
  
  // WebSocket 实时更新
  wsClient.on('task_created', (task: Task) => {
    const exists = tasks.value.find(t => t.id === task.id)
    if (!exists) {
      tasks.value.unshift(task)
    }
  })
  
  wsClient.on('task_updated', (task: Task) => {
    const index = tasks.value.findIndex(t => t.id === task.id)
    if (index >= 0) {
      tasks.value[index] = task
    }
  })
  
  wsClient.on('session_attached', (link: any) => {
    const task = tasks.value.find(t => t.id === link.taskId)
    if (task) {
      task.sessionCount = (task.sessionCount || 0) + 1
    }
  })
})

onUnmounted(() => {
  // 清理 WebSocket 监听器
  wsClient.off('task_created', () => {})
  wsClient.off('task_updated', () => {})
  wsClient.off('session_attached', () => {})
})
</script>

<template>
  <div class="max-w-4xl mx-auto p-6">
    <header class="mb-6">
      <h1 class="text-3xl font-bold mb-2">OpenCode Pocket</h1>
      <p class="text-gray-600">Task-centric control plane for multiple OpenCode instances</p>
    </header>

    <div class="mb-4 flex items-center justify-between">
      <h2 class="text-xl font-semibold">Tasks</h2>
      <div class="flex gap-3">
        <button
          @click="openCreateModal"
          class="px-4 py-2 bg-green-600 text-white rounded-lg hover:bg-green-700"
        >
          Create Task
        </button>
        <button
          @click="loadTasks"
          class="px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700"
        >
          Refresh
        </button>
      </div>
    </div>

    <div v-if="loading" class="text-center py-12">
      <div class="text-gray-500">Loading tasks...</div>
    </div>

    <div v-else-if="error" class="bg-red-50 border border-red-200 rounded-lg p-4">
      <p class="text-red-800">{{ error }}</p>
    </div>

    <div v-else-if="tasks.length === 0" class="text-center py-12">
      <div class="text-gray-400 mb-4">
        <svg class="w-16 h-16 mx-auto mb-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z" />
        </svg>
        <p class="text-lg">No tasks yet</p>
        <p class="text-sm">Click "Create Task" to get started</p>
      </div>
    </div>

    <div v-else class="space-y-4">
      <div
        v-for="task in tasks"
        :key="task.id"
        @click="emit('viewTask', task.id)"
        class="bg-white rounded-lg shadow-sm border border-gray-200 p-5 hover:shadow-md transition-shadow cursor-pointer"
      >
        <div class="flex items-start justify-between mb-3">
          <div class="flex-1">
            <h3 class="text-lg font-semibold text-gray-900">{{ task.title }}</h3>
            <p v-if="task.description" class="text-sm text-gray-600 mt-1">{{ task.description }}</p>
          </div>
          <span
            :class="['ml-4 px-3 py-1 rounded-full text-xs font-medium', getStatusColor(task.status)]"
          >
            {{ task.status }}
          </span>
        </div>

        <div class="flex items-center gap-4 text-sm">
          <div class="flex items-center gap-1">
            <span class="text-gray-500">Priority:</span>
            <span :class="['font-medium', getPriorityColor(task.priority)]">{{ task.priority }}</span>
          </div>
          <div class="flex items-center gap-1">
            <svg class="w-4 h-4 text-gray-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M8 7V3m8 4V3m-9 8h10M5 21h14a2 2 0 002-2V7a2 2 0 00-2-2H5a2 2 0 00-2 2v12a2 2 0 002 2z" />
            </svg>
            <span class="text-gray-600">{{ task.sessionCount }} sessions</span>
          </div>
          <div v-if="task.pendingApprovals > 0" class="flex items-center gap-1">
            <svg class="w-4 h-4 text-yellow-500" fill="currentColor" viewBox="0 0 20 20">
              <path fill-rule="evenodd" d="M8.257 3.099c.765-1.36 2.722-1.36 3.486 0l5.58 9.92c.75 1.334-.213 2.98-1.742 2.98H4.42c-1.53 0-2.493-1.646-1.743-2.98l5.58-9.92zM11 13a1 1 0 11-2 0 1 1 0 012 0zm-1-8a1 1 0 00-1 1v3a1 1 0 002 0V6a1 1 0 00-1-1z" clip-rule="evenodd" />
            </svg>
            <span class="text-yellow-700 font-medium">{{ task.pendingApprovals }} pending</span>
          </div>
        </div>

        <div class="mt-3 text-xs text-gray-400">
          Updated {{ new Date(task.updatedAt).toLocaleString() }}
        </div>
      </div>
    </div>

    <!-- Create Task Modal -->
    <div
      v-if="showCreateModal"
      class="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center z-50"
      @click.self="closeCreateModal"
    >
      <div class="bg-white rounded-lg shadow-xl max-w-md w-full mx-4 p-6">
        <h3 class="text-lg font-semibold mb-4">Create New Task</h3>

        <div v-if="createError" class="mb-4 bg-red-50 border border-red-200 rounded p-3 text-sm text-red-800">
          {{ createError }}
        </div>

        <div class="space-y-4">
          <div>
            <label class="block text-sm font-medium text-gray-700 mb-1">Title *</label>
            <input
              v-model="newTask.title"
              type="text"
              placeholder="e.g., Implement user authentication"
              class="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-blue-500"
              @keyup.enter="handleCreateTask"
            />
          </div>

          <div>
            <label class="block text-sm font-medium text-gray-700 mb-1">Description</label>
            <textarea
              v-model="newTask.description"
              rows="3"
              placeholder="Optional description..."
              class="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-blue-500"
            />
          </div>

          <div class="grid grid-cols-2 gap-4">
            <div>
              <label class="block text-sm font-medium text-gray-700 mb-1">Status</label>
              <select
                v-model="newTask.status"
                class="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-blue-500"
              >
                <option value="active">Active</option>
                <option value="completed">Completed</option>
                <option value="blocked">Blocked</option>
              </select>
            </div>

            <div>
              <label class="block text-sm font-medium text-gray-700 mb-1">Priority</label>
              <select
                v-model="newTask.priority"
                class="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-blue-500"
              >
                <option value="high">High</option>
                <option value="medium">Medium</option>
                <option value="low">Low</option>
              </select>
            </div>
          </div>
        </div>

        <div class="flex gap-3 mt-6">
          <button
            @click="closeCreateModal"
            :disabled="creating"
            class="flex-1 px-4 py-2 border border-gray-300 text-gray-700 rounded-lg hover:bg-gray-50 disabled:opacity-50"
          >
            Cancel
          </button>
          <button
            @click="handleCreateTask"
            :disabled="creating || !newTask.title"
            class="flex-1 px-4 py-2 bg-green-600 text-white rounded-lg hover:bg-green-700 disabled:opacity-50"
          >
            {{ creating ? "Creating..." : "Create" }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>
