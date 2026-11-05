<script setup lang="ts">
import type { HTMLAttributes } from 'vue'
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from '@/components/ui/card'

// The one card for a setting: title and what it changes on top, the fields in
// the middle, and a footer with a short note on the left and the save button
// on the right. Wrap it in a <form> when it saves.
defineProps<{ title?: string; description?: string; class?: HTMLAttributes['class'] }>()
</script>

<template>
  <Card :class="$props.class">
    <CardHeader v-if="title">
      <CardTitle>{{ title }}</CardTitle>
      <CardDescription v-if="description || $slots.description"><slot name="description">{{ description }}</slot></CardDescription>
    </CardHeader>
    <CardContent v-if="$slots.default"><slot /></CardContent>
    <CardFooter v-if="$slots.actions" class="flex-wrap justify-between gap-x-4 gap-y-2">
      <p class="min-w-0 text-xs text-muted-foreground"><slot name="hint" /></p>
      <div class="ml-auto flex flex-wrap items-center gap-2"><slot name="actions" /></div>
    </CardFooter>
  </Card>
</template>
