import whale01 from '@/assets/backgrounds/whale-01.webp'

/**
 * M4: background switching/upload was removed. The manager ships one default
 * background image (whale-01) used by AppShell and the login page.
 */
export const DEFAULT_BACKGROUND = whale01

/** Resolve the fixed default background; the legacy value is accepted and ignored. */
export function resolveBackground(_value?: string | null): string {
  return DEFAULT_BACKGROUND
}

/** Builtin art is right-aligned; there is no user-selectable background any more. */
export function backgroundPosition(_value?: string | null): 'right center' {
  return 'right center'
}
