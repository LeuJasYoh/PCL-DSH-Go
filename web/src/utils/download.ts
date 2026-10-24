/**
 * Trigger a same-origin artifact download through the session cookie.
 * The backend sets Content-Disposition; the download attribute only supplies
 * the filename for the browser's "Save as" UI.
 */
export function triggerDownload(url: string, filename?: string): void {
  const link = document.createElement('a')
  link.href = url
  link.rel = 'noopener'
  if (filename) link.download = filename
  link.style.display = 'none'
  document.body.appendChild(link)
  link.click()
  window.setTimeout(() => link.remove(), 0)
}
