import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { RANGE_PRESETS, type RangePreset, type TimeRangeValue } from "@/lib/time-range"

export function TimeRangeFilter({ value, onChange }: { value: TimeRangeValue; onChange: (v: TimeRangeValue) => void }) {
  return (
    <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
      <Select
        value={value.preset}
        onValueChange={(p) => onChange({ ...value, preset: p as RangePreset, anchor: Date.now() })}
      >
        <SelectTrigger className="w-full sm:w-44" aria-label="Time range">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {RANGE_PRESETS.map((p) => (
            <SelectItem key={p.value} value={p.value}>
              {p.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      {value.preset === "custom" ? (
        <div className="flex items-center gap-2">
          <Input
            type="datetime-local"
            aria-label="From"
            value={value.customFrom}
            max={value.customTo || undefined}
            onChange={(e) => onChange({ ...value, customFrom: e.target.value })}
            className="w-full sm:w-52"
          />
          <span className="text-xs text-muted-foreground">to</span>
          <Input
            type="datetime-local"
            aria-label="To"
            value={value.customTo}
            min={value.customFrom || undefined}
            onChange={(e) => onChange({ ...value, customTo: e.target.value })}
            className="w-full sm:w-52"
          />
        </div>
      ) : null}
    </div>
  )
}
