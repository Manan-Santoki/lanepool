import { countryName, flagEmoji } from "@/lib/format"
import { cn } from "@/lib/utils"

export function Flag({ code, className }: { code?: string; className?: string }) {
  const flag = flagEmoji(code)
  if (!flag) return null
  return (
    <span role="img" aria-label={countryName(code)} className={cn("leading-none", className)}>
      {flag}
    </span>
  )
}

export function CountryLabel({ code, city, className }: { code?: string; city?: string; className?: string }) {
  if (!code && !city) return <span className="text-muted-foreground">–</span>
  return (
    <span className={cn("inline-flex items-center gap-1.5 whitespace-nowrap", className)} title={countryName(code)}>
      <Flag code={code} />
      <span>{city || countryName(code)}</span>
      {city && code ? <span className="text-xs text-muted-foreground uppercase">{code}</span> : null}
    </span>
  )
}
