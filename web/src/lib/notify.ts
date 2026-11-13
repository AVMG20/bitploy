import { toast } from 'vue-sonner'
import { ApiError } from './api'

/**
 * Show an error toast with the message and the hint (if any). With an id it
 * replaces that toast (a loading one).
 */
export function toastError(e: unknown, fallback = 'Something went wrong.', id?: string | number) {
  if (e instanceof ApiError) {
    toast.error(e.message, { description: e.hint || undefined, id })
  } else if (e instanceof Error) {
    const hint = 'hint' in e && typeof e.hint === 'string' ? e.hint : undefined
    toast.error(e.message || fallback, { description: hint, id })
  } else {
    toast.error(fallback, { id })
  }
}

export function toastOk(message: string, description?: string) {
  toast.success(message, { description })
}

/**
 * Copy text that takes a moment to make (it needs a request first). Returns
 * whether it was copied.
 *
 * Browsers only allow a copy right after a click. The ClipboardItem is created
 * in the same tick as the click, with a promise for the text, so the copy
 * keeps that permission while the text loads (Safari requires this; Chrome and
 * Firefox 127+ accept it too). Without ClipboardItem or a secure context (plain
 * http) it waits for the text and tries the plain path, which may be refused
 * by then. `make` must not reject.
 */
export async function copyTextLater(make: () => Promise<string>, what = 'Copied'): Promise<boolean> {
  const text = make()
  if (window.isSecureContext && typeof ClipboardItem !== 'undefined' && navigator.clipboard?.write) {
    try {
      const blob = text.then((t) => new Blob([t], { type: 'text/plain' }))
      await navigator.clipboard.write([new ClipboardItem({ 'text/plain': blob })])
      toast.success(what)
      return true
    } catch {
      // fall through to the text path
    }
  }
  return copyText(await text, what)
}

/** Copy text to the clipboard and say so. Returns whether it was copied. */
export async function copyText(text: string, what = 'Copied'): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text)
    toast.success(what)
    return true
  } catch {
    // Clipboard API needs a secure context; fall back to a hidden textarea.
    const ta = document.createElement('textarea')
    ta.value = text
    ta.style.position = 'fixed'
    ta.style.opacity = '0'
    document.body.appendChild(ta)
    ta.select()
    let ok: boolean
    try {
      // Returns false (rather than throwing) when the browser refuses.
      ok = document.execCommand('copy')
    } catch {
      ok = false
    } finally {
      document.body.removeChild(ta)
    }
    if (ok) toast.success(what)
    else toast.error('Could not copy', { description: 'Your browser blocked it. Try again.' })
    return ok
  }
}

/** A warning: it partly worked (the description says what did not). */
export function toastWarn(message: string, description?: string) {
  toast.warning(message, { description })
}

/**
 * A loading toast for an action that takes a while, so its dialog can close
 * at once and the action ends in the background. The toast turns into the
 * result: ok, warn or fail. Return it from a mutation's onMutate to get it
 * back in onSuccess and onError.
 */
export function toastLoading(message: string) {
  const id = toast.loading(message)
  return {
    ok: (m: string, description?: string) => void toast.success(m, { id, description }),
    warn: (m: string, description?: string) => void toast.warning(m, { id, description }),
    fail: (e: unknown, fallback?: string) => toastError(e, fallback, id),
    dismiss: () => void toast.dismiss(id),
  }
}
export type LoadingToast = ReturnType<typeof toastLoading>
