import { StrictMode } from "react"
import { createRoot } from "react-dom/client"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { RouterProvider } from "@tanstack/react-router"
import { ThemeProvider } from "next-themes"
import { Toaster } from "@/components/ui/sonner"
import { TooltipProvider } from "@/components/ui/tooltip"
import { createAppRouter } from "@/router"
import { isApiError, setUnauthorizedHandler } from "@/lib/api"
import { qk } from "@/hooks/query-keys"
import "./index.css"

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 10_000,
      refetchOnWindowFocus: true,
      retry: (failureCount, error) => {
        // Never retry auth/permission/validation errors.
        if (isApiError(error) && error.status >= 400 && error.status < 500) return false
        return failureCount < 2
      },
    },
  },
})

const router = createAppRouter(queryClient)

// Any 401 from an authenticated endpoint means the session expired: drop cached data and go to /login.
setUnauthorizedHandler(() => {
  const { pathname, href } = router.state.location
  if (pathname === "/login" || pathname === "/setup") return
  queryClient.removeQueries({ queryKey: qk.me })
  queryClient.clear()
  void router.navigate({ to: "/login", search: { redirect: href } })
})

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <ThemeProvider attribute="class" defaultTheme="system" enableSystem storageKey="lanepool-theme" disableTransitionOnChange>
      <QueryClientProvider client={queryClient}>
        <TooltipProvider delayDuration={200}>
          <RouterProvider router={router} />
          <Toaster position="bottom-right" richColors closeButton />
        </TooltipProvider>
      </QueryClientProvider>
    </ThemeProvider>
  </StrictMode>,
)
