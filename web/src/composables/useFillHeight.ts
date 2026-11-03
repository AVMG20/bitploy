import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type { Ref } from 'vue'

/**
 * A max-height that lets an element reach the bottom of the screen, minus `reserve` pixels for what
 * follows it (a pager, the page padding). The page then fits the window and only that element scrolls.
 * It measures against the scrolling container, so the result does not change while the page scrolls.
 */
export function useFillHeight(el: Ref<HTMLElement | null>, reserve = 96, min = 220) {
  const maxHeight = ref<string | undefined>()

  function scroller(node: HTMLElement): HTMLElement | null {
    for (let p = node.parentElement; p; p = p.parentElement) {
      const o = getComputedStyle(p).overflowY
      if (o === 'auto' || o === 'scroll') return p
    }
    return null
  }

  function update() {
    const node = el.value
    if (!node || !node.offsetParent) return // not shown (a hidden tab)
    const sc = scroller(node)
    const rect = node.getBoundingClientRect()
    let top: number
    let room: number
    if (sc) {
      top = rect.top - sc.getBoundingClientRect().top + sc.scrollTop
      room = sc.clientHeight
    } else {
      top = rect.top + window.scrollY
      room = window.innerHeight
    }
    const next = `${Math.max(min, Math.round(room - top - reserve))}px`
    if (next !== maxHeight.value) maxHeight.value = next
  }

  let observer: ResizeObserver | null = null
  onMounted(() => {
    window.addEventListener('resize', update)
    observer = new ResizeObserver(update)
    watch(
      el,
      (node) => {
        observer?.disconnect()
        if (!node) return
        // What sits above or around the element changes its place: look again when that parent changes size.
        if (node.parentElement) observer?.observe(node.parentElement)
        const sc = scroller(node)
        if (sc) observer?.observe(sc)
        update()
      },
      { immediate: true, flush: 'post' },
    )
  })
  onBeforeUnmount(() => {
    window.removeEventListener('resize', update)
    observer?.disconnect()
  })

  return { maxHeight, update }
}
