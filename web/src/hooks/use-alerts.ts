import { useQuery } from "@tanstack/react-query"
import { api } from "@/lib/api"
import type { AlertChannel, AlertChannelInput, AlertRule, AlertRuleInput } from "@/lib/types"
import { qk } from "@/hooks/query-keys"
import { useApiMutation } from "@/hooks/use-api-mutation"

export function useChannels() {
  return useQuery({ queryKey: qk.channels, queryFn: api.alerts.channels })
}

export function useRules() {
  return useQuery({ queryKey: qk.rules, queryFn: api.alerts.rules })
}

export function useSaveChannel() {
  return useApiMutation({
    mutationFn: ({ id, input }: { id?: number; input: Partial<AlertChannelInput> }) =>
      id ? api.alerts.updateChannel(id, input) : api.alerts.createChannel(input as AlertChannelInput),
    invalidate: [qk.channels],
    toastError: false,
    success: (c) => `Saved channel "${c.name}"`,
  })
}

export function useToggleChannel() {
  return useApiMutation({
    mutationFn: ({ channel, enabled }: { channel: AlertChannel; enabled: boolean }) =>
      api.alerts.updateChannel(channel.id, { enabled }),
    invalidate: [qk.channels],
  })
}

export function useDeleteChannel() {
  return useApiMutation({
    mutationFn: (c: AlertChannel) => api.alerts.removeChannel(c.id),
    invalidate: [qk.channels, qk.rules],
    success: "Channel deleted",
  })
}

export function useTestChannel() {
  return useApiMutation({
    mutationFn: (c: AlertChannel) => api.alerts.testChannel(c.id),
    success: (_, c) => `Test message sent to "${c.name}"`,
  })
}

export function useSaveRule() {
  return useApiMutation({
    mutationFn: ({ id, input }: { id?: number; input: AlertRuleInput }) =>
      id ? api.alerts.updateRule(id, input) : api.alerts.createRule(input),
    invalidate: [qk.rules],
    toastError: false,
    success: (r) => `Saved rule "${r.name}"`,
  })
}

export function useToggleRule() {
  return useApiMutation({
    mutationFn: ({ rule, enabled }: { rule: AlertRule; enabled: boolean }) => api.alerts.updateRule(rule.id, { enabled }),
    invalidate: [qk.rules],
  })
}

export function useDeleteRule() {
  return useApiMutation({
    mutationFn: (r: AlertRule) => api.alerts.removeRule(r.id),
    invalidate: [qk.rules],
    success: "Rule deleted",
  })
}
