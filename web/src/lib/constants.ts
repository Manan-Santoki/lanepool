import type { AlertEvent, ChannelKind, ConnResult, LaneStatus, Strategy, TokenScope } from "@/lib/types"

export interface Tone {
  /** Badge classes (background + text), readable in light and dark. */
  badge: string
  /** Solid fill for dots and mini-grid squares. */
  dot: string
}

const TONES = {
  green: {
    badge: "bg-emerald-500/12 text-emerald-700 dark:text-emerald-400 border-emerald-500/25",
    dot: "bg-emerald-500",
  },
  blue: {
    badge: "bg-sky-500/12 text-sky-700 dark:text-sky-400 border-sky-500/25",
    dot: "bg-sky-500",
  },
  amber: {
    badge: "bg-amber-500/15 text-amber-700 dark:text-amber-400 border-amber-500/30",
    dot: "bg-amber-500",
  },
  red: {
    badge: "bg-red-500/12 text-red-700 dark:text-red-400 border-red-500/25",
    dot: "bg-red-500",
  },
  violet: {
    badge: "bg-violet-500/12 text-violet-700 dark:text-violet-400 border-violet-500/25",
    dot: "bg-violet-500",
  },
  gray: {
    badge: "bg-muted text-muted-foreground border-border",
    dot: "bg-muted-foreground/40",
  },
  slate: {
    badge: "bg-slate-500/10 text-slate-700 dark:text-slate-300 border-slate-500/25",
    dot: "bg-slate-400",
  },
} satisfies Record<string, Tone>

export type ToneName = keyof typeof TONES
export const tone = (name: ToneName): Tone => TONES[name]

export const LANE_STATUSES: LaneStatus[] = ["up", "connecting", "queued", "backoff", "down", "disabled", "standby"]

export const LANE_STATUS_META: Record<LaneStatus, { label: string; tone: ToneName; description: string }> = {
  up: { label: "Up", tone: "green", description: "Tunnel is healthy and serving traffic" },
  connecting: { label: "Connecting", tone: "blue", description: "Handshake in progress" },
  queued: { label: "Queued", tone: "slate", description: "Waiting for a pacing slot to connect" },
  backoff: { label: "Backoff", tone: "amber", description: "Failed recently; waiting before the next retry" },
  down: { label: "Down", tone: "red", description: "Tunnel is not working" },
  disabled: { label: "Disabled", tone: "gray", description: "Turned off by an admin" },
  standby: { label: "Standby", tone: "gray", description: "Server pool: not needed while enough lanes are up" },
}

export const CONN_RESULTS: ConnResult[] = [
  "ok",
  "auth_failed",
  "denied",
  "quota",
  "rate_limited",
  "no_lane",
  "dial_failed",
  "kicked",
  "paused",
  "bad_request",
]

export const CONN_RESULT_META: Record<ConnResult, { label: string; tone: ToneName }> = {
  ok: { label: "OK", tone: "green" },
  auth_failed: { label: "Auth failed", tone: "amber" },
  denied: { label: "Denied", tone: "amber" },
  quota: { label: "Quota", tone: "violet" },
  rate_limited: { label: "Rate limited", tone: "violet" },
  no_lane: { label: "No lane", tone: "red" },
  dial_failed: { label: "Dial failed", tone: "red" },
  kicked: { label: "Kicked", tone: "blue" },
  paused: { label: "Paused", tone: "slate" },
  bad_request: { label: "Bad request", tone: "gray" },
}

export const EVENT_LEVEL_TONE: Record<"info" | "warn" | "error", ToneName> = {
  info: "blue",
  warn: "amber",
  error: "red",
}

export const STRATEGIES: { value: Strategy; label: string; description: string }[] = [
  {
    value: "round_robin",
    label: "Round robin",
    description: "Cycle through healthy lanes in order. Even spread, predictable.",
  },
  { value: "random", label: "Random", description: "Pick a random healthy lane for each new connection." },
  {
    value: "least_connections",
    label: "Least connections",
    description: "Prefer the lane with the fewest open connections. Best for long-lived traffic.",
  },
  {
    value: "lowest_latency",
    label: "Lowest latency",
    description: "Prefer the lane with the lowest measured latency. Can concentrate load on few lanes.",
  },
]

export const ALERT_EVENTS: { value: AlertEvent; label: string; description: string; threshold?: string }[] = [
  {
    value: "lanes_below",
    label: "Healthy lanes below threshold",
    description: "Fires when the number of lanes that are up drops below the threshold.",
    threshold: "Minimum lanes up",
  },
  { value: "lane_down", label: "Lane down", description: "Fires when any lane goes down." },
  {
    value: "breaker_open",
    label: "Circuit breaker opened",
    description: "New lane connections were paused after repeated failures.",
  },
  {
    value: "key_failing",
    label: "Provider key failing",
    description: "A Surfshark key is failing to bring lanes up (revoked or expired).",
  },
  { value: "quota_reached", label: "User quota reached", description: "A proxy user used up their traffic quota." },
  {
    value: "engine_offline",
    label: "Engine offline",
    description: "The engine stopped reporting to the control plane.",
  },
  {
    value: "auth_failures",
    label: "Proxy auth failures",
    description: "Many failed proxy logins in a short time (possible brute force).",
    threshold: "Failures per minute",
  },
]

export const CHANNEL_KINDS: { value: ChannelKind; label: string }[] = [
  { value: "telegram", label: "Telegram" },
  { value: "discord", label: "Discord" },
  { value: "slack", label: "Slack" },
  { value: "webhook", label: "Webhook" },
]

export const TOKEN_SCOPES: { value: TokenScope; label: string; description: string }[] = [
  { value: "read", label: "read", description: "Read-only access to every GET endpoint and /metrics." },
  { value: "manage", label: "manage", description: "Everything an admin can do." },
  {
    value: "rotate",
    label: "rotate",
    description: "Rotate sessions, burn IPs and pick random lanes (for scrapers and apps).",
  },
]
