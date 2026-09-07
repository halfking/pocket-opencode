<template>
  <div class="state"><Skeleton :count="2" /></div>
</template>

<script setup lang="ts">
import { onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { Skeleton } from '@/components'
import { createMeeting } from './meetings-store'
import { captureDeviceLocation, formatCapturedTitle } from './meeting-meta'

const router = useRouter()

onMounted(async () => {
  try {
    const startedAt = Date.now()
    const location = await captureDeviceLocation()
    const meeting = await createMeeting({
      startedAt,
      location: location ?? undefined,
      title: formatCapturedTitle({ startedAt, location }),
    })
    await router.replace({
      name: 'meeting-detail',
      params: { id: meeting.id },
      query: { record: '1' },
    })
  } catch {
    await router.replace({ name: 'meetings' })
  }
})
</script>

<style scoped>
.state { padding: var(--space-3); }
</style>
