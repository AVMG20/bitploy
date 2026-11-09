<script setup lang="ts">
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import type { ChannelFormProps } from './channelTypes'

defineProps<ChannelFormProps>()
const config = defineModel<Record<string, unknown>>({ required: true })
</script>

<template>
  <div class="grid gap-2">
    <Label :for="`${idPrefix}-webhook`">Webhook URL</Label>
    <Input
      :id="`${idPrefix}-webhook`"
      type="password"
      autocomplete="off"
      spellcheck="false"
      class="font-mono"
      :placeholder="existing?.webhook_id ? 'Paste a new URL to replace the webhook' : 'https://discord.com/api/webhooks/...'"
      :model-value="String(config.webhook_url ?? '')"
      :aria-invalid="error ? true : undefined"
      @update:model-value="(v) => (config = { ...config, webhook_url: String(v) })"
    />
    <p v-if="error" class="text-sm text-destructive">{{ error }}</p>
    <p v-else-if="existing?.webhook_id" class="text-sm text-muted-foreground">Sends to webhook <span class="font-mono">{{ existing.webhook_id }}</span>.</p>
    <p class="text-sm text-muted-foreground">
      In Discord: Server settings → Integrations → Webhooks → New webhook, pick the channel, then Copy webhook URL.
    </p>
  </div>
</template>
