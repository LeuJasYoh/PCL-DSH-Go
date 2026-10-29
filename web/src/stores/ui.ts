import { defineStore } from 'pinia'
import { ref } from 'vue'

const THEME_KEY = 'pcl-dsh-go.theme'

export type Theme = 'dark' | 'light'

function readTheme(): Theme {
  try {
    return window.localStorage.getItem(THEME_KEY) === 'light' ? 'light' : 'dark'
  } catch {
    return 'dark'
  }
}

/** Shell-only UI state: theme, activity drawer and command palette. */
export const useUiStore = defineStore('ui', () => {
  const theme = ref<Theme>(readTheme())
  const activityOpen = ref(false)
  const paletteOpen = ref(false)

  function applyTheme(): void {
    document.documentElement.dataset.theme = theme.value
    const meta = document.querySelector('meta[name="theme-color"]')
    if (meta) meta.setAttribute('content', theme.value === 'dark' ? '#09090a' : '#f8f7f4')
  }

  function setTheme(next: Theme): void {
    theme.value = next
    try {
      window.localStorage.setItem(THEME_KEY, next)
    } catch {
      // Theme persistence is best effort.
    }
    applyTheme()
  }

  function toggleTheme(): void {
    setTheme(theme.value === 'dark' ? 'light' : 'dark')
  }

  function setActivityOpen(open: boolean): void {
    activityOpen.value = open
  }

  function toggleActivity(): void {
    activityOpen.value = !activityOpen.value
  }

  function setPaletteOpen(open: boolean): void {
    paletteOpen.value = open
  }

  return {
    theme,
    activityOpen,
    paletteOpen,
    applyTheme,
    setTheme,
    toggleTheme,
    setActivityOpen,
    toggleActivity,
    setPaletteOpen,
  }
})
