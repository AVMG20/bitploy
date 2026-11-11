<script setup lang="ts">
import { RouterView } from 'vue-router'
import { Toaster } from '@/components/ui/sonner'
import { TooltipProvider } from '@/components/ui/tooltip'
import { theme } from '@/lib/theme'
</script>

<template>
  <TooltipProvider :delay-duration="200">
    <RouterView />
  </TooltipProvider>
  <!-- Every toast looks like the loading one (the popover's colours, in the
       app's theme); only the icon says how it went. -->
  <Toaster position="top-center" :theme="theme" close-button />
</template>
