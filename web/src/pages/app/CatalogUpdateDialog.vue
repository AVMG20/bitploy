<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { Archive, Check, Loader2 } from 'lucide-vue-next'
import RelativeTime from '@/components/RelativeTime.vue'
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { REFRESH_MS, keys, livePoll, useBackups } from '@/composables/queries'
import { api } from '@/lib/api'
import { toastError, toastOk } from '@/lib/notify'
import type { App } from '@/lib/types'

// Updating a ready-made app to the catalog's newer version. A new version may
// convert the saved files so the old one cannot read them again, so the dialog
// offers a backup right here instead of only telling you to make one.
const props = defineProps<{ app: App }>()
const open = defineModel<boolean>('open', { required: true })
const emit = defineEmits<{ updated: [] }>()
const qc = useQueryClient()
const router = useRouter()
const slug = computed(() => props.app.slug)
const to = computed(() => props.app.catalog_update ?? '')

const backups = useBackups(slug)
const latestBackup = computed(() => backups.data.value?.items.find((b) => b.status === 'ready') ?? null)
const backingUp = computed(() => !!backups.data.value?.busy || !!backups.data.value?.items.some((b) => b.status === 'running'))
// A backup made in the last 10 minutes counts as "just made".
const freshBackup = computed(() => !!latestBackup.value && Date.now() - new Date(latestBackup.value.created_at).getTime() < 10 * 60_000)

const deployments = useQuery({
  queryKey: computed(() => keys.deployments(slug.value)),
  queryFn: () => api.deployments(slug.value),
  refetchInterval: livePoll(REFRESH_MS),
  enabled: open,
})
const deploying = computed(() => (deployments.data.value ?? []).some((d) => !['running', 'failed', 'superseded', 'cancelled'].includes(d.state)))

const backup = useMutation({
  mutationFn: () => api.createBackup(slug.value),
  onSuccess: () => {
    void qc.invalidateQueries({ queryKey: keys.backups(slug.value) })
    void qc.invalidateQueries({ queryKey: keys.me })
  },
  onError: (e) => toastError(e),
})

const update = useMutation({
  mutationFn: () => api.catalogUpdate(slug.value),
  onSuccess: () => {
    open.value = false
    emit('updated')
    toastOk('Update started', 'A new version is being built. The current one keeps running until it is ready.')
    void qc.invalidateQueries({ queryKey: keys.dockerfile(slug.value) })
    void qc.invalidateQueries({ queryKey: keys.app(slug.value) })
    void qc.invalidateQueries({ queryKey: keys.deployments(slug.value) })
    void qc.invalidateQueries({ queryKey: keys.apps })
    void router.push({ name: 'app-deployments', params: { slug: slug.value } })
  },
  onError: (e) => {
    open.value = false
    toastError(e)
    void qc.invalidateQueries({ queryKey: keys.app(slug.value) })
  },
})

const blocked = computed(() => update.isPending.value || deploying.value || backingUp.value || backup.isPending.value)
</script>

<template>
  <AlertDialog v-model:open="open">
    <AlertDialogContent>
      <AlertDialogHeader>
        <AlertDialogTitle>Update to {{ to }}?</AlertDialogTitle>
        <AlertDialogDescription>
          Bitploy replaces the Dockerfile with the catalog's version {{ to }} and deploys it. Changes you made to the Dockerfile are lost.
          <template v-if="app.volume"> Saved files are kept, but a new version may convert them so the old one cannot read them again.</template>
        </AlertDialogDescription>
      </AlertDialogHeader>

      <div v-if="app.volume" class="flex flex-wrap items-center gap-3 rounded-lg border bg-muted/30 p-3 text-sm">
        <div class="min-w-0 flex-1">
          <p class="font-medium">{{ freshBackup ? 'Backup made' : 'Make a backup first' }}</p>
          <p class="text-xs text-muted-foreground">
            <template v-if="backingUp">Backing up your saved files...</template>
            <template v-else-if="latestBackup">Latest backup: <RelativeTime :date="latestBackup.created_at" />. You can restore it in Storage.</template>
            <template v-else>There is no backup yet.</template>
          </p>
        </div>
        <Check v-if="freshBackup && !backingUp" class="size-4 text-success" aria-hidden="true" />
        <Button v-else size="sm" variant="outline" :disabled="backingUp || backup.isPending.value" @click="backup.mutate()">
          <Loader2 v-if="backingUp || backup.isPending.value" class="animate-spin" /><Archive v-else />
          {{ backingUp || backup.isPending.value ? 'Backing up...' : 'Back up now' }}
        </Button>
      </div>

      <p v-if="deploying" class="text-sm text-muted-foreground">A deployment is in progress. You can update when it is done.</p>

      <AlertDialogFooter>
        <AlertDialogCancel :disabled="update.isPending.value">Cancel</AlertDialogCancel>
        <Button :disabled="blocked" @click="update.mutate()">{{ update.isPending.value ? 'Starting...' : `Update to ${to}` }}</Button>
      </AlertDialogFooter>
    </AlertDialogContent>
  </AlertDialog>
</template>
