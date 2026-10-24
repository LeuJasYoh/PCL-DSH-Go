export type CopyOutcome = 'copied' | 'fallback'

/**
 * navigator.clipboard only exists on secure contexts (localhost/HTTPS).
 * When it is unavailable or denied the caller must show the manual-copy
 * fallback instead of claiming success (plan 5.5 #3).
 */
export async function copyText(text: string): Promise<CopyOutcome> {
  if (!text) return 'fallback'
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text)
      return 'copied'
    }
  } catch {
    // Fall through to the read-only textarea fallback.
  }
  return 'fallback'
}
