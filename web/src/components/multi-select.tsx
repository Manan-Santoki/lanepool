import { useMemo, useState, type ReactNode } from "react"
import { ChevronsUpDownIcon, PlusIcon, XIcon } from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { cn } from "@/lib/utils"

export interface MultiSelectOption {
  value: string
  label: string
  icon?: ReactNode
  hint?: string
  keywords?: string[]
}

interface MultiSelectProps {
  id?: string
  options: MultiSelectOption[]
  value: string[]
  onChange: (value: string[]) => void
  placeholder?: string
  searchPlaceholder?: string
  emptyText?: string
  disabled?: boolean
  /** Allow adding values not in the list. Return the normalized value, or null if invalid. */
  create?: (input: string) => string | null
  /** Max chips to show before "+N more". */
  maxChips?: number
  className?: string
}

/** Searchable multi-select (Popover + Command) with removable chips. */
export function MultiSelect({
  id,
  options,
  value,
  onChange,
  placeholder = "Select…",
  searchPlaceholder = "Search…",
  emptyText = "No matches.",
  disabled,
  create,
  maxChips = 8,
  className,
}: MultiSelectProps) {
  const [open, setOpen] = useState(false)
  const [search, setSearch] = useState("")
  const byValue = useMemo(() => new Map(options.map((o) => [o.value, o])), [options])
  const selected = new Set(value)

  const toggle = (v: string) => {
    onChange(selected.has(v) ? value.filter((x) => x !== v) : [...value, v])
  }

  const created = create && search.trim() ? create(search.trim()) : null
  const canCreate = created !== null && !byValue.has(created) && !selected.has(created)
  // Selected values that aren't in the options (e.g. lanes that no longer exist) stay visible.
  const orphans = value.filter((v) => !byValue.has(v))

  return (
    <div className={cn("space-y-2", className)}>
      <Popover
        open={open}
        onOpenChange={(o) => {
          setOpen(o)
          if (!o) setSearch("")
        }}
      >
        <PopoverTrigger asChild>
          <Button
            id={id}
            type="button"
            variant="outline"
            role="combobox"
            aria-expanded={open}
            disabled={disabled}
            className="w-full justify-between font-normal"
          >
            <span className={cn("truncate", value.length === 0 && "text-muted-foreground")}>
              {value.length === 0 ? placeholder : `${value.length} selected`}
            </span>
            <ChevronsUpDownIcon className="opacity-50" />
          </Button>
        </PopoverTrigger>
        <PopoverContent className="w-(--radix-popover-trigger-width) min-w-64 p-0" align="start">
          <Command>
            <CommandInput placeholder={searchPlaceholder} value={search} onValueChange={setSearch} />
            <CommandList>
              <CommandEmpty>{emptyText}</CommandEmpty>
              {canCreate && created ? (
                <CommandGroup>
                  <CommandItem
                    value={`__create__${created}`}
                    onSelect={() => {
                      onChange([...value, created])
                      setSearch("")
                    }}
                  >
                    <PlusIcon /> Add “{created}”
                  </CommandItem>
                </CommandGroup>
              ) : null}
              {orphans.length > 0 ? (
                <CommandGroup heading="Selected">
                  {orphans.map((v) => (
                    <CommandItem key={v} value={v} data-checked onSelect={() => toggle(v)}>
                      {v}
                    </CommandItem>
                  ))}
                </CommandGroup>
              ) : null}
              <CommandGroup>
                {options.map((o) => (
                  <CommandItem
                    key={o.value}
                    value={o.value}
                    keywords={[o.label, ...(o.keywords ?? [])]}
                    data-checked={selected.has(o.value)}
                    onSelect={() => toggle(o.value)}
                  >
                    {o.icon}
                    <span className="truncate">{o.label}</span>
                    {o.hint ? <span className="ml-auto pr-5 text-xs text-muted-foreground">{o.hint}</span> : null}
                  </CommandItem>
                ))}
              </CommandGroup>
            </CommandList>
          </Command>
        </PopoverContent>
      </Popover>
      {value.length > 0 ? (
        <div className="flex flex-wrap gap-1">
          {value.slice(0, maxChips).map((v) => {
            const o = byValue.get(v)
            return (
              <Badge key={v} variant="secondary" className="h-6 gap-1 pr-1 font-normal">
                {o?.icon}
                <span className="max-w-48 truncate">{o?.label ?? v}</span>
                {!disabled ? (
                  <button
                    type="button"
                    className="rounded-sm p-0.5 opacity-60 hover:bg-foreground/10 hover:opacity-100"
                    onClick={() => toggle(v)}
                    aria-label={`Remove ${o?.label ?? v}`}
                  >
                    <XIcon className="size-3" />
                  </button>
                ) : null}
              </Badge>
            )
          })}
          {value.length > maxChips ? (
            <Badge variant="outline" className="h-6 font-normal">
              +{value.length - maxChips} more
            </Badge>
          ) : null}
          {!disabled && value.length > 1 ? (
            <Button type="button" variant="ghost" size="xs" onClick={() => onChange([])}>
              Clear
            </Button>
          ) : null}
        </div>
      ) : null}
    </div>
  )
}
