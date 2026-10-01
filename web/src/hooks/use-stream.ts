import { useEffect, useSyncExternalStore } from "react"
import { useQueryClient, type QueryClient } from "@tanstack/react-query"
import type { AppEvent, Overview, StreamState } from "@/lib/types"
import { qk } from "@/hooks/query-keys"

/**
 * Live state from GET /api/stream (Server-Sent Events).
 *  - `state` events carry { lanes, gateway, engine, activeConnections } about every 2 s.
 *    Lanes are written straight into the ["lanes"] query cache so tables update without refetching.
 *  - `event` events carry a single Event and are prepended to the overview's recent events.
 * When no `state` arrives for STALE_MS the stream counts as down and pages fall back to polling.
 */

const STALE_MS = 8_000

interface Snapshot {
  live: boolean
  state: StreamState | null
  receivedAt: number
}

let snapshot: Snapshot = { live: false, state: null, receivedAt: 0 }
const listeners = new Set<() => void>()

function emit(next: Snapshot) {
  snapshot = next
  listeners.forEach((l) => l())
}

function subscribe(listener: () => void) {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

function handleState(qc: QueryClient, data: StreamState) {
  qc.setQueryData(qk.lanes, data.lanes)
  qc.setQueryData<Overview>(qk.overview, (prev) =>
    prev ? { ...prev, gateway: data.gateway, engine: data.engine, activeConnections: data.activeConnections } : prev,
  )
  emit({ live: true, state: data, receivedAt: Date.now() })
}

function handleEvent(qc: QueryClient, ev: AppEvent) {
  qc.setQueryData<Overview>(qk.overview, (prev) => {
    if (!prev || prev.recentEvents.some((e) => e.id === ev.id)) return prev
    return { ...prev, recentEvents: [ev, ...prev.recentEvents].slice(0, Math.max(prev.recentEvents.length, 20)) }
  })
}

/** Opens the SSE connection for the lifetime of the authenticated app shell. */
export function useStreamConnection(enabled: boolean) {
  const qc = useQueryClient()

  useEffect(() => {
    if (!enabled || typeof EventSource === "undefined") return
    const source = new EventSource("/api/stream", { withCredentials: true })

    const onState = (e: MessageEvent<string>) => {
      try {
        handleState(qc, JSON.parse(e.data) as StreamState)
      } catch {
        // Ignore malformed frames; the staleness check falls back to polling.
      }
    }
    const onEvent = (e: MessageEvent<string>) => {
      try {
        handleEvent(qc, JSON.parse(e.data) as AppEvent)
      } catch {
        // ignore
      }
    }
    source.addEventListener("state", onState)
    source.addEventListener("event", onEvent)

    const watchdog = setInterval(() => {
      if (snapshot.live && Date.now() - snapshot.receivedAt > STALE_MS) emit({ ...snapshot, live: false })
    }, 2_000)
    const onError = () => {
      if (source.readyState === EventSource.CLOSED && snapshot.live) emit({ ...snapshot, live: false })
    }
    source.addEventListener("error", onError)

    return () => {
      clearInterval(watchdog)
      source.close()
      emit({ ...snapshot, live: false })
    }
  }, [enabled, qc])
}

/** True while `state` frames are arriving. */
export function useStreamLive(): boolean {
  return useSyncExternalStore(subscribe, () => snapshot.live)
}

/** Latest `state` frame (null until the first one). */
export function useStreamState(): StreamState | null {
  return useSyncExternalStore(subscribe, () => snapshot.state)
}
