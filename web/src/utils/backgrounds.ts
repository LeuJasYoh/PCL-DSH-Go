import whale01 from '@/assets/backgrounds/whale-01.webp'
import whale02 from '@/assets/backgrounds/whale-02.webp'
import whale03 from '@/assets/backgrounds/whale-03.webp'
import whale04 from '@/assets/backgrounds/whale-04.webp'
import whale05 from '@/assets/backgrounds/whale-05.webp'

export interface BuiltinBackground {
  id: string
  value: string
  title: string
  src: string
}

/** Mirrors BackgroundCatalog.java titles and builtin ids. */
export const BACKGROUNDS: BuiltinBackground[] = [
  { id: 'whale-01', value: 'builtin:whale-01', title: '01 晴空招手', src: whale01 },
  { id: 'whale-02', value: 'builtin:whale-02', title: '02 浅海微风', src: whale02 },
  { id: 'whale-03', value: 'builtin:whale-03', title: '03 暖阳小憩', src: whale03 },
  { id: 'whale-04', value: 'builtin:whale-04', title: '04 月色晚安', src: whale04 },
  { id: 'whale-05', value: 'builtin:whale-05', title: '05 初雪围巾', src: whale05 },
]

export const DEFAULT_BACKGROUND = 'builtin:whale-01'

export function findBackground(value: string | null | undefined): BuiltinBackground | null {
  if (!value) return null
  return BACKGROUNDS.find((entry) => entry.value === value) ?? null
}

/** Asset URLs served by the backend (/api/assets/backgrounds/...) or plain http(s)/data URLs. */
export function isCustomBackground(value: string | null | undefined): boolean {
  if (!value) return false
  return (
    value.startsWith('/api/') ||
    value.startsWith('/') ||
    value.startsWith('data:') ||
    value.startsWith('https://') ||
    value.startsWith('http://')
  )
}

/**
 * Builtin backgrounds are bundled PNGs (WebP conversion is a follow-up, plan 5.7).
 * Backend-served custom backgrounds are used directly; desktop-only local paths
 * cannot be served by a browser and fall back to the default builtin art.
 */
export function resolveBackground(value: string | null | undefined): string {
  const builtin = findBackground(value)
  if (builtin) return builtin.src
  if (isCustomBackground(value)) return value as string
  return BACKGROUNDS[0].src
}

/** Plan 5.2: builtin art is right-aligned, custom backgrounds are centered. */
export function backgroundPosition(value: string | null | undefined): 'right center' | 'center center' {
  if (findBackground(value)) return 'right center'
  if (isCustomBackground(value)) return 'center center'
  return 'right center'
}

export function backgroundTitle(value: string | null | undefined): string {
  const builtin = findBackground(value)
  if (builtin) return builtin.title
  if (isCustomBackground(value)) return '自定义背景'
  if (value) return '自定义背景（Web 版无法显示本地文件，已回退默认背景）'
  return BACKGROUNDS[0].title
}
