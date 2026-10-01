import { LevelBadge } from "@/components/badges"
import { RelativeTime } from "@/components/time"
import type { AppEvent } from "@/lib/types"

export function EventList({ events }: { events: AppEvent[] }) {
  return (
    <ul className="divide-y">
      {events.map((ev) => (
        <li key={ev.id} className="flex items-start gap-3 py-2.5 first:pt-0 last:pb-0">
          <div className="w-14 shrink-0 pt-0.5">
            <LevelBadge level={ev.level} />
          </div>
          <div className="min-w-0 flex-1 space-y-0.5">
            <p className="text-sm leading-snug break-words">{ev.message}</p>
            <p className="flex flex-wrap gap-x-2 text-xs text-muted-foreground">
              <span className="font-mono">{ev.type}</span>
              {ev.laneId ? <span className="font-mono">{ev.laneId}</span> : null}
              {ev.actor ? <span>by {ev.actor}</span> : null}
            </p>
          </div>
          <RelativeTime value={ev.time} className="tabular shrink-0 text-xs whitespace-nowrap text-muted-foreground" />
        </li>
      ))}
    </ul>
  )
}
