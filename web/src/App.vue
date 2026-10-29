<script setup lang="ts">
import { RouterView } from 'vue-router'
import { Toaster } from '@/components/ui/sonner'
import { TooltipProvider } from '@/components/ui/tooltip'
</script>

<template>
  <TooltipProvider :delay-duration="200">
    <RouterView />
  </TooltipProvider>
  <Toaster position="bottom-right" rich-colors close-button />
</template>
