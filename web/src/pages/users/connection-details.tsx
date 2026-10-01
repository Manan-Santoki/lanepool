import { Link } from "@tanstack/react-router"
import { InfoIcon } from "lucide-react"
import { Skeleton } from "@/components/ui/skeleton"
import { CopyButton, CopyField } from "@/components/copy-button"
import { useSettings } from "@/hooks/use-settings"
import { buildProxyUrls, curlExample, usernameExamples } from "@/lib/proxy-urls"

/** Ready-made proxy URLs, username parameter examples and a curl one-liner for a proxy user. */
export function ConnectionDetails({ username, password }: { username: string; password?: string }) {
  const settings = useSettings()
  if (settings.isPending) return <Skeleton className="h-48 w-full" />
  const app = settings.data?.app
  const urls = buildProxyUrls(app, username, password)
  const curl = curlExample(urls[0])
  const missingHost = !app?.publicProxyHost

  return (
    <div className="space-y-5">
      {!password ? (
        <p className="flex items-start gap-2 rounded-md border bg-muted/40 p-3 text-xs text-muted-foreground">
          <InfoIcon className="mt-0.5 size-3.5 shrink-0" />
          Passwords are only shown once. Replace PASSWORD with the user’s password, or reset it to get a new one.
        </p>
      ) : null}

      <section className="space-y-2">
        <h4 className="text-sm font-medium">Proxy URLs</h4>
        {urls.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            No public proxy port is configured.{" "}
            <Link to="/settings" search={{ tab: "app" }} className="underline underline-offset-4">
              Set the public host and ports
            </Link>{" "}
            to get ready-made URLs.
          </p>
        ) : (
          <div className="space-y-2">
            {urls.map((u) => (
              <div key={u.kind} className="space-y-1">
                <p className="text-xs text-muted-foreground">{u.label}</p>
                <CopyField value={u.url} label={u.label} />
              </div>
            ))}
            {missingHost ? (
              <p className="text-xs text-muted-foreground">
                Using this browser’s hostname because no public proxy host is set in Settings → App.
              </p>
            ) : null}
          </div>
        )}
      </section>

      <section className="space-y-2">
        <h4 className="text-sm font-medium">Username parameters</h4>
        <p className="text-xs text-muted-foreground">
          Append parameters to the username to steer routing. The password stays the same.
        </p>
        <ul className="divide-y rounded-md border">
          {usernameExamples(username).map((ex) => (
            <li key={ex.value} className="flex items-center gap-2 px-3 py-2">
              <div className="min-w-0 flex-1">
                <code className="block truncate font-mono text-xs">{ex.value}</code>
                <p className="text-xs text-muted-foreground">{ex.description}</p>
              </div>
              <CopyButton value={ex.value} label="Copy username" size="icon-sm" />
            </li>
          ))}
        </ul>
      </section>

      <section className="space-y-2">
        <h4 className="text-sm font-medium">Test with curl</h4>
        <div className="relative rounded-md border bg-muted/40">
          <pre className="overflow-x-auto p-3 pr-10 font-mono text-xs leading-relaxed whitespace-pre">{curl}</pre>
          <CopyButton value={curl} label="Copy command" size="icon-sm" className="absolute top-1.5 right-1.5" />
        </div>
      </section>
    </div>
  )
}
