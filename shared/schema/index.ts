export interface PocketTaskSummary {
  id: string
  title: string
  status: string
  priority: string
  sessionCount?: number
  pendingApprovals?: number
}

export interface PocketSessionResumeBrief {
  instanceId: string
  sessionId: string
  taskId: string
  title: string
  currentState: string
  nextAction: string
}
