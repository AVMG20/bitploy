import { ref } from 'vue'

// One shared clock, ticking every 15 s, so relative times and uptimes stay fresh.
export const nowMs = ref(Date.now())
setInterval(() => {
  nowMs.value = Date.now()
}, 15_000)
