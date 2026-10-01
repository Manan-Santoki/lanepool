import { CheckIcon, CopyIcon } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { useCopy } from "@/hooks/use-copy"
import { cn } from "@/lib/utils"

export function CopyButton({
  value,
  label = "Copy",
  className,
  size = "icon-xs",
}: {
  value: string
  label?: string
  className?: string
  size?: "icon-xs" | "icon-sm" | "icon"
}) {
  const { copied, copy } = useCopy()
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          type="button"
          variant="ghost"
          size={size}
          className={cn("text-muted-foreground hover:text-foreground", className)}
          onClick={(e) => {
            e.stopPropagation()
            void copy(value)
          }}
          aria-label={copied ? "Copied" : label}
        >
          {copied ? <CheckIcon className="text-emerald-600 dark:text-emerald-400" /> : <CopyIcon />}
        </Button>
      </TooltipTrigger>
      <TooltipContent>{copied ? "Copied" : label}</TooltipContent>
    </Tooltip>
  )
}

/** Monospace value with a copy button, for URLs, tokens, passwords. */
export function CopyField({ value, label, className }: { value: string; label?: string; className?: string }) {
  return (
    <div className={cn("flex min-w-0 items-center gap-1 rounded-md border bg-muted/40 py-1 pr-1 pl-3", className)}>
      <code className="min-w-0 flex-1 truncate font-mono text-xs" title={value}>
        {value}
      </code>
      <CopyButton value={value} label={label ? `Copy ${label}` : "Copy"} size="icon-sm" />
    </div>
  )
}
