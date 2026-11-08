import { ref } from 'vue'

/**
 * Tooltips that also open on a tap. Reka's tooltips open on hover and keyboard
 * focus only; on a phone a tap here opens one and keeps it open until the next
 * tap, on it or anywhere else. One tooltip of the group is open at a time.
 *
 * Per tooltip, bind `open(key)` and `onOpen(key, $event)` to the Tooltip (with
 * disable-closing-trigger), `onTap(key, $event)` to the trigger's pointerup,
 * and `onOutside(key, $event)` and `close` to the content's pointer-down-outside
 * and escape-key-down.
 */
export function useTapTooltips() {
  const current = ref<string | null>(null)
  const pinned = ref(false)

  const open = (key: string) => current.value === key

  function onOpen(key: string, o: boolean) {
    if (o) {
      current.value = key
      pinned.value = false
    } else if (current.value === key && !pinned.value) {
      current.value = null
    }
  }

  function onTap(key: string, e: PointerEvent) {
    if (e.pointerType !== 'touch' && e.pointerType !== 'pen') return
    const again = current.value === key && pinned.value
    current.value = again ? null : key
    pinned.value = !again
  }

  function close() {
    current.value = null
    pinned.value = false
  }

  /** A tap on the tooltip's own trigger is left to onTap, which closes it. */
  function onOutside(key: string, e: Event) {
    const target = (e as CustomEvent<{ originalEvent?: Event }>).detail?.originalEvent?.target ?? e.target
    const trigger = target instanceof Element ? target.closest('[data-tap-tooltip]') : null
    if (trigger?.getAttribute('data-tap-tooltip') === key) return
    close()
  }

  return { open, onOpen, onTap, onOutside, close }
}
