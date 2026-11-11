import { onBeforeUnmount, ref, watch, type Ref } from 'vue'

/**
 * A number that glides to each new value of source over ms (ease-out). The
 * first number after NaN (not loaded yet), and every value with reduced
 * motion, is shown at once.
 */
export function useTween(source: () => number, ms = 900): Ref<number> {
  const out = ref(source())
  let frame = 0
  const reduced = typeof window !== 'undefined' && window.matchMedia?.('(prefers-reduced-motion: reduce)').matches

  watch(source, (to) => {
    cancelAnimationFrame(frame)
    const from = out.value
    if (reduced || from === to || !Number.isFinite(from)) {
      out.value = to
      return
    }
    const start = performance.now()
    const tick = (t: number) => {
      const p = Math.min(1, (t - start) / ms)
      out.value = from + (to - from) * (1 - Math.pow(1 - p, 3))
      if (p < 1) frame = requestAnimationFrame(tick)
    }
    frame = requestAnimationFrame(tick)
  })
  onBeforeUnmount(() => cancelAnimationFrame(frame))
  return out
}
