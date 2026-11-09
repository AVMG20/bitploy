<script setup lang="ts">
import { computed } from 'vue'
import type { Component } from 'vue'
import { Clock, DatabaseZap, FileQuestion, FolderOpen, HardDrive, Lock } from 'lucide-vue-next'
import { RouterLink } from 'vue-router'
import type { RouteLocationRaw } from 'vue-router'
import EmptyState from '@/components/EmptyState.vue'
import QueryError from '@/components/QueryError.vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { ApiError } from '@/lib/api'

// How the viewer shows what went wrong with loading a database. The errors a
// person can run into have words of their own (as on the Storage tab); the rest
// is shown like everywhere else.
const props = defineProps<{
  error: unknown
  /** Show this error code's words without an API error (for a state the page already knows). */
  code?: string
  /** A database server (PostgreSQL, MariaDB, MySQL), not a file. */
  server?: boolean
  /** Where "Back" goes, and what it is called. */
  backTo?: RouteLocationRaw
  backLabel?: string
  /** A link to download the file (offered when it is not a database). */
  downloadHref?: string
}>()
defineEmits<{ retry: [] }>()

const code = computed(() => props.code ?? (props.error instanceof ApiError ? props.error.code : ''))

interface Info {
  icon: Component
  title: string
  description: string
  /** Trying again can help (the database is busy or the folder is being restored). */
  retry: boolean
}
const INFO: Record<string, Info> = {
  not_sqlite: {
    icon: FileQuestion,
    title: 'This is not a SQLite database.',
    description: 'The file does not start with the SQLite header. It may be another kind of file, or damaged.',
    retry: false,
  },
  db_not_found: { icon: FileQuestion, title: 'That file does not exist.', description: 'Go back to Storage and pick it again.', retry: false },
  file_not_found: { icon: FileQuestion, title: 'That file does not exist.', description: 'Go back to Storage and pick it again.', retry: false },
  db_unreadable: {
    icon: FileQuestion,
    title: 'This database cannot be read.',
    description: 'It may be damaged, encrypted, or written by a newer SQLite than this server has.',
    retry: false,
  },
  file_symlink: { icon: FileQuestion, title: 'This is a link, not a file.', description: 'The viewer only opens real files among the saved files.', retry: false },
  not_a_file: { icon: FileQuestion, title: 'This is not a file.', description: 'Go back to Storage and pick a database file.', retry: false },
  db_helper_unavailable: {
    icon: DatabaseZap,
    title: "The server's SQLite helper is not available.",
    description: 'Databases cannot be opened on this server right now. Tell your admin.',
    retry: false,
  },
  admin_only: { icon: Lock, title: 'Only admins can open this.', description: 'Ask an admin if you need to look at bitploy\'s own database.', retry: false },
  db_not_running: { icon: DatabaseZap, title: 'The database is not running.', description: 'Start it, or wait until its deployment is live, then try again.', retry: true },
  db_unreachable: { icon: DatabaseZap, title: 'The database does not answer.', description: 'It may still be starting. Check that it is running and healthy, then try again.', retry: true },
  db_login_failed: {
    icon: Lock,
    title: 'The database did not accept the login.',
    description: 'Its password was probably changed in Variables after the database was made. Set it back to the one it was made with.',
    retry: false,
  },
  volume_off: { icon: HardDrive, title: 'Saved files are turned off for this app.', description: 'Turn them on in Storage to look at its files.', retry: false },
  volume_not_ready: { icon: FolderOpen, title: 'The saved files folder is not there yet.', description: 'Restart the app or deploy it again to create it.', retry: false },
}
const BUSY: Record<string, { icon: Component; title: string; description: string }> = {
  db_busy: {
    icon: Lock,
    title: 'The database is busy.',
    description: 'Another process has it locked, probably your app. Wait a moment and try again.',
  },
  db_timeout: {
    icon: Clock,
    title: 'The database took too long to answer.',
    description: 'The request was stopped after 15 seconds. Try again, or narrow it down with a filter or a smaller page.',
  },
  db_out_of_memory: {
    icon: DatabaseZap,
    title: 'The database ran out of memory.',
    description: 'That request needed too much memory. Try again with a filter or a smaller page.',
  },
  backup_busy: { icon: DatabaseZap, title: 'A backup or restore is running.', description: 'Wait until it has finished, then try again.' },
  files_busy: { icon: DatabaseZap, title: 'A backup or restore is running.', description: 'Wait until it has finished, then try again.' },
}
// A database server has databases where SQLite has files.
const SERVER: Record<string, Info> = {
  db_not_found: { icon: FileQuestion, title: 'There is no such database on this server.', description: 'Pick another one, or open the Database tab again for the main one.', retry: false },
  db_helper_unavailable: {
    icon: DatabaseZap,
    title: "The server's database helper is not available.",
    description: 'Databases cannot be opened on this server right now. Tell your admin.',
    retry: false,
  },
}
const info = computed(() => (props.server && SERVER[code.value]) || INFO[code.value])
const busy = computed(() => BUSY[code.value])
</script>

<template>
  <EmptyState v-if="info" :icon="info.icon" :title="info.title" :description="info.description">
    <Button v-if="backTo" as-child variant="outline"><RouterLink :to="backTo">Back to {{ backLabel ?? 'Storage' }}</RouterLink></Button>
    <Button v-if="code === 'not_sqlite' && downloadHref" as-child variant="outline"><a :href="downloadHref" download>Download the file</a></Button>
  </EmptyState>
  <Alert v-else-if="busy" variant="destructive">
    <component :is="busy.icon" />
    <AlertTitle>{{ busy.title }}</AlertTitle>
    <AlertDescription>
      <p>{{ busy.description }}</p>
      <Button class="mt-2" variant="outline" size="sm" @click="$emit('retry')">Try again</Button>
    </AlertDescription>
  </Alert>
  <QueryError v-else :error="error" @retry="$emit('retry')" />
</template>
