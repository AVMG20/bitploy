<script setup lang="ts">
// A new account with no projects: a short checklist instead of an empty page,
// how a project and an app differ, and the limits in one sentence.
import { RouterLink } from 'vue-router'
import { Check, Database, FolderClosed, Globe, Server } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { formatMB } from '@/lib/format'
import { useServerUsage } from './useServerUsage'
import { computed } from 'vue'

defineProps<{ name: string }>()
const { quota, unlimited } = useServerUsage()
const sentence = computed(() => {
  const q = quota.value
  if (!q) return ''
  if (unlimited.value) return `Up to ${q.apps} apps. Memory and CPU have no limit for you. Plain websites cost nothing.`
  return `Up to ${q.apps} apps that reserve ${formatMB(q.memory_mb)} memory together. Plain websites cost nothing.`
})
</script>

<template>
  <div class="mx-auto max-w-4xl">
    <h1 class="text-2xl font-semibold tracking-tight">Welcome to bitploy<template v-if="name">, {{ name }}</template></h1>
    <p class="mt-1 text-sm text-muted-foreground">Your code, online at its own address, in a few minutes.</p>

    <div class="mt-6 grid gap-5 lg:grid-cols-[1fr_20rem]">
      <div class="rounded-xl border bg-card shadow-card">
        <div class="flex items-center justify-between gap-4 border-b p-5">
          <div>
            <h2 class="text-sm font-semibold">Get started</h2>
            <p class="mt-0.5 text-xs text-muted-foreground">1 of 4 done</p>
          </div>
          <div class="h-1.5 w-36 overflow-hidden rounded-full bg-muted"><div class="h-full w-1/4 rounded-full bg-primary" /></div>
        </div>
        <ol>
          <li class="flex gap-4 border-b p-5">
            <span class="flex size-7 shrink-0 items-center justify-center rounded-full bg-success/10 text-success"><Check class="size-3.5" aria-hidden="true" /></span>
            <div class="font-medium text-muted-foreground line-through">Make your account</div>
          </li>
          <li class="flex gap-4 border-b bg-linear-to-r from-primary/10 to-transparent to-60% p-5">
            <span class="flex size-7 shrink-0 items-center justify-center rounded-full bg-primary text-xs font-semibold text-primary-foreground">2</span>
            <div class="flex-1">
              <div class="font-medium">Deploy something</div>
              <p class="mt-1 text-sm text-muted-foreground">Let your AI assistant do it, use GitHub, or drop a folder.</p>
              <div class="mt-3 flex flex-wrap gap-2">
                <Button size="sm" as-child><RouterLink :to="{ name: 'new-app' }">Deploy my code</RouterLink></Button>
                <Button size="sm" variant="outline" as-child><RouterLink :to="{ name: 'ready-made' }">Try a ready-made app</RouterLink></Button>
              </div>
            </div>
          </li>
          <li class="flex gap-4 border-b p-5">
            <span class="flex size-7 shrink-0 items-center justify-center rounded-full border bg-muted text-xs font-semibold">3</span>
            <div class="flex-1">
              <div class="font-medium">Connect your AI assistant</div>
              <p class="mt-1 text-sm text-muted-foreground">So it can deploy, read logs and fix failures for you.</p>
            </div>
            <Button size="sm" variant="outline" class="self-center" as-child><RouterLink :to="{ name: 'connect' }">Connect</RouterLink></Button>
          </li>
          <li class="flex gap-4 p-5">
            <span class="flex size-7 shrink-0 items-center justify-center rounded-full border bg-muted text-xs font-semibold">4</span>
            <div class="flex-1">
              <div class="font-medium">Use your own domain <span class="text-xs font-normal text-muted-foreground">· optional</span></div>
              <p class="mt-1 text-sm text-muted-foreground">Add it to an app once it runs; it gets HTTPS by itself.</p>
            </div>
          </li>
        </ol>
      </div>

      <div class="grid content-start gap-4">
        <div class="rounded-xl border bg-card p-5 shadow-card">
          <h2 class="text-sm font-semibold">How it fits together</h2>
          <div class="mt-4 rounded-xl border border-dashed p-3.5">
            <div class="flex items-center gap-1.5 text-xs text-muted-foreground"><FolderClosed class="size-3.5" aria-hidden="true" />Project <b class="font-medium text-foreground">Shop</b></div>
            <div class="mt-3 grid grid-cols-3 gap-2 text-center text-xs">
              <div class="rounded-lg bg-muted/50 p-2"><Globe class="mx-auto size-3.5 text-primary-text" aria-hidden="true" /><div class="mt-1">website</div></div>
              <div class="rounded-lg bg-muted/50 p-2"><Server class="mx-auto size-3.5 text-muted-foreground" aria-hidden="true" /><div class="mt-1">API</div></div>
              <div class="rounded-lg bg-muted/50 p-2"><Database class="mx-auto size-3.5 text-muted-foreground" aria-hidden="true" /><div class="mt-1">database</div></div>
            </div>
          </div>
          <p class="mt-3 text-sm text-muted-foreground">
            <b class="font-medium text-foreground">A project</b> is one product. <b class="font-medium text-foreground">An app</b> is one running part of it. Apps in a project reach each other privately.
          </p>
        </div>
        <div v-if="sentence" class="rounded-xl border bg-card p-5 shadow-card">
          <h2 class="text-sm font-semibold">What you can run</h2>
          <p class="mt-1.5 text-sm text-muted-foreground">{{ sentence }}</p>
        </div>
      </div>
    </div>
  </div>
</template>
