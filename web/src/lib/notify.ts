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
