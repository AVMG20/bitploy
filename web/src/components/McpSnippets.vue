<script setup lang="ts">
import { computed } from 'vue'
import CodeBlock from '@/components/CodeBlock.vue'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { clientSnippets } from '@/lib/mcp'

const props = defineProps<{ mcpUrl: string; token: string }>()
const snippets = computed(() => clientSnippets(props.mcpUrl, props.token))
</script>

<template>
  <Tabs default-value="claude-code" class="min-w-0">
    <div class="-mx-1 overflow-x-auto px-1">
      <TabsList>
        <TabsTrigger v-for="s in snippets" :key="s.id" :value="s.id">{{ s.label }}</TabsTrigger>
      </TabsList>
    </div>
    <TabsContent v-for="s in snippets" :key="s.id" :value="s.id" class="grid gap-2">
      <p class="text-sm text-muted-foreground">{{ s.where }}</p>
      <CodeBlock :code="s.code" :what="`${s.label} setup copied`" />
      <p v-if="s.note" class="text-xs text-muted-foreground">{{ s.note }}</p>
    </TabsContent>
  </Tabs>
</template>
