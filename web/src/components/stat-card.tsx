import type { ReactNode } from "react"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"

export function StatCard({
  title,
  icon,
  value,
  sub,
  loading,
  children,
}: {
  title: string
  icon?: ReactNode
  value: ReactNode
  sub?: ReactNode
  loading?: boolean
  children?: ReactNode
}) {
  return (
    <Card className="gap-2 py-4">
      <CardHeader className="flex flex-row items-center justify-between gap-2 px-4">
        <CardTitle className="text-sm font-medium text-muted-foreground">{title}</CardTitle>
        <span className="text-muted-foreground [&_svg]:size-4" aria-hidden>
          {icon}
        </span>
      </CardHeader>
      <CardContent className="space-y-1 px-4">
        {loading ? (
          <>
            <Skeleton className="h-8 w-24" />
            <Skeleton className="h-4 w-32" />
          </>
        ) : (
          <>
            <div className="tabular text-2xl font-semibold tracking-tight">{value}</div>
            {sub ? <div className="text-xs text-muted-foreground">{sub}</div> : null}
            {children}
          </>
        )}
      </CardContent>
    </Card>
  )
}
