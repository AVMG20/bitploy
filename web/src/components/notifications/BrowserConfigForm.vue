<script setup lang="ts">
import { computed } from 'vue'
import { browserName, pushBlockedText } from '@/lib/notifications'
import type { ChannelFormProps } from './channelTypes'

const props = defineProps<ChannelFormProps>()
// The subscription is made when the dialog is saved, not typed in.
defineModel<Record<string, unknown>>({ required: true })
const blocked = computed(() => pushBlockedText(props.pushState, browserName(navigator.userAgent)))
</script>

<template>
  <div class="grid gap-2 text-sm">
    <template v-if="existing">
      <p v-if="current">This is the browser you are using now.</p>
      <p v-else class="text-muted-foreground">Another browser or device. To change which browser gets these, open bitploy there.</p>
    </template>
    <template v-else>
      <p v-if="error" class="text-destructive">{{ error }}</p>
      <p v-else-if="blocked" class="text-destructive">{{ blocked }}</p>
      <p v-else class="text-muted-foreground">Notifications appear on this device, also when no bitploy tab is open. Your browser asks for permission first.</p>
    </template>
  </div>
</template>
