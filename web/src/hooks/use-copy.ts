import { useCallback, useEffect, useRef, useState } from "react"
import { toast } from "sonner"

async function writeClipboard(text: string) {
  if (navigator.clipboard && window.isSecureContext) {
    await navigator.clipboard.writeText(text)
    return
  }
  // Fallback for plain-HTTP deployments where the async clipboard API is unavailable.
  const el = document.createElement("textarea")
  el.value = text
  el.setAttribute("readonly", "")
  el.style.position = "fixed"
  el.style.opacity = "0"
  document.body.appendChild(el)
  el.select()
  const ok = document.execCommand("copy")
  document.body.removeChild(el)
  if (!ok) throw new Error("copy failed")
}

export function useCopy(resetMs = 1500) {
  const [copied, setCopied] = useState(false)
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => () => {
    if (timer.current) clearTimeout(timer.current)
  }, [])

  const copy = useCallback(
    async (text: string) => {
      try {
        await writeClipboard(text)
        setCopied(true)
        if (timer.current) clearTimeout(timer.current)
        timer.current = setTimeout(() => setCopied(false), resetMs)
      } catch {
        toast.error("Could not copy to the clipboard")
      }
    },
    [resetMs],
  )

  return { copied, copy }
}
