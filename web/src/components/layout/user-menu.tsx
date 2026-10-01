import { useState } from "react"
import { useNavigate } from "@tanstack/react-router"
import { KeyRoundIcon, LogOutIcon, UserIcon } from "lucide-react"
import { Avatar, AvatarFallback } from "@/components/ui/avatar"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { ChangePasswordForm } from "@/components/change-password-form"
import { useLogout, useMe } from "@/hooks/use-auth"

function initials(name: string, email: string) {
  const source = name.trim() || email
  const parts = source.split(/[\s@._-]+/).filter(Boolean)
  return ((parts[0]?.[0] ?? "") + (parts[1]?.[0] ?? "")).toUpperCase() || "?"
}

export function UserMenu() {
  const me = useMe()
  const logout = useLogout()
  const navigate = useNavigate()
  const [pwOpen, setPwOpen] = useState(false)
  if (!me) return null

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant="ghost" size="sm" className="gap-2 px-1.5" aria-label="Account menu">
            <Avatar className="size-7">
              <AvatarFallback className="text-xs">{initials(me.name, me.email)}</AvatarFallback>
            </Avatar>
            <span className="hidden max-w-40 truncate text-sm md:inline">{me.name || me.email}</span>
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="w-60">
          <DropdownMenuLabel className="font-normal">
            <div className="flex items-start justify-between gap-2">
              <div className="min-w-0">
                <p className="truncate text-sm font-medium">{me.name || "Admin"}</p>
                <p className="truncate text-xs text-muted-foreground">{me.email}</p>
              </div>
              <Badge variant={me.role === "admin" ? "default" : "secondary"} className="capitalize">
                {me.role}
              </Badge>
            </div>
          </DropdownMenuLabel>
          <DropdownMenuSeparator />
          <DropdownMenuItem onSelect={() => void navigate({ to: "/settings", search: { tab: "account" } })}>
            <UserIcon /> Account
          </DropdownMenuItem>
          <DropdownMenuItem onSelect={() => setPwOpen(true)}>
            <KeyRoundIcon /> Change password
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem
            variant="destructive"
            onSelect={() =>
              logout.mutate(undefined, {
                onSettled: () => void navigate({ to: "/login", search: {} }),
              })
            }
          >
            <LogOutIcon /> Log out
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      <Dialog open={pwOpen} onOpenChange={setPwOpen}>
        <DialogContent className="grid-cols-[minmax(0,1fr)] sm:max-w-md">
          <DialogHeader>
            <DialogTitle>Change password</DialogTitle>
            <DialogDescription>Other sessions stay signed in until they expire.</DialogDescription>
          </DialogHeader>
          <ChangePasswordForm onDone={() => setPwOpen(false)} />
        </DialogContent>
      </Dialog>
    </>
  )
}
