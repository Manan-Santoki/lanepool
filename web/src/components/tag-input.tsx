import { useId, useState, type ClipboardEvent, type KeyboardEvent } from "react"
import { XIcon } from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { cn } from "@/lib/utils"

interface TagInputProps {
  id?: string
  value: string[]
  onChange: (value: string[]) => void
  placeholder?: string
  disabled?: boolean
  /** Normalize a raw entry (e.g. lowercase). */
  normalize?: (raw: string) => string
  /** Return an error message for invalid entries. */
  validate?: (value: string) => string | null
  "aria-invalid"?: boolean
  className?: string
}

/** Free-form tag input: Enter, comma, space or paste to add; Backspace removes the last tag. */
export function TagInput({
  id,
  value,
  onChange,
  placeholder,
  disabled,
  normalize = (s) => s.trim(),
  validate,
  className,
  ...rest
}: TagInputProps) {
  const [draft, setDraft] = useState("")
  const [error, setError] = useState<string | null>(null)
  const errorId = useId()

  const add = (raw: string[]) => {
    const next = [...value]
    for (const r of raw) {
      const v = normalize(r)
      if (!v || next.includes(v)) continue
      const err = validate?.(v)
      if (err) {
        setError(err)
        return false
      }
      next.push(v)
    }
    setError(null)
    if (next.length !== value.length) onChange(next)
    return true
  }

  const commit = () => {
    if (!draft.trim()) return
    if (add([draft])) setDraft("")
  }

  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "Enter" || e.key === "," || e.key === " " || e.key === "Tab") {
      if (draft.trim()) {
        e.preventDefault()
        commit()
      }
    } else if (e.key === "Backspace" && !draft && value.length) {
      onChange(value.slice(0, -1))
    }
  }

  const onPaste = (e: ClipboardEvent<HTMLInputElement>) => {
    const text = e.clipboardData.getData("text")
    const parts = text.split(/[\s,;]+/).filter(Boolean)
    if (parts.length > 1) {
      e.preventDefault()
      add(parts)
    }
  }

  return (
    <div className="space-y-1.5">
      <div
        className={cn(
          "flex min-h-9 w-full flex-wrap items-center gap-1 rounded-md border border-input bg-transparent px-2 py-1 shadow-xs transition-[color,box-shadow] focus-within:border-ring focus-within:ring-3 focus-within:ring-ring/50 dark:bg-input/30",
          (error || rest["aria-invalid"]) && "border-destructive",
          disabled && "pointer-events-none opacity-50",
          className,
        )}
      >
        {value.map((tag) => (
          <Badge key={tag} variant="secondary" className="h-6 gap-1 pr-1 font-mono text-xs font-normal">
            {tag}
            <button
              type="button"
              className="rounded-sm p-0.5 opacity-60 hover:bg-foreground/10 hover:opacity-100"
              onClick={() => onChange(value.filter((t) => t !== tag))}
              aria-label={`Remove ${tag}`}
            >
              <XIcon className="size-3" />
            </button>
          </Badge>
        ))}
        <input
          id={id}
          value={draft}
          disabled={disabled}
          onChange={(e) => {
            setDraft(e.target.value)
            if (error) setError(null)
          }}
          onKeyDown={onKeyDown}
          onPaste={onPaste}
          onBlur={commit}
          placeholder={value.length ? "" : placeholder}
          aria-describedby={error ? errorId : undefined}
          aria-invalid={error ? true : rest["aria-invalid"]}
          className="h-7 min-w-32 flex-1 bg-transparent text-sm outline-none placeholder:text-muted-foreground"
        />
      </div>
      {error ? (
        <p id={errorId} className="text-xs text-destructive">
          {error}
        </p>
      ) : null}
    </div>
  )
}
