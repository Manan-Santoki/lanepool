import { useMemo, useState, type FormEvent } from "react"
import type { ColumnDef } from "@tanstack/react-table"
import { KeyRoundIcon, MoreHorizontalIcon, PlusIcon, ShieldIcon, Trash2Icon, UserCheckIcon, UserXIcon } from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Spinner } from "@/components/ui/spinner"
import { ToneBadge } from "@/components/badges"
import { ControlledConfirm } from "@/components/confirm-dialog"
import { DataTable } from "@/components/data-table"
import { FormError, FormField } from "@/components/form-field"
import { EmptyState, ErrorState } from "@/components/states"
import { RelativeTime } from "@/components/time"
import { WriteOnly } from "@/components/write-only"
import { useCanWrite, useMe } from "@/hooks/use-auth"
import { useAdmins, useCreateAdmin, useDeleteAdmin, useUpdateAdmin } from "@/hooks/use-settings"
import { errorMessage, fieldErrors } from "@/lib/api"
import type { Admin, Role } from "@/lib/types"

const ROLE_HELP: Record<Role, string> = {
  admin: "Full access, including settings and other admins.",
  viewer: "Read-only: can see everything but change nothing.",
}

type Pending = { kind: "delete" | "disable" | "enable"; admin: Admin }

export function AdminsSettings() {
  const admins = useAdmins()
  const me = useMe()
  const canWrite = useCanWrite()
  const update = useUpdateAdmin()
  const del = useDeleteAdmin()
  const [addOpen, setAddOpen] = useState(false)
  const [pwFor, setPwFor] = useState<Admin | null>(null)
  const [pending, setPending] = useState<Pending | null>(null)

  const columns = useMemo<ColumnDef<Admin>[]>(() => {
    const cols: ColumnDef<Admin>[] = [
      {
        id: "name",
        accessorFn: (a) => a.name || a.email,
        header: "Admin",
        cell: ({ row }) => (
          <div className="min-w-0">
            <div className="flex items-center gap-2">
              <span className="font-medium">{row.original.name || "–"}</span>
              {row.original.id === me?.id ? (
                <Badge variant="outline" className="font-normal">
                  you
                </Badge>
              ) : null}
            </div>
            <p className="text-xs text-muted-foreground">{row.original.email}</p>
          </div>
        ),
      },
      {
        id: "role",
        accessorKey: "role",
        header: "Role",
        cell: ({ row }) => (
          <Badge variant={row.original.role === "admin" ? "default" : "secondary"} className="capitalize">
            {row.original.role}
          </Badge>
        ),
      },
      {
        id: "status",
        accessorKey: "disabled",
        header: "Status",
        cell: ({ row }) =>
          row.original.disabled ? <ToneBadge toneName="gray">Disabled</ToneBadge> : <ToneBadge toneName="green">Active</ToneBadge>,
      },
      { id: "lastLogin", accessorFn: (a) => a.lastLoginAt ?? "", header: "Last login", cell: ({ row }) => <RelativeTime value={row.original.lastLoginAt} fallback="Never" /> },
      { id: "created", accessorKey: "createdAt", header: "Created", cell: ({ row }) => <RelativeTime value={row.original.createdAt} /> },
    ]
    if (canWrite) {
      cols.push({
        id: "actions",
        header: () => <span className="sr-only">Actions</span>,
        enableSorting: false,
        meta: { className: "w-10" },
        cell: ({ row }) => {
          const a = row.original
          const self = a.id === me?.id
          return (
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button variant="ghost" size="icon-sm" aria-label={`Actions for ${a.email}`}>
                  <MoreHorizontalIcon />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="w-56">
                <DropdownMenuLabel>Role</DropdownMenuLabel>
                <DropdownMenuRadioGroup
                  value={a.role}
                  onValueChange={(role) => role !== a.role && update.mutate({ admin: a, patch: { role: role as Role } })}
                >
                  <DropdownMenuRadioItem value="admin" disabled={self}>
                    Admin
                  </DropdownMenuRadioItem>
                  <DropdownMenuRadioItem value="viewer" disabled={self}>
                    Viewer
                  </DropdownMenuRadioItem>
                </DropdownMenuRadioGroup>
                <DropdownMenuSeparator />
                <DropdownMenuItem onSelect={() => setPwFor(a)}>
                  <KeyRoundIcon /> Reset password
                </DropdownMenuItem>
                {a.disabled ? (
                  <DropdownMenuItem onSelect={() => setPending({ kind: "enable", admin: a })}>
                    <UserCheckIcon /> Enable
                  </DropdownMenuItem>
                ) : (
                  <DropdownMenuItem disabled={self} onSelect={() => setPending({ kind: "disable", admin: a })}>
                    <UserXIcon /> Disable
                  </DropdownMenuItem>
                )}
                <DropdownMenuSeparator />
                <DropdownMenuItem variant="destructive" disabled={self} onSelect={() => setPending({ kind: "delete", admin: a })}>
                  <Trash2Icon /> Delete
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          )
        },
      })
    }
    return cols
  }, [canWrite, me?.id, update])

  const confirm = pending
    ? pending.kind === "delete"
      ? {
          title: `Delete ${pending.admin.email}?`,
          description: <p>They lose access immediately. Their past actions stay in the audit log.</p>,
          label: "Delete admin",
          run: () => del.mutateAsync(pending.admin),
        }
      : pending.kind === "disable"
        ? {
            title: `Disable ${pending.admin.email}?`,
            description: <p>They are signed out and can no longer log in until re-enabled.</p>,
            label: "Disable",
            run: () => update.mutateAsync({ admin: pending.admin, patch: { disabled: true } }),
          }
        : {
            title: `Enable ${pending.admin.email}?`,
            description: <p>They can log in again with their existing password.</p>,
            label: "Enable",
            run: () => update.mutateAsync({ admin: pending.admin, patch: { disabled: false } }),
          }
    : null

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <ShieldIcon className="size-4 text-muted-foreground" /> Admins
        </CardTitle>
        <CardDescription>People who can sign in to this dashboard. You cannot delete or demote yourself.</CardDescription>
        <CardAction>
          <WriteOnly>
            <Button size="sm" variant="outline" onClick={() => setAddOpen(true)}>
              <PlusIcon /> Add admin
            </Button>
          </WriteOnly>
        </CardAction>
      </CardHeader>
      <CardContent>
        {admins.isError && !admins.data ? (
          <ErrorState error={admins.error} onRetry={() => void admins.refetch()} />
        ) : (
          <div className="overflow-hidden rounded-md border">
            <DataTable
              columns={columns}
              data={admins.data}
              isLoading={admins.isPending}
              skeletonRows={3}
              getRowId={(a) => String(a.id)}
              initialSorting={[{ id: "name", desc: false }]}
              rowClassName={(row) => (row.original.disabled ? "opacity-60" : undefined)}
              empty={<EmptyState title="No admins" />}
            />
          </div>
        )}
      </CardContent>

      <Dialog open={addOpen} onOpenChange={setAddOpen}>
        <DialogContent className="grid-cols-[minmax(0,1fr)] sm:max-w-md">{addOpen ? <AddAdminForm onDone={() => setAddOpen(false)} /> : null}</DialogContent>
      </Dialog>
      <Dialog open={pwFor !== null} onOpenChange={(o) => !o && setPwFor(null)}>
        <DialogContent className="grid-cols-[minmax(0,1fr)] sm:max-w-md">{pwFor ? <ResetPasswordForm admin={pwFor} onDone={() => setPwFor(null)} /> : null}</DialogContent>
      </Dialog>
      {confirm ? (
        <ControlledConfirm
          open
          onOpenChange={(o) => !o && setPending(null)}
          title={confirm.title}
          description={confirm.description}
          confirmLabel={confirm.label}
          destructive={pending?.kind !== "enable"}
          onConfirm={confirm.run}
        />
      ) : null}
    </Card>
  )
}

function AddAdminForm({ onDone }: { onDone: () => void }) {
  const create = useCreateAdmin()
  const [email, setEmail] = useState("")
  const [name, setName] = useState("")
  const [role, setRole] = useState<Role>("viewer")
  const [password, setPassword] = useState("")
  const [local, setLocal] = useState<Record<string, string>>({})
  const server = fieldErrors(create.error)
  const errors = { ...server, ...local }
  const general = create.error && Object.keys(server).length === 0 ? errorMessage(create.error) : null

  const submit = (e: FormEvent) => {
    e.preventDefault()
    const errs: Record<string, string> = {}
    if (!/^\S+@\S+\.\S+$/.test(email.trim())) errs.email = "Enter a valid email address."
    if (password.length < 8) errs.password = "At least 8 characters."
    setLocal(errs)
    if (Object.keys(errs).length) return
    create.mutate({ email: email.trim(), name: name.trim(), role, password }, { onSuccess: onDone })
  }

  return (
    <form onSubmit={submit} className="space-y-4" noValidate>
      <DialogHeader>
        <DialogTitle>Add admin</DialogTitle>
        <DialogDescription>Share the password with them securely; they can change it under Account.</DialogDescription>
      </DialogHeader>
      <FormError message={general} />
      <FormField label="Email" htmlFor="ad-email" error={errors.email}>
        <Input id="ad-email" type="email" value={email} onChange={(e) => setEmail(e.target.value)} autoFocus autoComplete="off" />
      </FormField>
      <FormField label="Name" htmlFor="ad-name" error={errors.name}>
        <Input id="ad-name" value={name} onChange={(e) => setName(e.target.value)} autoComplete="off" />
      </FormField>
      <FormField label="Role" htmlFor="ad-role" error={errors.role} description={ROLE_HELP[role]}>
        <Select value={role} onValueChange={(v) => setRole(v as Role)}>
          <SelectTrigger id="ad-role" className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="viewer">Viewer</SelectItem>
            <SelectItem value="admin">Admin</SelectItem>
          </SelectContent>
        </Select>
      </FormField>
      <FormField label="Password" htmlFor="ad-password" error={errors.password} description="At least 8 characters.">
        <Input id="ad-password" type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" />
      </FormField>
      <DialogFooter>
        <Button type="button" variant="outline" onClick={onDone}>
          Cancel
        </Button>
        <Button type="submit" disabled={create.isPending}>
          {create.isPending ? <Spinner /> : null}
          Add admin
        </Button>
      </DialogFooter>
    </form>
  )
}

function ResetPasswordForm({ admin, onDone }: { admin: Admin; onDone: () => void }) {
  const update = useUpdateAdmin()
  const [password, setPassword] = useState("")
  const [error, setError] = useState<string | null>(null)
  const server = fieldErrors(update.error)

  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (password.length < 8) return setError("At least 8 characters.")
    setError(null)
    update.mutate({ admin, patch: { password } }, { onSuccess: onDone })
  }

  return (
    <form onSubmit={submit} className="space-y-4" noValidate>
      <DialogHeader>
        <DialogTitle>Reset password</DialogTitle>
        <DialogDescription>Set a new password for {admin.email}.</DialogDescription>
      </DialogHeader>
      <FormField label="New password" htmlFor="ad-newpw" error={error ?? server.password}>
        <Input id="ad-newpw" type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" autoFocus />
      </FormField>
      <DialogFooter>
        <Button type="button" variant="outline" onClick={onDone}>
          Cancel
        </Button>
        <Button type="submit" disabled={update.isPending}>
          {update.isPending ? <Spinner /> : null}
          Set password
        </Button>
      </DialogFooter>
    </form>
  )
}
