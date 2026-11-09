<script setup lang="ts">
import { RouterLink, useRoute } from 'vue-router'
import AuthShell from '@/components/AuthShell.vue'
import { Button } from '@/components/ui/button'

const route = useRoute()
</script>

<template>
  <AuthShell title="Page not found" description="Nothing lives at this address. Check the link, or go back to your projects.">
    <div class="grid gap-4">
      <p class="flex min-w-0 items-center gap-2 rounded-lg border bg-muted/40 px-3 py-2 text-sm">
        <span class="shrink-0 font-mono text-xs text-primary-text">404</span>
        <code class="min-w-0 truncate font-mono text-xs text-muted-foreground" :title="route.fullPath">{{ route.fullPath }}</code>
      </p>
      <Button as-child class="w-full"><RouterLink to="/">Back to your projects</RouterLink></Button>
    </div>
  </AuthShell>
</template>
