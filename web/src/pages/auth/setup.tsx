import { useState, type FormEvent } from "react"
import { useNavigate } from "@tanstack/react-router"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import { FormError, FormField } from "@/components/form-field"
import { AuthLayout } from "@/pages/auth/auth-layout"
import { useSetup } from "@/hooks/use-auth"
import { errorMessage, fieldErrors } from "@/lib/api"

export function SetupPage() {
  const navigate = useNavigate()
  const setup = useSetup()
  const [name, setName] = useState("")
  const [email, setEmail] = useState("")
  const [password, setPassword] = useState("")
  const [confirm, setConfirm] = useState("")
  const [localError, setLocalError] = useState<string | null>(null)
  const fields = fieldErrors(setup.error)

  const submit = (e: FormEvent) => {
    e.preventDefault()
    setLocalError(null)
    if (password.length < 8) return setLocalError("Use at least 8 characters for the password.")
    if (password !== confirm) return setLocalError("The passwords do not match.")
    setup.mutate(
      { name: name.trim(), email: email.trim(), password },
      { onSuccess: () => void navigate({ to: "/", replace: true }) },
    )
  }

  const general = localError ?? (setup.error && Object.keys(fields).length === 0 ? errorMessage(setup.error) : null)

  return (
    <AuthLayout
      title="Welcome to lanepool"
      description="Create the first administrator account. You can add more admins later in Settings."
    >
      <form onSubmit={submit} className="space-y-4">
        <FormError message={general} />
        <FormField label="Name" htmlFor="setup-name" error={fields.name}>
          <Input id="setup-name" autoComplete="name" autoFocus value={name} onChange={(e) => setName(e.target.value)} />
        </FormField>
        <FormField label="Email" htmlFor="setup-email" error={fields.email}>
          <Input
            id="setup-email"
            type="email"
            autoComplete="username"
            required
            value={email}
            onChange={(e) => setEmail(e.target.value)}
          />
        </FormField>
        <FormField label="Password" htmlFor="setup-password" error={fields.password} description="At least 8 characters.">
          <Input
            id="setup-password"
            type="password"
            autoComplete="new-password"
            required
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </FormField>
        <FormField label="Confirm password" htmlFor="setup-confirm">
          <Input
            id="setup-confirm"
            type="password"
            autoComplete="new-password"
            required
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
          />
        </FormField>
        <Button type="submit" className="w-full" disabled={setup.isPending}>
          {setup.isPending ? <Spinner /> : null}
          Create admin and continue
        </Button>
      </form>
    </AuthLayout>
  )
}
