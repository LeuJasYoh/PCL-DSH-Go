<script setup lang="ts">
import { computed } from 'vue'

/**
 * The desktop launcher draws every icon as an original 24x24 SVG path
 * (ShellView.java icon()). The same paths are reused here at a 19px box.
 */
const paths: Record<string, string> = {
  home: 'M3 10 L12 3 L21 10 M5 9 L5 21 L10 21 L10 14 L14 14 L14 21 L19 21 L19 9',
  user: 'M8 7 A4 4 0 1 0 16 7 A4 4 0 1 0 8 7 M3 22 L3 19 C3 11 21 11 21 19 L21 22',
  layers: 'M12 3 L22 8 L12 13 L2 8 Z M3 13 L12 18 L21 13 M3 18 L12 23 L21 18',
  box: 'M3 6 L12 2 L21 6 L21 18 L12 23 L3 18 Z M3 6 L12 11 L21 6 M12 11 L12 23 M7 4 L17 9',
  cpu: 'M6 6 L18 6 L18 18 L6 18 Z M9 9 L15 9 L15 15 L9 15 Z M9 2 L9 6 M15 2 L15 6 M9 18 L9 22 M15 18 L15 22 M2 9 L6 9 M2 15 L6 15 M18 9 L22 9 M18 15 L22 15',
  puzzle: 'M4 3 L10 3 C8 8 16 8 14 3 L20 3 L20 9 C15 7 15 15 20 13 L20 20 L13 20 C15 15 7 15 9 20 L3 20 L3 13 C8 15 8 7 3 9 L3 3 Z',
  terminal: 'M3 4 L21 4 L21 20 L3 20 Z M6 9 L10 12 L6 15 M13 16 L18 16',
  settings: 'M4 6 L20 6 M4 12 L20 12 M4 18 L20 18 M8 3 L8 9 M16 9 L16 15 M10 15 L10 21',
  refresh: 'M20 9 C18 3 8 2 4 8 M4 3 L4 8 L9 8 M4 15 C6 21 16 22 20 16 M20 21 L20 16 L15 16',
  plus: 'M12 4 L12 20 M4 12 L20 12',
  play: 'M7 3 L21 12 L7 21 Z',
  stop: 'M5 5 L19 5 L19 19 L5 19 Z',
  download: 'M12 3 L12 16 M7 11 L12 16 L17 11 M4 16 L4 21 L20 21 L20 16',
  upload: 'M12 17 L12 4 M7 9 L12 4 L17 9 M4 16 L4 21 L20 21 L20 16',
  trash: 'M4 6 L20 6 M9 3 L15 3 M6 6 L7 21 L17 21 L18 6 M10 10 L10 17 M14 10 L14 17',
  folder: 'M2 6 L10 6 L12 9 L22 9 L20 21 L2 21 Z M2 6 L2 3 L10 3 L13 6 L20 6 L20 9',
  external: 'M14 3 L21 3 L21 10 M21 3 L11 13 M10 5 L3 5 L3 21 L19 21 L19 14',
  copy: 'M9 8 L21 8 L21 22 L9 22 Z M15 8 L15 2 L3 2 L3 16 L9 16',
  link: 'M10 8 L14 4 C18 0 24 6 20 10 L16 14 M8 10 L4 14 C0 18 6 24 10 20 L14 16 M8 16 L16 8',
  check: 'M4 12 L9 17 L20 6',
}

const props = withDefaults(defineProps<{ name: string; size?: number }>(), {
  size: 19,
})

const drawing = computed(() => paths[props.name] ?? paths.check)
</script>

<template>
  <span class="app-icon" :style="{ width: size + 'px', height: size + 'px' }" aria-hidden="true">
    <svg
      :width="size"
      :height="size"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="1.7"
      stroke-linecap="round"
      stroke-linejoin="round"
    >
      <path :d="drawing" />
    </svg>
  </span>
</template>

<style scoped>
.app-icon {
  display: inline-flex;
  flex: 0 0 auto;
  align-items: center;
  justify-content: center;
}

.app-icon svg {
  display: block;
}
</style>
