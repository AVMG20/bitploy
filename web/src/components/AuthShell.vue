<script setup lang="ts">
import BrandMark from './BrandMark.vue'
import ThemeToggle from './ThemeToggle.vue'

defineProps<{ title: string; description?: string }>()

const steps = [
  { title: 'Connect your AI assistant', text: 'Add bitploy to Claude, Cursor or another AI tool. It takes a minute.' },
  { title: 'Ask it to put your app online', text: 'It builds the app on this server and gives you a link.' },
  { title: 'Change it any time', text: 'Ask again or push to GitHub. Every version is kept, so you can go back.' },
]
</script>

<template>
  <div class="grid min-h-svh lg:grid-cols-2">
    <section class="auth-canvas flex flex-col gap-10 border-t p-8 sm:p-12 lg:border-t-0 lg:border-r">
      <div class="hidden items-center gap-2.5 text-lg font-semibold tracking-tight lg:flex">
        <BrandMark class="size-8" />
        bitploy
      </div>
      <div class="my-auto max-w-md">
        <h2 class="text-2xl font-semibold tracking-tight text-balance sm:text-3xl">Put your apps online by asking your AI.</h2>
        <p class="mt-3 text-muted-foreground">bitploy hosts websites and small apps on your own server. Your AI assistant does the deploying.</p>
        <ol class="mt-10 grid gap-6">
          <li v-for="(step, i) in steps" :key="step.title" class="flex gap-4">
            <span class="font-mono text-sm text-primary tabular-nums">0{{ i + 1 }}</span>
            <div>
              <p class="font-medium">{{ step.title }}</p>
              <p class="mt-0.5 text-sm text-muted-foreground">{{ step.text }}</p>
            </div>
          </li>
        </ol>
      </div>
      <p class="text-xs text-muted-foreground">Plain HTML, Node, Python, PHP, or anything with a Dockerfile.</p>
    </section>

    <section class="relative order-first flex flex-col items-center justify-center gap-8 p-6 py-16 lg:order-none">
      <div class="absolute top-3 right-3"><ThemeToggle /></div>
      <div class="flex items-center gap-2.5 text-lg font-semibold tracking-tight lg:hidden">
        <BrandMark class="size-8" />
        bitploy
      </div>
      <div class="w-full max-w-sm">
        <h1 class="text-xl font-semibold tracking-tight">{{ title }}</h1>
        <p v-if="description" class="mt-1 mb-6 text-sm text-muted-foreground">{{ description }}</p>
        <div v-else class="mb-6" />
        <slot />
      </div>
    </section>
  </div>
</template>

<style scoped>
.auth-canvas {
  background-color: color-mix(in oklch, var(--muted) 35%, var(--background));
  background-image: radial-gradient(circle at 1px 1px, color-mix(in oklch, var(--foreground) 9%, transparent) 1px, transparent 0);
  background-size: 22px 22px;
}
</style>
