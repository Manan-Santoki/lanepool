import { Link } from "@tanstack/react-router"
import { CircleSlashIcon, PauseCircleIcon, PlugZapIcon } from "lucide-react"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { useNow } from "@/hooks/use-now"
import { formatDateTime, formatRelative, parseDate } from "@/lib/format"
import type { EngineStatus, GatewayState } from "@/lib/types"

/** Prominent banners for the circuit breaker, maintenance mode and an offline engine. */
export function StatusBanners({
  gateway,
  maintenance,
  engine,
}: {
  gateway?: GatewayState
  maintenance?: boolean
  engine?: EngineStatus
}) {
  const now = useNow()
  const pausedUntil = parseDate(gateway?.pausedUntil)
  const breakerOpen = pausedUntil !== null && pausedUntil.getTime() > now.getTime()

  return (
    <>
      {breakerOpen ? (
        <Alert className="border-red-500/40 bg-red-500/8 text-red-900 dark:text-red-200 [&>svg]:text-red-600 dark:[&>svg]:text-red-400">
          <CircleSlashIcon />
          <AlertTitle>New lane connections paused</AlertTitle>
          <AlertDescription className="text-red-900/80 dark:text-red-200/80">
            <p>
              New lane connections are paused until{" "}
              <strong className="tabular font-semibold">{formatDateTime(gateway?.pausedUntil, "HH:mm:ss")}</strong> (
              {formatRelative(gateway?.pausedUntil, now)}) after repeated failures. This circuit breaker protects your
              provider account from being flagged; lanes that are already up keep serving traffic.
            </p>
          </AlertDescription>
        </Alert>
      ) : null}
      {maintenance ? (
        <Alert className="border-amber-500/40 bg-amber-500/8 text-amber-900 dark:text-amber-200 [&>svg]:text-amber-600 dark:[&>svg]:text-amber-400">
          <PauseCircleIcon />
          <AlertTitle>Maintenance mode is on</AlertTitle>
          <AlertDescription className="text-amber-900/80 dark:text-amber-200/80">
            <p>New proxy connections are being rejected. Lanes stay connected.</p>
            <Button asChild size="xs" variant="outline" className="mt-1">
              <Link to="/settings" search={{ tab: "engine" }}>
                Open engine settings
              </Link>
            </Button>
          </AlertDescription>
        </Alert>
      ) : null}
      {engine && !engine.connected ? (
        <Alert variant="destructive">
          <PlugZapIcon />
          <AlertTitle>Engine offline</AlertTitle>
          <AlertDescription>
            <p>
              The engine has not reported
              {engine.lastReportAt ? ` since ${formatRelative(engine.lastReportAt, now)}` : " yet"}. Lane data may be out
              of date.
            </p>
          </AlertDescription>
        </Alert>
      ) : null}
    </>
  )
}
