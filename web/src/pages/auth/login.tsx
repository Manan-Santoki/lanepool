import { useState, type FormEvent } from "react"
import { getRouteApi, useRouter } from "@tanstack/react-router"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import { FormError, FormField } from "@/components/form-field"
import { AuthLayout } from "@/pages/auth/auth-layout"
import { useLogin } from "@/hooks/use-auth"
import { errorMessage, fieldErrors, isApiError } from "@/lib/api"

const route = getRouteApi("/login")

export function LoginPage() {
  const { redirect } = route.useSearch()
  const router = useRouter()
  const login = useLogin()
  const [email, setEmail] = useState("")
  const [password, setPassword] = useState("")
  const fields = fieldErrors(login.error)

  const submit = (e: FormEvent) => {
    e.preventDefault()
    login.mutate(
      { email: email.trim(), password },
      { onSuccess: () => router.history.replace(redirect ?? "/") },
    )
  }

  const general =
    login.error && Object.keys(fields).length === 0
      ? isApiError(login.error) && login.error.status === 401
        ? "Incorrect email or password."
        : errorMessage(login.error)
      : null

  return (
    <AuthLayout title="Sign in" description="Sign in to the lanepool admin dashboard.">
      <form onSubmit={submit} className="space-y-4">
        <FormError message={general} />
        <FormField label="Email" htmlFor="login-email" error={fields.email}>
          <Input
            id="login-email"
            type="email"
            autoComplete="username"
            autoFocus
            required
            value={email}
            onChange={(e) => setEmail(e.target.value)}
          />
        </FormField>
        <FormField label="Password" htmlFor="login-password" error={fields.password}>
          <Input
            id="login-password"
            type="password"
            autoComplete="current-password"
            required
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </FormField>
        <Button type="submit" className="w-full" disabled={login.isPending}>
          {login.isPending ? <Spinner /> : null}
          Sign in
        </Button>
      </form>
    </AuthLayout>
  )
}
