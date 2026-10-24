import { defineStore } from 'pinia'
import { ref } from 'vue'

export type NotifyKind = 'info' | 'success' | 'error'

export interface NotifyItem {
  id: number
  kind: NotifyKind
  text: string
}

/**
 * Minimal toast store (no UI framework). Used to explain graceful degradation:
 * an unimplemented endpoint shows a truthful message instead of fake data.
 */
export const useNotifyStore = defineStore('notify', () => {
  const items = ref<NotifyItem[]>([])
  const timers = new Map<number, number>()
  let sequence = 0

  function dismiss(id: number): void {
    items.value = items.value.filter((item) => item.id !== id)
    const timer = timers.get(id)
    if (timer !== undefined) {
      window.clearTimeout(timer)
      timers.delete(id)
    }
  }

  function push(kind: NotifyKind, text: string, duration = 5200): number {
    const id = ++sequence
    items.value = [...items.value, { id, kind, text }]
    timers.set(id, window.setTimeout(() => dismiss(id), duration))
    return id
  }

  const info = (text: string) => push('info', text)
  const success = (text: string) => push('success', text)
  const error = (text: string) => push('error', text, 7000)

  return { items, push, dismiss, info, success, error }
})
