import { ref } from 'vue'
import { useEventListener } from '@vueuse/core'

/** The quick switcher (⌘K / Ctrl+K), opened from anywhere. */
export const paletteOpen = ref(false)

export const isMac = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent)
/** "⌘" on a Mac, "Ctrl" elsewhere, for shortcut hints. */
export const modKey = isMac ? '⌘' : 'Ctrl'

/** The key goes to a text field, so it is not a shortcut. */
export function isTyping(e: KeyboardEvent): boolean {
  const t = e.target as HTMLElement | null
  if (!t) return false
  return t.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(t.tagName)
}

/** ⌘K / Ctrl+K anywhere toggles the quick switcher. Mounted once, by the layout. */
export function usePaletteShortcut() {
  useEventListener(document, 'keydown', (e: KeyboardEvent) => {
    if ((e.metaKey || e.ctrlKey) && !e.altKey && !e.shiftKey && e.key.toLowerCase() === 'k') {
      e.preventDefault()
      paletteOpen.value = !paletteOpen.value
    }
  })
}

/** "/" puts the cursor in this page's search field, unless you are typing somewhere already. */
export function useSlashFocus(focus: () => void) {
  useEventListener(document, 'keydown', (e: KeyboardEvent) => {
    if (e.key !== '/' || e.metaKey || e.ctrlKey || e.altKey || isTyping(e) || paletteOpen.value) return
    // A dialog is open: the shortcut belongs to the page behind it.
    if (document.querySelector('[role="dialog"], [role="alertdialog"]')) return
    e.preventDefault()
    focus()
  })
}
