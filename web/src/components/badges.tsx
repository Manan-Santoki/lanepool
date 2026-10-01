import type { ReactNode } from "react"
import { Badge } from "@/components/ui/badge"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import {
  CONN_RESULT_META,
  EVENT_LEVEL_TONE,
  LANE_STATUS_META,
  tone,
  type ToneName,
} from "@/lib/constants"
import type { ConnResult, LaneStatus } from "@/lib/types"
import { cn } from "@/lib/utils"

export function ToneBadge({
  toneName,
  children,
  dot = false,
  className,
}: {
  toneName: ToneName
  children: ReactNode
  dot?: boolean
  className?: string
}) {
  const t = tone(toneName)
  return (
    <Badge variant="outline" className={cn("gap-1.5 font-medium", t.badge, className)}>
      {dot ? <span aria-hidden className={cn("size-1.5 rounded-full", t.dot)} /> : null}
      {children}
    </Badge>
  )
}

export function LaneStatusBadge({ status }: { status: LaneStatus }) {
  const meta = LANE_STATUS_META[status] ?? LANE_STATUS_META.down
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span className="inline-flex">
          <ToneBadge toneName={meta.tone} dot>
            {meta.label}
          </ToneBadge>
        </span>
      </TooltipTrigger>
      <TooltipContent>{meta.description}</TooltipContent>
    </Tooltip>
  )
}

export function ResultBadge({ result }: { result: ConnResult }) {
  const meta = CONN_RESULT_META[result] ?? { label: result, tone: "gray" as const }
  return <ToneBadge toneName={meta.tone}>{meta.label}</ToneBadge>
}

export function LevelBadge({ level }: { level: "info" | "warn" | "error" }) {
  return (
    <ToneBadge toneName={EVENT_LEVEL_TONE[level] ?? "gray"} className="uppercase tracking-wide text-[10px]">
      {level}
    </ToneBadge>
  )
}

export function Chip({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <Badge variant="secondary" className={cn("font-normal", className)}>
      {children}
    </Badge>
  )
}
