<script setup lang="ts">
import { ref } from "vue"
import TaskBoard from "../features/tasks/TaskBoard.vue"
import TaskDetail from "../features/tasks/TaskDetail.vue"

const currentView = ref<"board" | "detail">("board")
const selectedTaskId = ref<string | null>(null)

function showTaskDetail(taskId: string) {
  selectedTaskId.value = taskId
  currentView.value = "detail"
}

function backToBoard() {
  currentView.value = "board"
  selectedTaskId.value = null
}
</script>

<template>
  <div>
    <TaskBoard
      v-if="currentView === 'board'"
      @view-task="showTaskDetail"
    />
    <TaskDetail
      v-else-if="currentView === 'detail' && selectedTaskId"
      :task-id="selectedTaskId"
      @back="backToBoard"
    />
  </div>
</template>
