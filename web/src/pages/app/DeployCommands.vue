<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useMutation, useQueryClient } from '@tanstack/vue-query'
import FormCard from '@/components/FormCard.vue'
import SettingsSection from '@/components/SettingsSection.vue'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { keys } from '@/composables/queries'
import { api } from '@/lib/api'
import { deployCommandError } from '@/lib/deployCommands'
import { toastError, toastOk } from '@/lib/notify'
import type { App, UpdateAppInput } from '@/lib/types'

// The before- and after-deploy commands of an app (Settings, "Deploy
// commands"). Read by the next deploy, not by the running app.
const props = defineProps<{ app: App }>()
const qc = useQueryClient()

const before = ref('')
const after = ref('')
const savedBefore = computed(() => props.app.before_deploy_command ?? '')
const savedAfter = computed(() => props.app.after_deploy_command ?? '')
let loadedFor = ''
watch(
  () => [props.app.id, savedBefore.value, savedAfter.value] as const,
  ([id, b, a]) => {
    // Follow the saved commands, but never wipe what someone is typing during a poll.
    if (loadedFor !== `${id}:${b}:${a}`) {
      loadedFor = `${id}:${b}:${a}`
      before.value = b
      after.value = a
    }
  },
  { immediate: true },
)

const beforeError = computed(() => deployCommandError(before.value))
const afterError = computed(() => deployCommandError(after.value))
const changed = computed(() => before.value.trim() !== savedBefore.value || after.value.trim() !== savedAfter.value)

const save = useMutation({
  mutationFn: () => {
    const input: UpdateAppInput = {}
    if (before.value.trim() !== savedBefore.value) input.before_deploy_command = before.value.trim()
    if (after.value.trim() !== savedAfter.value) input.after_deploy_command = after.value.trim()
    return api.updateApp(props.app.slug, input)
  },
  onSuccess: (updated) => {
    qc.setQueryData(keys.app(updated.slug), updated)
    void qc.invalidateQueries({ queryKey: keys.app(updated.slug) })
    const any = !!updated.before_deploy_command || !!updated.after_deploy_command
    toastOk('Deploy commands saved', any ? 'They run from the next deploy on.' : 'No commands run on the next deploy.')
  },
  onError: (e) => toastError(e),
})
</script>

<template>
  <SettingsSection id="deploy-commands" class="scroll-mt-16" title="Deploy commands" description="Commands bitploy runs each time you deploy new code, such as updating your database.">
    <form novalidate @submit.prevent="save.mutate()">
      <FormCard
        title="Before and after a deploy"
        description="They run in your app's own image, with its variables, its database connection and its saved files (/data)."
      >
        <div class="grid gap-5">
          <div class="grid gap-2">
            <Label for="deploy-before">Before the new version goes live</Label>
            <Textarea
              id="deploy-before"
              v-model="before"
              class="font-mono"
              rows="2"
              spellcheck="false"
              autocapitalize="off"
              placeholder="php artisan migrate --force"
              :aria-invalid="!!beforeError"
              :disabled="save.isPending.value"
            />
            <p class="text-sm text-muted-foreground">
              For database migrations, like <span class="font-mono">php artisan migrate --force</span>, <span class="font-mono">python manage.py migrate</span> or
              <span class="font-mono">npx prisma migrate deploy</span>. If the command fails or takes longer than 10 minutes, the
              deploy stops and the current version stays online.
            </p>
            <p v-if="beforeError" class="text-sm text-destructive">{{ beforeError }}</p>
          </div>
          <div class="grid gap-2">
            <Label for="deploy-after">After the new version is live</Label>
            <Textarea
              id="deploy-after"
              v-model="after"
              class="font-mono"
              rows="2"
              spellcheck="false"
              autocapitalize="off"
              placeholder="php artisan cache:clear"
              :aria-invalid="!!afterError"
              :disabled="save.isPending.value"
            />
            <p class="text-sm text-muted-foreground">
              Runs in the new version once visitors reach it, for example to clear a cache (up to 5 minutes). If it fails, the new version stays online and the deployment
              shows a warning.
            </p>
            <p v-if="afterError" class="text-sm text-destructive">{{ afterError }}</p>
          </div>
        </div>
        <template #hint>Applies on the next deploy. Their output is in the deployment's log. Going back to an earlier version runs neither.</template>
        <template #actions>
          <Button type="submit" :disabled="!changed || !!beforeError || !!afterError || save.isPending.value">{{ save.isPending.value ? 'Saving...' : 'Save commands' }}</Button>
        </template>
      </FormCard>
    </form>
  </SettingsSection>
</template>
