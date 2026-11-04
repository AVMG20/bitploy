<script setup lang="ts">
import { computed } from 'vue'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { useMe } from '@/composables/queries'
import { addressParts } from '@/lib/slug'

/**
 * The domain part of an app's address, next to the name field. With only the
 * default domain it is plain text (".example.com"), as it always was; when an
 * admin gave the person more domains it is a select. The model is the domain
 * name; "" means the default domain.
 */
const props = defineProps<{
  disabled?: boolean
  /** The app's current domain: stays a choice even when the person may no longer pick it. */
  current?: string
}>()
const model = defineModel<string>({ default: '' })
const me = useMe()

const defaultDomain = computed(() => me.data.value?.app_domains?.find((d) => d.default)?.domain ?? me.data.value?.apps_domain ?? '')
const choices = computed(() => {
  const names = (me.data.value?.app_domains ?? []).map((d) => d.domain)
  if (props.current && !names.includes(props.current)) names.push(props.current)
  return names.map((domain) => ({ domain, after: addressParts(me.data.value, domain).after }))
})
const selected = computed({
  get: () => model.value || defaultDomain.value,
  set: (v: string) => (model.value = v),
})
const after = computed(() => addressParts(me.data.value, model.value).after)
</script>

<template>
  <Select v-if="choices.length > 1" v-model="selected" :disabled="disabled">
    <SelectTrigger class="max-w-[60%] shrink-0 font-mono text-sm" aria-label="Domain">
      <SelectValue />
    </SelectTrigger>
    <SelectContent>
      <SelectItem v-for="c in choices" :key="c.domain" :value="c.domain" class="font-mono">{{ c.after }}</SelectItem>
    </SelectContent>
  </Select>
  <span v-else class="max-w-[50%] shrink-0 truncate font-mono text-sm text-muted-foreground">{{ after }}</span>
</template>
