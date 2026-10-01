import { Badge } from "@/components/ui/badge"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { ChangePasswordForm } from "@/components/change-password-form"
import { RelativeTime } from "@/components/time"
import { useMe } from "@/hooks/use-auth"

export function AccountSettings() {
  const me = useMe()
  return (
    <div className="grid gap-4 lg:grid-cols-2">
      <Card>
        <CardHeader>
          <CardTitle>Your account</CardTitle>
          <CardDescription>Signed in as this admin.</CardDescription>
        </CardHeader>
        <CardContent>
          {me ? (
            <dl className="grid grid-cols-[auto_1fr] gap-x-6 gap-y-3 text-sm">
              <dt className="text-muted-foreground">Name</dt>
              <dd>{me.name || "–"}</dd>
              <dt className="text-muted-foreground">Email</dt>
              <dd className="break-all">{me.email}</dd>
              <dt className="text-muted-foreground">Role</dt>
              <dd>
                <Badge variant={me.role === "admin" ? "default" : "secondary"} className="capitalize">
                  {me.role}
                </Badge>
                {me.role === "viewer" ? <span className="ml-2 text-xs text-muted-foreground">read-only access</span> : null}
              </dd>
              <dt className="text-muted-foreground">Created</dt>
              <dd>
                <RelativeTime value={me.createdAt} />
              </dd>
              <dt className="text-muted-foreground">Last login</dt>
              <dd>
                <RelativeTime value={me.lastLoginAt} fallback="–" />
              </dd>
            </dl>
          ) : null}
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>Change password</CardTitle>
          <CardDescription>Use a long, unique password.</CardDescription>
        </CardHeader>
        <CardContent>
          <ChangePasswordForm />
        </CardContent>
      </Card>
    </div>
  )
}
