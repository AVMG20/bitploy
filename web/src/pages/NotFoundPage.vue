<script setup lang="ts">
import { RouterLink } from 'vue-router'
import { Button } from '@/components/ui/button'
</script>

<template>
  <div class="flex min-h-svh flex-col items-center justify-center gap-3 p-4 text-center">
    <p class="text-5xl font-semibold">404</p>
    <p class="text-muted-foreground">This page does not exist.</p>
    <Button as-child><RouterLink to="/">Back to your apps</RouterLink></Button>
  </div>
</template>
