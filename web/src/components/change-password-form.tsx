import { useState, type FormEvent } from "react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import { FormError, FormField } from "@/components/form-field"
import { useChangePassword } from "@/hooks/use-auth"
import { errorMessage, fieldErrors } from "@/lib/api"

export function ChangePasswordForm({ onDone }: { onDone?: () => void }) {
  const [current, setCurrent] = useState("")
  const [next, setNext] = useState("")
  const [confirm, setConfirm] = useState("")
  const [localError, setLocalError] = useState<string | null>(null)
  const mutation = useChangePassword()
  const fields = fieldErrors(mutation.error)

  const submit = (e: FormEvent) => {
    e.preventDefault()
    setLocalError(null)
    if (next.length < 8) return setLocalError("The new password must be at least 8 characters.")
    if (next !== confirm) return setLocalError("The new passwords do not match.")
    mutation.mutate(
      { currentPassword: current, newPassword: next },
      {
        onSuccess: () => {
          setCurrent("")
          setNext("")
          setConfirm("")
          onDone?.()
        },
      },
    )
  }

  const generalError =
    localError ?? (mutation.error && Object.keys(fields).length === 0 ? errorMessage(mutation.error) : null)

  return (
    <form onSubmit={submit} className="space-y-4">
      <FormError message={generalError} />
      <FormField label="Current password" htmlFor="pw-current" error={fields.currentPassword}>
        <Input
          id="pw-current"
          type="password"
          autoComplete="current-password"
          value={current}
          onChange={(e) => setCurrent(e.target.value)}
          required
        />
      </FormField>
      <FormField label="New password" htmlFor="pw-new" error={fields.newPassword} description="At least 8 characters.">
        <Input
          id="pw-new"
          type="password"
          autoComplete="new-password"
          value={next}
          onChange={(e) => setNext(e.target.value)}
          required
        />
      </FormField>
      <FormField label="Confirm new password" htmlFor="pw-confirm">
        <Input
          id="pw-confirm"
          type="password"
          autoComplete="new-password"
          value={confirm}
          onChange={(e) => setConfirm(e.target.value)}
          required
        />
      </FormField>
      <div className="flex justify-end">
        <Button type="submit" disabled={mutation.isPending}>
          {mutation.isPending ? <Spinner /> : null}
          Change password
        </Button>
      </div>
    </form>
  )
}
