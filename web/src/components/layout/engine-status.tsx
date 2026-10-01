import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { useOverview } from "@/hooks/use-overview"
import { useNow } from "@/hooks/use-now"
import { useStreamLive, useStreamState } from "@/hooks/use-stream"
import { formatRelative } from "@/lib/format"
import type { EngineStatus } from "@/lib/types"
import { cn } from "@/lib/utils"

function useEngineStatus(): EngineStatus | undefined {
  const stream = useStreamState()
  const overview = useOverview().data
  return stream?.engine ?? overview?.engine
}

/** Small coloured dot + label for engine connectivity. */
export function EngineStatusIndicator({ showLabel = true, className }: { showLabel?: boolean; className?: string }) {
  const engine = useEngineStatus()
  const live = useStreamLive()
  const now = useNow()
  const state = !engine ? "unknown" : engine.connected ? "connected" : "offline"
  const label = state === "connected" ? "Engine connected" : state === "offline" ? "Engine offline" : "Engine status unknown"
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span
          className={cn("inline-flex items-center gap-2 rounded-md px-2 py-1 text-xs text-muted-foreground", className)}
          tabIndex={0}
          aria-label={label}
        >
          <span className="relative flex size-2.5">
            {state === "connected" ? (
              <span className="absolute inline-flex size-full animate-ping rounded-full bg-emerald-500 opacity-50 motion-reduce:animate-none" />
            ) : null}
            <span
              className={cn(
                "relative inline-flex size-2.5 rounded-full",
                state === "connected" && "bg-emerald-500",
                state === "offline" && "bg-red-500",
                state === "unknown" && "bg-muted-foreground/40",
              )}
            />
          </span>
          {showLabel ? <span className="hidden sm:inline">{state === "connected" ? "Engine" : label}</span> : null}
        </span>
      </TooltipTrigger>
      <TooltipContent className="space-y-0.5">
        <p className="font-medium">{label}</p>
        {engine?.nodeId ? <p>Node {engine.nodeId}</p> : null}
        {engine?.lastReportAt ? <p>Last report {formatRelative(engine.lastReportAt, now)}</p> : null}
        {engine?.startedAt ? <p>Started {formatRelative(engine.startedAt, now)}</p> : null}
        <p className="opacity-70">{live ? "Live updates on" : "Live updates off (polling)"}</p>
      </TooltipContent>
    </Tooltip>
  )
}
