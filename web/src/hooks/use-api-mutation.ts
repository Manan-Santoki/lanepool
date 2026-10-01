import { useMutation, useQueryClient, type QueryKey } from "@tanstack/react-query"
import { toast } from "sonner"
import { errorMessage } from "@/lib/api"

interface Options<TData, TVars> {
  mutationFn: (vars: TVars) => Promise<TData>
  /** Query keys (prefixes) to invalidate after success. */
  invalidate?: QueryKey[]
  /** Toast shown on success. A function receives the result and variables. */
  success?: string | ((data: TData, vars: TVars) => string | null)
  /** Show a toast for errors (default true). Forms that render field errors inline still get a toast. */
  toastError?: boolean
  onSuccess?: (data: TData, vars: TVars) => void
}

/** useMutation with cache invalidation and toast feedback, the pattern every page uses. */
export function useApiMutation<TData = unknown, TVars = void>(opts: Options<TData, TVars>) {
  const qc = useQueryClient()
  return useMutation<TData, Error, TVars>({
    mutationFn: opts.mutationFn,
    onSuccess: async (data, vars) => {
      opts.onSuccess?.(data, vars)
      const msg = typeof opts.success === "function" ? opts.success(data, vars) : opts.success
      if (msg) toast.success(msg)
      await Promise.all((opts.invalidate ?? []).map((queryKey) => qc.invalidateQueries({ queryKey })))
    },
    onError: (err) => {
      if (opts.toastError !== false) toast.error(errorMessage(err))
    },
  })
}
