<script setup lang="ts">
import { computed, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { CircleCheck, KeyRound, LoaderCircle } from 'lucide-vue-next'
import CodeBlock from '@/components/CodeBlock.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import McpSnippets from '@/components/McpSnippets.vue'
import PageHeader from '@/components/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { keys, useMe } from '@/composables/queries'
import { api } from '@/lib/api'
import { connectToken, rememberToken, STARTER_PROMPT } from '@/lib/mcp'
import { toastError, toastOk } from '@/lib/notify'

const qc = useQueryClient()
const me = useMe()
const mcpUrl = computed(() => `${me.data.value?.public_url ?? window.location.origin}/mcp`)
const pasted = ref('')

// The token on this page, found by its prefix, and whether an assistant used
// it yet. The list is polled while it waits, so the page turns green by itself.
const tokens = useQuery({
  queryKey: keys.tokens,
  queryFn: api.tokens,
  enabled: computed(() => !!connectToken.value),
  refetchInterval: (q) => {
    const list = q.state.data
    const t = list?.find((x) => !x.revoked_at && connectToken.value.startsWith(x.prefix))
    return t?.last_used_at ? false : 5000
  },
})
const current = computed(() => tokens.data.value?.find((t) => !t.revoked_at && connectToken.value.startsWith(t.prefix)))

const create = useMutation({
  mutationFn: () => api.createToken({ name: 'Connect AI', scope: 'deploy' }),
  onSuccess: (t) => {
    rememberToken(t.value)
    toastOk('Token created', 'It is filled in below. Copy the setup for your tool.')
    void qc.invalidateQueries({ queryKey: keys.tokens })
  },
  onError: (e) => toastError(e),
})

function usePasted() {
  const v = pasted.value.trim()
  if (v) {
    rememberToken(v)
    pasted.value = ''
  }
}
</script>

<template>
  <div class="grid gap-6">
    <PageHeader title="Connect your AI assistant" description="Three steps, about two minutes. Then your assistant can put your code online for you." />

    <Card>
      <CardHeader>
        <CardTitle>1. Get a token</CardTitle>
        <CardDescription>The token lets your assistant use bitploy as you. Keep it private, like a password.</CardDescription>
      </CardHeader>
      <CardContent class="grid gap-3">
        <div v-if="connectToken" class="flex flex-wrap items-center gap-2 text-sm">
          <KeyRound class="size-4 text-success" />
          <span>Your token is filled in below.</span>
          <Button size="sm" variant="ghost" @click="rememberToken('')">Forget it</Button>
        </div>
        <div class="flex flex-wrap gap-2">
          <Button :disabled="create.isPending.value" @click="create.mutate()">
            {{ create.isPending.value ? 'Creating...' : connectToken ? 'Make another token' : 'Create a token for me' }}
          </Button>
          <Button as-child variant="outline"><RouterLink :to="{ name: 'settings-tokens' }">Manage tokens</RouterLink></Button>
        </div>
        <form class="flex max-w-lg gap-2" @submit.prevent="usePasted">
          <Input v-model="pasted" placeholder="Or paste a token you already have" autocomplete="off" spellcheck="false" aria-label="Existing token" />
          <Button type="submit" variant="outline" :disabled="!pasted.trim()">Use it</Button>
        </form>
        <p class="text-xs text-muted-foreground">The token is only kept in this browser tab and is gone when you close it.</p>
      </CardContent>
    </Card>

    <Card>
      <CardHeader>
        <CardTitle>2. Add bitploy to your tool</CardTitle>
        <CardDescription>
          Pick the tool you use and copy the setup.
          <template v-if="!connectToken"> Until you create a token it shows <code>bp_YOUR_TOKEN</code>, which you replace by hand.</template>
        </CardDescription>
      </CardHeader>
      <CardContent>
        <McpSnippets :mcp-url="mcpUrl" :token="connectToken" />
      </CardContent>
    </Card>

    <Card>
      <CardHeader>
        <CardTitle>3. Ask your assistant</CardTitle>
        <CardDescription>Open your code folder in your tool, then paste this.</CardDescription>
      </CardHeader>
      <CardContent>
        <CodeBlock :code="STARTER_PROMPT" what="Prompt copied" wrap />
        <p class="mt-3 text-sm text-muted-foreground">
          Your assistant sends the files, builds the app and gives you a link. You can watch it on the <RouterLink to="/" class="underline">Projects page</RouterLink>.
        </p>
        <p v-if="current?.last_used_at" class="mt-3 flex items-center gap-2 text-sm" role="status" data-testid="token-used">
          <CircleCheck class="size-4 shrink-0 text-success" />
          <span>It works: your assistant used this token <RelativeTime :date="current.last_used_at" />.</span>
        </p>
        <p v-else-if="current" class="mt-3 flex items-center gap-2 text-sm text-muted-foreground" role="status" data-testid="token-waiting">
          <LoaderCircle class="size-4 shrink-0 animate-spin" />
          <span>Waiting for your assistant to use this token. This turns green once it has.</span>
        </p>
      </CardContent>
    </Card>
  </div>
</template>
