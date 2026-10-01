import type { ReactNode } from "react"
import { Field, FieldDescription, FieldError, FieldLabel } from "@/components/ui/field"
import { cn } from "@/lib/utils"

/** Label + control + help text + inline server error (from ApiError.fields). */
export function FormField({
  label,
  htmlFor,
  description,
  error,
  children,
  className,
  unit,
}: {
  label: ReactNode
  htmlFor?: string
  description?: ReactNode
  error?: string
  children: ReactNode
  className?: string
  unit?: string
}) {
  return (
    <Field data-invalid={error ? true : undefined} className={cn("gap-2", className)}>
      <FieldLabel htmlFor={htmlFor}>
        {label}
        {unit ? <span className="font-normal text-muted-foreground">({unit})</span> : null}
      </FieldLabel>
      {children}
      {description ? <FieldDescription className="text-xs">{description}</FieldDescription> : null}
      {error ? <FieldError>{error}</FieldError> : null}
    </Field>
  )
}

/** Card-like section inside long forms. */
export function FormSection({
  title,
  description,
  children,
  className,
}: {
  title: string
  description?: ReactNode
  children: ReactNode
  className?: string
}) {
  return (
    <section className={cn("space-y-4", className)}>
      <div className="space-y-1">
        <h3 className="text-sm font-semibold">{title}</h3>
        {description ? <p className="text-xs text-muted-foreground">{description}</p> : null}
      </div>
      {children}
    </section>
  )
}

/** Form-level error banner (for errors without field mapping). */
export function FormError({ message }: { message?: string | null }) {
  if (!message) return null
  return (
    <div role="alert" className="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">
      {message}
    </div>
  )
}
