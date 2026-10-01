import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { useNow } from "@/hooks/use-now"
import { formatAge, formatDateTime, formatRelative } from "@/lib/format"

/** "3m ago" / "in 20s" that ticks, with the absolute time in a tooltip. */
export function RelativeTime({
  value,
  mode = "relative",
  fallback = "–",
  className,
}: {
  value: string | null | undefined
  mode?: "relative" | "age"
  fallback?: string
  className?: string
}) {
  const now = useNow()
  if (!value) return <span className={className ?? "text-muted-foreground"}>{fallback}</span>
  const text = mode === "age" ? formatAge(value, now) : formatRelative(value, now)
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <time dateTime={value} className={className ?? "tabular whitespace-nowrap"}>
          {text}
        </time>
      </TooltipTrigger>
      <TooltipContent>{formatDateTime(value)}</TooltipContent>
    </Tooltip>
  )
}
