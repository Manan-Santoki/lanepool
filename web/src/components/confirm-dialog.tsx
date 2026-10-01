import { useState, type ReactNode } from "react"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog"
import { Spinner } from "@/components/ui/spinner"

interface ConfirmProps {
  title: string
  description: ReactNode
  confirmLabel?: string
  destructive?: boolean
  /** May return a promise; the dialog stays open (with a spinner) until it settles, closing on success. */
  onConfirm: () => unknown
}

/** Uncontrolled confirm: wrap any trigger element. */
export function ConfirmDialog({ trigger, ...props }: ConfirmProps & { trigger: ReactNode }) {
  const [open, setOpen] = useState(false)
  return (
    <AlertDialog open={open} onOpenChange={setOpen}>
      <AlertDialogTrigger asChild>{trigger}</AlertDialogTrigger>
      <ConfirmContent {...props} onDone={() => setOpen(false)} />
    </AlertDialog>
  )
}

/** Controlled confirm, for actions launched from dropdown menus. */
export function ControlledConfirm({
  open,
  onOpenChange,
  ...props
}: ConfirmProps & { open: boolean; onOpenChange: (open: boolean) => void }) {
  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <ConfirmContent {...props} onDone={() => onOpenChange(false)} />
    </AlertDialog>
  )
}

function ConfirmContent({
  title,
  description,
  confirmLabel = "Confirm",
  destructive,
  onConfirm,
  onDone,
}: ConfirmProps & { onDone: () => void }) {
  const [pending, setPending] = useState(false)
  return (
    <AlertDialogContent>
      <AlertDialogHeader>
        <AlertDialogTitle>{title}</AlertDialogTitle>
        <AlertDialogDescription asChild>
          <div className="space-y-2 text-sm text-muted-foreground">{description}</div>
        </AlertDialogDescription>
      </AlertDialogHeader>
      <AlertDialogFooter>
        <AlertDialogCancel disabled={pending}>Cancel</AlertDialogCancel>
        <AlertDialogAction
          disabled={pending}
          variant={destructive ? "destructive" : "default"}
          onClick={async (e) => {
            e.preventDefault()
            setPending(true)
            try {
              await onConfirm()
              onDone()
            } catch {
              // Mutations toast their own errors; keep the dialog open.
            } finally {
              setPending(false)
            }
          }}
        >
          {pending ? <Spinner /> : null}
          {confirmLabel}
        </AlertDialogAction>
      </AlertDialogFooter>
    </AlertDialogContent>
  )
}
