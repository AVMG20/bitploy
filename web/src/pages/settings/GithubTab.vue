<script setup lang="ts">
import { ref } from 'vue'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { useForm } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import { Github } from 'lucide-vue-next'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import FormScope from '@/components/FormScope.vue'
import QueryError from '@/components/QueryError.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import SettingsSection from '@/components/SettingsSection.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardFooter } from '@/components/ui/card'
import { FormControl, FormField, FormItem, FormLabel, FormMessage } from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { keys } from '@/composables/queries'
import { api } from '@/lib/api'
import { exactTime } from '@/lib/format'
import { toastError, toastOk } from '@/lib/notify'

const qc = useQueryClient()
const github = useQuery({ queryKey: keys.github, queryFn: api.githubStatus })
const gh = useForm({
  validationSchema: toTypedSchema(z.object({ token: z.string().trim().min(10, 'Paste the full token from GitHub.') })),
  initialValues: { token: '' },
})
const saveGh = useMutation({
  mutationFn: (token: string) => api.saveGithubToken(token),
  onSuccess: (s) => {
    toastOk(s?.login ? `Connected as @${s.login}` : 'GitHub token saved')
    gh.resetForm()
    void qc.invalidateQueries({ queryKey: keys.github })
  },
  onError: (e) => toastError(e),
})
const submitGh = gh.handleSubmit((v) => saveGh.mutate(v.token))
const ghRemoveOpen = ref(false)
const removeGh = useMutation({
  mutationFn: api.removeGithubToken,
  onSuccess: () => {
    toastOk('GitHub token removed')
    ghRemoveOpen.value = false
    void qc.invalidateQueries({ queryKey: keys.github })
  },
  onError: (e) => toastError(e),
})
</script>

<template>
  <SettingsSection
    title="GitHub"
    description="Needed only to deploy private repositories. Create a fine-grained personal access token on GitHub with read-only access to Contents and Metadata for the repositories you choose."
  >
    <FormScope :form="gh">
      <form novalidate @submit.prevent="submitGh">
        <Card>
          <CardContent class="grid gap-4">
            <QueryError v-if="github.isError.value" :error="github.error.value" @retry="github.refetch()" />
            <Skeleton v-else-if="github.isPending.value" class="h-10 w-full" />
            <div v-else class="-mx-4 flex flex-wrap items-center gap-3 border-b px-4 pb-4">
              <div class="flex size-9 items-center justify-center rounded-lg border bg-muted/50"><Github class="size-4" /></div>
              <div class="min-w-0 text-sm">
                <template v-if="github.data.value?.connected">
                  <p class="font-medium">Connected as @{{ github.data.value.login }}</p>
                  <p v-if="github.data.value.expires_at" class="text-xs text-muted-foreground" :title="exactTime(github.data.value.expires_at)">
                    The token expires <RelativeTime :date="github.data.value.expires_at" />
                  </p>
                </template>
                <template v-else>
                  <p class="font-medium">Not connected</p>
                  <p class="text-xs text-muted-foreground">Public repositories work without a token.</p>
                </template>
              </div>
              <Button v-if="github.data.value?.connected" type="button" class="ml-auto" variant="outline" @click="ghRemoveOpen = true">Disconnect</Button>
            </div>
            <FormField v-slot="{ componentField }" name="token">
              <FormItem>
                <FormLabel>{{ github.data.value?.connected ? 'Replace the token' : 'Personal access token' }}</FormLabel>
                <FormControl><Input type="password" placeholder="github_pat_..." autocomplete="off" spellcheck="false" class="font-mono" v-bind="componentField" /></FormControl>
                <FormMessage />
              </FormItem>
            </FormField>
          </CardContent>
          <CardFooter class="justify-between gap-3">
            <p class="text-xs text-muted-foreground">Checked with GitHub when you save, stored encrypted, never shown again.</p>
            <Button type="submit" :disabled="saveGh.isPending.value">{{ saveGh.isPending.value ? 'Checking...' : 'Save token' }}</Button>
          </CardFooter>
        </Card>
      </form>
    </FormScope>

    <ConfirmDialog
      v-model:open="ghRemoveOpen"
      title="Disconnect GitHub?"
      description="Bitploy forgets the token. Deploys from private repositories stop working until you add a token again."
      confirm-label="Disconnect"
      :loading="removeGh.isPending.value"
      @confirm="removeGh.mutate()"
    />
  </SettingsSection>
</template>
