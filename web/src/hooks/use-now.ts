import { useSyncExternalStore } from "react"

// One shared 1 s ticker for every relative timestamp on screen.
let now = Date.now()
const listeners = new Set<() => void>()
let timer: ReturnType<typeof setInterval> | null = null

function subscribe(listener: () => void) {
  listeners.add(listener)
  if (!timer) {
    // The value is stale while nothing is subscribed; refresh it before the
    // first tick, or recent timestamps render as "in 17s".
    now = Date.now()
    timer = setInterval(() => {
      now = Date.now()
      listeners.forEach((l) => l())
    }, 1000)
  }
  return () => {
    listeners.delete(listener)
    if (listeners.size === 0 && timer) {
      clearInterval(timer)
      timer = null
    }
  }
}

/** Current time, re-rendering once per second. */
export function useNow(): Date {
  const t = useSyncExternalStore(subscribe, () => now)
  return new Date(t)
}
