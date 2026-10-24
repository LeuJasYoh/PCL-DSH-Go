<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref } from 'vue'

/**
 * Lightweight modal dialog (plan 5.5/5.6): focus trap, ESC close, aria-modal.
 * A UI framework is intentionally not used to keep the desktop visual match.
 */
const props = withDefaults(
  defineProps<{
    title: string
    testid?: string
    wide?: boolean
    closable?: boolean
  }>(),
  { testid: 'modal', wide: false, closable: true },
)

const emit = defineEmits<{ close: [] }>()

const panel = ref<HTMLElement | null>(null)
let previouslyFocused: HTMLElement | null = null

function focusables(): HTMLElement[] {
  const root = panel.value
  if (!root) return []
  return Array.from(
    root.querySelectorAll<HTMLElement>(
      'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])',
    ),
  ).filter((element) => element.offsetParent !== null || element === document.activeElement)
}

function onKeydown(event: KeyboardEvent): void {
  if (event.key === 'Escape') {
    if (props.closable) {
      event.preventDefault()
      event.stopPropagation()
      emit('close')
    }
    return
  }
  if (event.key !== 'Tab') return
  const items = focusables()
  if (!items.length) return
  const first = items[0]
  const last = items[items.length - 1]
  if (event.shiftKey && document.activeElement === first) {
    event.preventDefault()
    last.focus()
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault()
    first.focus()
  }
}

function onOverlayClick(event: MouseEvent): void {
  if (props.closable && event.target === event.currentTarget) emit('close')
}

onMounted(() => {
  previouslyFocused = document.activeElement instanceof HTMLElement ? document.activeElement : null
  document.addEventListener('keydown', onKeydown, true)
  document.body.classList.add('modal-open')
  void nextTick(() => {
    const items = focusables()
    const preferred = items.find((item) => item.dataset.autofocus === 'true')
    ;(preferred ?? items[0] ?? panel.value)?.focus()
  })
})

onBeforeUnmount(() => {
  document.removeEventListener('keydown', onKeydown, true)
  document.body.classList.remove('modal-open')
  previouslyFocused?.focus?.()
})
</script>

<template>
  <Teleport to="body">
    <div class="modal-overlay" @click="onOverlayClick">
      <div
        ref="panel"
        class="modal-panel"
        :class="{ 'modal-panel--wide': wide }"
        role="dialog"
        aria-modal="true"
        :aria-label="title"
        :data-testid="testid"
        tabindex="-1"
      >
        <header class="modal-header">
          <span class="modal-title">{{ title }}</span>
          <button
            v-if="closable"
            class="modal-close"
            type="button"
            aria-label="关闭"
            data-testid="modal-close"
            @click="emit('close')"
          >
            ×
          </button>
        </header>
        <div class="modal-body">
          <slot />
        </div>
        <footer v-if="$slots.footer" class="modal-footer">
          <slot name="footer" />
        </footer>
      </div>
    </div>
  </Teleport>
</template>
