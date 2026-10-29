import { ref } from 'vue'

export type ThemeChoice = 'light' | 'dark' | 'system'
const KEY = 'bitploy.theme'

function stored(): ThemeChoice {
  try {
    const v = localStorage.getItem(KEY)
    if (v === 'light' || v === 'dark' || v === 'system') return v
  } catch {
    // storage unavailable
  }
  return 'system'
}

export const theme = ref<ThemeChoice>(stored())

const media = window.matchMedia('(prefers-color-scheme: dark)')

function apply() {
  const dark = theme.value === 'dark' || (theme.value === 'system' && media.matches)
  document.documentElement.classList.toggle('dark', dark)
  document.documentElement.style.colorScheme = dark ? 'dark' : 'light'
}

export function setTheme(choice: ThemeChoice) {
  theme.value = choice
  try {
    localStorage.setItem(KEY, choice)
  } catch {
    // ignore
  }
  apply()
}

export function initTheme() {
  apply()
  media.addEventListener('change', apply)
}
