import { Link } from "@tanstack/react-router"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { LANE_STATUS_META, tone } from "@/lib/constants"
import { flagEmoji } from "@/lib/format"
import type { Lane } from "@/lib/types"
import { cn } from "@/lib/utils"

/** One small coloured square per lane, with name/status/exit IP on hover. */
export function LaneGrid({ lanes }: { lanes: Lane[] }) {
  return (
    <div className="flex flex-wrap gap-1" role="list" aria-label="Lane status grid">
      {lanes.map((lane) => {
        const meta = LANE_STATUS_META[lane.status] ?? LANE_STATUS_META.down
        return (
          <Tooltip key={lane.id}>
            <TooltipTrigger asChild>
              <Link
                to="/lanes"
                role="listitem"
                aria-label={`${lane.name}: ${meta.label}`}
                className={cn(
                  "size-4 rounded-[3px] ring-offset-background transition-transform hover:scale-125 focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-1 focus-visible:outline-none",
                  tone(meta.tone).dot,
                  lane.status === "connecting" && "animate-pulse motion-reduce:animate-none",
                )}
              />
            </TooltipTrigger>
            <TooltipContent className="space-y-0.5">
              <p className="font-medium">
                {flagEmoji(lane.countryCode)} {lane.name}
              </p>
              <p>{meta.label}</p>
              <p className="font-mono">{lane.exitIp ?? "no exit IP"}</p>
            </TooltipContent>
          </Tooltip>
        )
      })}
    </div>
  )
}

export function LaneGridLegend({ lanes }: { lanes: Lane[] }) {
  const counts = new Map<string, number>()
  for (const l of lanes) counts.set(l.status, (counts.get(l.status) ?? 0) + 1)
  return (
    <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
      {Object.entries(LANE_STATUS_META)
        .filter(([status]) => counts.get(status))
        .map(([status, meta]) => (
          <span key={status} className="inline-flex items-center gap-1.5">
            <span aria-hidden className={cn("size-2.5 rounded-[2px]", tone(meta.tone).dot)} />
            {meta.label}
            <span className="tabular font-medium text-foreground">{counts.get(status)}</span>
          </span>
        ))}
    </div>
  )
}
