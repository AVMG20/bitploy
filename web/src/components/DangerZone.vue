<script setup lang="ts">
// The one place for actions that cannot be undone: a red-bordered card with
// one DangerRow per action (title, what it does, the button).
</script>

<template>
  <div data-slot="danger-zone" class="divide-y divide-destructive/20 overflow-hidden rounded-xl border border-destructive/40 bg-card shadow-card">
    <slot />
  </div>
</template>
