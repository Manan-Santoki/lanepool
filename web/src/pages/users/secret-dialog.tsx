import { AlertTriangleIcon } from "lucide-react"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { CopyField } from "@/components/copy-button"
import { ConnectionDetails } from "@/pages/users/connection-details"

/** Shows a freshly created or reset password exactly once, with ready-to-use proxy URLs. */
export function PasswordRevealDialog({
  data,
  onClose,
}: {
  data: { username: string; password: string; title: string } | null
  onClose: () => void
}) {
  return (
    <Dialog open={data !== null} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-h-[90svh] grid-cols-[minmax(0,1fr)] overflow-y-auto sm:max-w-2xl">
        {data ? (
          <>
            <DialogHeader>
              <DialogTitle>{data.title}</DialogTitle>
              <DialogDescription className="flex items-start gap-2">
                <AlertTriangleIcon className="mt-0.5 size-4 shrink-0 text-amber-600" />
                Copy the password now. It is not stored in a readable form and will not be shown again.
              </DialogDescription>
            </DialogHeader>
            <div className="grid gap-3 sm:grid-cols-2">
              <div className="space-y-1">
                <p className="text-xs text-muted-foreground">Username</p>
                <CopyField value={data.username} label="username" />
              </div>
              <div className="space-y-1">
                <p className="text-xs text-muted-foreground">Password</p>
                <CopyField value={data.password} label="password" />
              </div>
            </div>
            <ConnectionDetails username={data.username} password={data.password} />
            <DialogFooter>
              <Button onClick={onClose}>Done</Button>
            </DialogFooter>
          </>
        ) : null}
      </DialogContent>
    </Dialog>
  )
}
