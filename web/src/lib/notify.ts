import { toast } from 'vue-sonner'
import { ApiError } from './api'

/** Show an error toast with the message and the hint (if any). */
export function toastError(e: unknown, fallback = 'Something went wrong.') {
  if (e instanceof ApiError) {
    toast.error(e.message, { description: e.hint || undefined })
  } else if (e instanceof Error) {
    toast.error(e.message || fallback)
  } else {
    toast.error(fallback)
  }
}

export function toastOk(message: string, description?: string) {
  toast.success(message, { description })
}

/**
 * Copy text that takes a moment to make (like one that needs a request first).
 * Handing the clipboard a promise keeps the click's permission, which Safari
 * needs. Where that is not available (plain http, older browsers) it waits for
 * the text and uses the normal path with its textarea fallback. `make` must not reject.
 */
export async function copyTextLater(make: () => Promise<string>, what = 'Copied') {
  const text = make()
  if (window.isSecureContext && typeof ClipboardItem !== 'undefined' && navigator.clipboard?.write) {
    try {
      const blob = text.then((t) => new Blob([t], { type: 'text/plain' }))
      await navigator.clipboard.write([new ClipboardItem({ 'text/plain': blob })])
      toast.success(what)
      return
    } catch {
      // fall through to the text path
    }
  }
  await copyText(await text, what)
}

export async function copyText(text: string, what = 'Copied') {
  try {
    await navigator.clipboard.writeText(text)
    toast.success(what)
  } catch {
    // Clipboard API needs a secure context; fall back to a hidden textarea.
    const ta = document.createElement('textarea')
    ta.value = text
    ta.style.position = 'fixed'
    ta.style.opacity = '0'
    document.body.appendChild(ta)
    ta.select()
    try {
      document.execCommand('copy')
      toast.success(what)
    } catch {
      toast.error('Could not copy', { description: 'Select the text and copy it by hand.' })
    } finally {
      document.body.removeChild(ta)
    }
  }
}
