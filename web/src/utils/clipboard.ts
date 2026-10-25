export type CopyOutcome = 'copied' | 'fallback'

// Selection copy can still work on HTTP pages without the Clipboard API.
export async function copyText(text: string): Promise<CopyOutcome> {
  if (!text) return 'fallback'
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text)
      return 'copied'
    }
  } catch {}
  return copyWithSelection(text) ? 'copied' : 'fallback'
}

function copyWithSelection(text: string): boolean {
  const textarea = document.createElement('textarea')
  const previouslyFocused = document.activeElement instanceof HTMLElement ? document.activeElement : null
  textarea.value = text
  textarea.readOnly = true
  textarea.setAttribute('aria-hidden', 'true')
  textarea.style.position = 'fixed'
  textarea.style.left = '-9999px'
  textarea.style.top = '0'
  document.body.appendChild(textarea)
  textarea.focus()
  textarea.select()
  textarea.setSelectionRange(0, text.length)
  let copied = false
  try {
    copied = document.execCommand('copy')
  } catch {
    copied = false
  }
  textarea.remove()
  previouslyFocused?.focus()
  return copied
}
