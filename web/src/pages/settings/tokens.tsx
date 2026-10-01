import { useMemo, useState, type FormEvent } from "react"
import type { ColumnDef } from "@tanstack/react-table"
import { format } from "date-fns"
import { AlertTriangleIcon, KeySquareIcon, PlusIcon, Trash2Icon } from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Checkbox } from "@/components/ui/checkbox"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import { ToneBadge } from "@/components/badges"
import { ConfirmDialog } from "@/components/confirm-dialog"
import { CopyField } from "@/components/copy-button"
import { DataTable } from "@/components/data-table"
import { FormError, FormField } from "@/components/form-field"
import { EmptyState, ErrorState } from "@/components/states"
import { RelativeTime } from "@/components/time"
import { WriteOnly } from "@/components/write-only"
import { useCanWrite } from "@/hooks/use-auth"
import { useNow } from "@/hooks/use-now"
import { useCreateToken, useRevokeToken, useTokens } from "@/hooks/use-settings"
import { errorMessage, fieldErrors } from "@/lib/api"
import { TOKEN_SCOPES } from "@/lib/constants"
import { parseDate } from "@/lib/format"
import type { ApiToken, TokenScope } from "@/lib/types"

function ExpiryCell({ value }: { value?: string }) {
  const now = useNow()
  const d = parseDate(value)
  if (!d) return <span className="text-muted-foreground">Never</span>
  if (d.getTime() < now.getTime()) return <ToneBadge toneName="gray">Expired</ToneBadge>
  return <RelativeTime value={value} />
}

export function TokensSettings() {
  const tokens = useTokens()
  const canWrite = useCanWrite()
  const revoke = useRevokeToken()
  const [createOpen, setCreateOpen] = useState(false)
  const [created, setCreated] = useState<{ token: string; apiToken: ApiToken } | null>(null)

  const columns = useMemo<ColumnDef<ApiToken>[]>(() => {
    const cols: ColumnDef<ApiToken>[] = [
      { id: "name", accessorKey: "name", header: "Name", cell: ({ row }) => <span className="font-medium">{row.original.name}</span> },
      {
        id: "prefix",
        accessorKey: "prefix",
        header: "Token",
        cell: ({ row }) => <code className="font-mono text-xs">{row.original.prefix}…</code>,
      },
      {
        id: "scopes",
        header: "Scopes",
        enableSorting: false,
        cell: ({ row }) => (
          <div className="flex gap-1">
            {row.original.scopes.map((s) => (
              <Badge key={s} variant="secondary" className="font-mono text-[11px] font-normal">
                {s}
              </Badge>
            ))}
          </div>
        ),
      },
      { id: "created", accessorKey: "createdAt", header: "Created", cell: ({ row }) => <RelativeTime value={row.original.createdAt} /> },
      {
        id: "lastUsed",
        accessorFn: (t) => t.lastUsedAt ?? "",
        header: "Last used",
        cell: ({ row }) => <RelativeTime value={row.original.lastUsedAt} fallback="Never" />,
      },
      { id: "expires", accessorFn: (t) => t.expiresAt ?? "9999", header: "Expires", cell: ({ row }) => <ExpiryCell value={row.original.expiresAt} /> },
    ]
    if (canWrite) {
      cols.push({
        id: "actions",
        header: () => <span className="sr-only">Actions</span>,
        enableSorting: false,
        meta: { className: "w-10" },
        cell: ({ row }) => (
          <ConfirmDialog
            trigger={
              <Button variant="ghost" size="icon-sm" aria-label={`Revoke ${row.original.name}`}>
                <Trash2Icon />
              </Button>
            }
            title={`Revoke “${row.original.name}”?`}
            description={<p>Apps using this token stop working immediately.</p>}
            confirmLabel="Revoke"
            destructive
            onConfirm={() => revoke.mutateAsync(row.original)}
          />
        ),
      })
    }
    return cols
  }, [canWrite, revoke])

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <KeySquareIcon className="size-4 text-muted-foreground" /> API tokens
        </CardTitle>
        <CardDescription>
          For apps and scripts: send <code className="font-mono text-xs">Authorization: Bearer &lt;token&gt;</code>.
        </CardDescription>
        <CardAction>
          <WriteOnly>
            <Button size="sm" variant="outline" onClick={() => setCreateOpen(true)}>
              <PlusIcon /> Create token
            </Button>
          </WriteOnly>
        </CardAction>
      </CardHeader>
      <CardContent>
        {tokens.isError && !tokens.data ? (
          <ErrorState error={tokens.error} onRetry={() => void tokens.refetch()} />
        ) : (
          <div className="overflow-hidden rounded-md border">
            <DataTable
              columns={columns}
              data={tokens.data}
              isLoading={tokens.isPending}
              skeletonRows={2}
              getRowId={(t) => String(t.id)}
              empty={<EmptyState icon={<KeySquareIcon />} title="No API tokens" className="py-8" />}
            />
          </div>
        )}
      </CardContent>

      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogContent className="grid-cols-[minmax(0,1fr)] sm:max-w-md">
          {createOpen ? (
            <CreateTokenForm
              onDone={(res) => {
                setCreateOpen(false)
                if (res) setCreated(res)
              }}
            />
          ) : null}
        </DialogContent>
      </Dialog>

      <Dialog open={created !== null} onOpenChange={(o) => !o && setCreated(null)}>
        <DialogContent className="grid-cols-[minmax(0,1fr)] sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>Token “{created?.apiToken.name}” created</DialogTitle>
            <DialogDescription className="flex items-start gap-2">
              <AlertTriangleIcon className="mt-0.5 size-4 shrink-0 text-amber-600" />
              Copy it now. It will not be shown again.
            </DialogDescription>
          </DialogHeader>
          {created ? <CopyField value={created.token} label="token" /> : null}
          <DialogFooter>
            <Button onClick={() => setCreated(null)}>Done</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </Card>
  )
}

function CreateTokenForm({ onDone }: { onDone: (res?: { token: string; apiToken: ApiToken }) => void }) {
  const create = useCreateToken()
  const [name, setName] = useState("")
  const [scopes, setScopes] = useState<TokenScope[]>(["rotate"])
  const [expires, setExpires] = useState("")
  const [today] = useState(() => format(new Date(), "yyyy-MM-dd"))
  const [local, setLocal] = useState<Record<string, string>>({})
  const server = fieldErrors(create.error)
  const errors = { ...server, ...local }
  const general = create.error && Object.keys(server).length === 0 ? errorMessage(create.error) : null

  const submit = (e: FormEvent) => {
    e.preventDefault()
    const errs: Record<string, string> = {}
    if (!name.trim()) errs.name = "Name is required."
    if (scopes.length === 0) errs.scopes = "Choose at least one scope."
    setLocal(errs)
    if (Object.keys(errs).length) return
    create.mutate(
      {
        name: name.trim(),
        scopes,
        expiresAt: expires ? new Date(`${expires}T23:59:59`).toISOString() : undefined,
      },
      { onSuccess: (res) => onDone(res) },
    )
  }

  return (
    <form onSubmit={submit} className="space-y-4" noValidate>
      <DialogHeader>
        <DialogTitle>Create API token</DialogTitle>
        <DialogDescription>Give each app its own token with the smallest scope it needs.</DialogDescription>
      </DialogHeader>
      <FormError message={general} />
      <FormField label="Name" htmlFor="tk-name" error={errors.name}>
        <Input id="tk-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="scraper-prod" autoFocus />
      </FormField>
      <fieldset className="space-y-2">
        <legend className="mb-2 text-sm font-medium">Scopes</legend>
        {TOKEN_SCOPES.map((s) => (
          <label key={s.value} className="flex items-start gap-3 rounded-md border p-3">
            <Checkbox
              checked={scopes.includes(s.value)}
              onCheckedChange={(c) => setScopes((cur) => (c === true ? [...cur, s.value] : cur.filter((x) => x !== s.value)))}
              className="mt-0.5"
            />
            <span className="space-y-0.5">
              <span className="block font-mono text-sm">{s.label}</span>
              <span className="block text-xs text-muted-foreground">{s.description}</span>
            </span>
          </label>
        ))}
        {errors.scopes ? <p className="text-sm text-destructive">{errors.scopes}</p> : null}
      </fieldset>
      <FormField label="Expires" htmlFor="tk-exp" error={errors.expiresAt} description="Optional. Empty = never expires.">
        <Input
          id="tk-exp"
          type="date"
          min={today}
          value={expires}
          onChange={(e) => setExpires(e.target.value)}
          className="w-48"
        />
      </FormField>
      <DialogFooter>
        <Button type="button" variant="outline" onClick={() => onDone()}>
          Cancel
        </Button>
        <Button type="submit" disabled={create.isPending}>
          {create.isPending ? <Spinner /> : null}
          Create token
        </Button>
      </DialogFooter>
    </form>
  )
}
