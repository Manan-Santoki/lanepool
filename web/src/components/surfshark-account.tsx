import { useMemo, useState, type FormEvent } from "react"
import type { ColumnDef } from "@tanstack/react-table"
import {
  AlertTriangleIcon,
  KeyRoundIcon,
  LinkIcon,
  PlusIcon,
  Trash2Icon,
  UnplugIcon,
  UserRoundIcon,
} from "lucide-react"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Separator } from "@/components/ui/separator"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { Switch } from "@/components/ui/switch"
import { ConfirmDialog } from "@/components/confirm-dialog"
import { CopyButton } from "@/components/copy-button"
import { DataTable } from "@/components/data-table"
import { FormError, FormField } from "@/components/form-field"
import { EmptyState, ErrorState } from "@/components/states"
import { RelativeTime } from "@/components/time"
import { WriteOnly } from "@/components/write-only"
import { useCanWrite } from "@/hooks/use-auth"
import {
  useConnectSurfsharkAccount,
  useDeleteRemoteKey,
  useDisconnectSurfsharkAccount,
  useGenerateSurfsharkKeys,
  useSurfsharkAccount,
  useUpdateSurfsharkAccount,
} from "@/hooks/use-providers"
import { errorMessage, fieldErrors } from "@/lib/api"
import { truncateMiddle } from "@/lib/format"
import type { SurfsharkAccount, SurfsharkRemoteKey } from "@/lib/types"

/** Connect a Surfshark account so lanepool can create, rotate and delete WireGuard keys itself. */
export function SurfsharkAccountCard() {
  const account = useSurfsharkAccount()
  const a = account.data
  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <UserRoundIcon className="size-4 text-muted-foreground" /> Surfshark account
        </CardTitle>
        <CardDescription>
          {a?.connected ? (
            <>
              Connected as <span className="font-medium text-foreground">{a.email}</span> · checked{" "}
              <RelativeTime value={a.lastSyncAt ?? undefined} fallback="never" />
            </>
          ) : (
            "Let lanepool create, rotate and delete WireGuard keys in your Surfshark account."
          )}
        </CardDescription>
        {a?.connected ? (
          <CardAction>
            <WriteOnly>
              <DisconnectButton email={a.email} />
            </WriteOnly>
          </CardAction>
        ) : null}
      </CardHeader>
      <CardContent className="space-y-6">
        {account.isPending ? (
          <Skeleton className="h-32 w-full" />
        ) : account.isError && !a ? (
          <ErrorState error={account.error} onRetry={() => void account.refetch()} />
        ) : a && !a.connected ? (
          <ConnectForm />
        ) : a ? (
          <ConnectedView account={a} />
        ) : null}
      </CardContent>
    </Card>
  )
}

function ConnectForm() {
  const canWrite = useCanWrite()
  const connect = useConnectSurfsharkAccount()
  const [email, setEmail] = useState("")
  const [password, setPassword] = useState("")
  const server = fieldErrors(connect.error)
  const general = connect.error && Object.keys(server).length === 0 ? errorMessage(connect.error) : null

  const submit = (e: FormEvent) => {
    e.preventDefault()
    connect.mutate({ email, password })
  }

  return (
    <form onSubmit={submit} className="space-y-4" noValidate>
      <div className="space-y-2 text-sm text-muted-foreground">
        <p>With your account connected, lanepool can:</p>
        <ul className="list-disc space-y-1 pl-5">
          <li>generate new keys and register them at Surfshark in one click;</li>
          <li>
            delete keys at Surfshark, which <strong className="text-foreground">ends every session</strong> using them
            immediately (the only way to clear old sessions, since WireGuard has no disconnect);
          </li>
          <li>rotate a key when its lanes keep failing, renew keys before they expire, and add keys as lanes grow.</li>
        </ul>
        <p>
          This uses the account API that Surfshark's own apps use. It isn't officially documented, so it could change.
          Your password is stored encrypted. Accounts with two-factor login may not be supported.
        </p>
      </div>
      <FormError message={general} />
      <fieldset disabled={!canWrite || connect.isPending} className="grid gap-4 sm:grid-cols-2">
        <FormField label="Surfshark email" htmlFor="ss-email" error={server.email}>
          <Input
            id="ss-email"
            type="email"
            autoComplete="off"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
          />
        </FormField>
        <FormField label="Surfshark password" htmlFor="ss-password" error={server.password}>
          <Input
            id="ss-password"
            type="password"
            autoComplete="new-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </FormField>
      </fieldset>
      {canWrite ? (
        <div className="flex justify-end">
          <Button type="submit" disabled={!email || !password || connect.isPending}>
            {connect.isPending ? <Spinner /> : <LinkIcon />}
            Connect account
          </Button>
        </div>
      ) : null}
    </form>
  )
}

function DisconnectButton({ email }: { email: string }) {
  const disconnect = useDisconnectSurfsharkAccount()
  return (
    <ConfirmDialog
      trigger={
        <Button size="sm" variant="outline">
          <UnplugIcon /> Disconnect
        </Button>
      }
      title="Disconnect the Surfshark account?"
      description={
        <p>
          lanepool forgets the login for {email}. Your keys and lanes keep working, but lanepool can no longer create,
          rotate or delete keys at Surfshark.
        </p>
      }
      confirmLabel="Disconnect"
      destructive
      onConfirm={() => disconnect.mutateAsync()}
    />
  )
}

function ConnectedView({ account }: { account: SurfsharkAccount }) {
  return (
    <>
      {account.lastError ? (
        <Alert variant="destructive">
          <AlertTriangleIcon />
          <AlertTitle>Last request to Surfshark failed</AlertTitle>
          <AlertDescription>{account.lastError}</AlertDescription>
        </Alert>
      ) : null}
      <GenerateKeys />
      <Separator />
      <AutoManageForm account={account} />
      <Separator />
      <RemoteKeys keys={account.remoteKeys ?? []} />
    </>
  )
}

function GenerateKeys() {
  const canWrite = useCanWrite()
  const generate = useGenerateSurfsharkKeys()
  const [count, setCount] = useState("3")
  const n = Number.parseInt(count, 10)
  const valid = Number.isFinite(n) && n >= 1 && n <= 20
  if (!canWrite) return null
  return (
    <section className="space-y-2">
      <h3 className="text-sm font-semibold">Generate keys</h3>
      <p className="text-xs text-muted-foreground">
        Creates new key pairs and registers them at Surfshark. They appear in the Keys list below and lanes start using
        them. Fresh keys carry no old sessions, so they're the cleanest way to recover from throttling.
      </p>
      <div className="flex items-center gap-2">
        <Input
          type="number"
          min={1}
          max={20}
          value={count}
          onChange={(e) => setCount(e.target.value)}
          className="tabular w-24"
          aria-label="Number of keys"
        />
        <Button disabled={!valid || generate.isPending} onClick={() => generate.mutate(n)}>
          {generate.isPending ? <Spinner /> : <PlusIcon />}
          Generate {valid ? n : ""} key{n === 1 ? "" : "s"}
        </Button>
      </div>
    </section>
  )
}

function AutoManageForm({ account }: { account: SurfsharkAccount }) {
  const canWrite = useCanWrite()
  const update = useUpdateSurfsharkAccount()
  const [lanesPerKey, setLanesPerKey] = useState(String(account.lanesPerKey))
  const [rotateFailures, setRotateFailures] = useState(String(account.rotateFailures))
  const server = fieldErrors(update.error)
  const dirty = lanesPerKey !== String(account.lanesPerKey) || rotateFailures !== String(account.rotateFailures)

  return (
    <section className="space-y-4">
      <label htmlFor="ss-auto" className="flex items-start justify-between gap-4 rounded-md border p-3">
        <span className="space-y-1">
          <span className="block text-sm font-medium">Manage keys automatically</span>
          <span className="block text-xs text-muted-foreground">
            Every 5 minutes: renew keys that expire within 3 days, generate keys when there are more lanes than the
            limit per key allows, and rotate a key when its lanes keep failing (the old key is deleted at Surfshark,
            ending its sessions).
          </span>
        </span>
        <Switch
          id="ss-auto"
          checked={
            update.isPending && update.variables?.autoManage !== undefined
              ? update.variables.autoManage
              : account.autoManage
          }
          disabled={!canWrite || update.isPending}
          onCheckedChange={(autoManage) => update.mutate({ autoManage })}
        />
      </label>
      <div className="grid gap-4 sm:grid-cols-2">
        <FormField
          label="Lanes per key"
          htmlFor="ss-lanes-per-key"
          error={server.lanesPerKey}
          description="Keys are added so no key carries more lanes than this."
        >
          <Input
            id="ss-lanes-per-key"
            type="number"
            min={1}
            value={lanesPerKey}
            disabled={!canWrite}
            onChange={(e) => setLanesPerKey(e.target.value)}
            className="tabular max-w-32"
          />
        </FormField>
        <FormField
          label="Rotate a key after failures on"
          htmlFor="ss-rotate"
          error={server.rotateFailures}
          description="Number of its lanes failing (with none up) before the key is replaced."
        >
          <Input
            id="ss-rotate"
            type="number"
            min={1}
            value={rotateFailures}
            disabled={!canWrite}
            onChange={(e) => setRotateFailures(e.target.value)}
            className="tabular max-w-32"
          />
        </FormField>
      </div>
      {canWrite && dirty ? (
        <div className="flex justify-end">
          <Button
            disabled={update.isPending}
            onClick={() =>
              update.mutate({
                lanesPerKey: Number.parseInt(lanesPerKey, 10),
                rotateFailures: Number.parseInt(rotateFailures, 10),
              })
            }
          >
            {update.isPending ? <Spinner /> : null}
            Save
          </Button>
        </div>
      ) : null}
    </section>
  )
}

function RemoteKeys({ keys }: { keys: SurfsharkRemoteKey[] }) {
  const canWrite = useCanWrite()
  const remove = useDeleteRemoteKey()
  const columns = useMemo<ColumnDef<SurfsharkRemoteKey>[]>(() => {
    const cols: ColumnDef<SurfsharkRemoteKey>[] = [
      {
        id: "name",
        accessorKey: "name",
        header: "Name",
        cell: ({ row }) => <span className="font-medium">{row.original.name || "–"}</span>,
      },
      {
        id: "pubKey",
        accessorKey: "pubKey",
        header: "Public key",
        enableSorting: false,
        cell: ({ row }) => (
          <span className="inline-flex items-center gap-1">
            <code className="font-mono text-xs" title={row.original.pubKey}>
              {truncateMiddle(row.original.pubKey, 6)}
            </code>
            <CopyButton value={row.original.pubKey} label="Copy public key" />
          </span>
        ),
      },
      {
        id: "inLanepool",
        header: "In lanepool",
        cell: ({ row }) =>
          row.original.localKeyId ? <Badge variant="secondary">Yes</Badge> : <Badge variant="outline">No</Badge>,
      },
      {
        id: "expiresAt",
        accessorKey: "expiresAt",
        header: "Expires",
        cell: ({ row }) => <RelativeTime value={row.original.expiresAt} fallback="–" />,
      },
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
              <Button variant="ghost" size="icon-sm" aria-label={`Delete ${row.original.name} at Surfshark`}>
                <Trash2Icon />
              </Button>
            }
            title={`Delete “${row.original.name || row.original.id}” at Surfshark?`}
            description={
              <p>
                The key is removed from your Surfshark account and every session using it ends immediately.
                {row.original.localKeyId
                  ? " lanepool uses this key, so it's removed from lanepool too and its lanes move to other keys."
                  : " Any device still using it (a phone, router…) loses its VPN connection."}
              </p>
            }
            confirmLabel="Delete at Surfshark"
            destructive
            onConfirm={() => remove.mutateAsync(row.original.id)}
          />
        ),
      })
    }
    return cols
  }, [canWrite, remove])

  return (
    <section className="space-y-2">
      <div>
        <h3 className="text-sm font-semibold">Keys at Surfshark</h3>
        <p className="text-xs text-muted-foreground">
          Every WireGuard key registered in the account, including ones lanepool doesn't use (phones, routers, old
          setups). Deleting one ends its sessions.
        </p>
      </div>
      <div className="overflow-hidden rounded-md border">
        <DataTable
          columns={columns}
          data={keys}
          getRowId={(k) => k.id}
          empty={<EmptyState icon={<KeyRoundIcon />} title="No keys registered" description="Generate keys above." />}
        />
      </div>
    </section>
  )
}
