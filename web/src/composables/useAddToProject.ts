import { ref } from 'vue'

// The "Add to project" dialog lives once in the layout; the project page, the
// app page's project list, the sidebar and the command palette open it.

export interface AddTarget {
  projectId: string
  /** Open straight on one template (postgres) instead of the list. */
  template?: string
}

export const addTarget = ref<AddTarget | null>(null)

export function openAdd(projectId: string, template?: string) {
  addTarget.value = { projectId, template }
}
