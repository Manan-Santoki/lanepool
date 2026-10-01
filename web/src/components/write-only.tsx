import type { ReactNode } from "react"
import { useCanWrite } from "@/hooks/use-auth"

/** Renders children only for admins with write access (role "admin"). */
export function WriteOnly({ children, fallback = null }: { children: ReactNode; fallback?: ReactNode }) {
  return useCanWrite() ? <>{children}</> : <>{fallback}</>
}
