import { Outlet, useRouterState } from "@tanstack/react-router"
import { Separator } from "@/components/ui/separator"
import { SidebarInset, SidebarProvider, SidebarTrigger } from "@/components/ui/sidebar"
import { AppSidebar } from "@/components/layout/app-sidebar"
import { EngineStatusIndicator } from "@/components/layout/engine-status"
import { ALL_NAV } from "@/components/layout/nav"
import { ThemeToggle } from "@/components/layout/theme-toggle"
import { UserMenu } from "@/components/layout/user-menu"
import { useStreamConnection } from "@/hooks/use-stream"

function sidebarDefaultOpen() {
  return !document.cookie.split("; ").includes("sidebar_state=false")
}

export function AppShell() {
  useStreamConnection(true)
  const pathname = useRouterState({ select: (s) => s.location.pathname })
  const current = ALL_NAV.find((n) => (n.to === "/" ? pathname === "/" : pathname.startsWith(n.to)))

  return (
    <SidebarProvider defaultOpen={sidebarDefaultOpen()}>
      <AppSidebar />
      <SidebarInset className="min-w-0">
        <header className="sticky top-0 z-20 flex h-14 shrink-0 items-center gap-2 border-b bg-background/85 px-3 backdrop-blur supports-backdrop-filter:bg-background/70 sm:px-4">
          <SidebarTrigger className="-ml-1" />
          <Separator orientation="vertical" className="mx-1 data-[orientation=vertical]:h-5" />
          <span className="truncate text-sm font-medium">{current?.label ?? "lanepool"}</span>
          <div className="ml-auto flex items-center gap-1">
            <EngineStatusIndicator />
            <ThemeToggle />
            <UserMenu />
          </div>
        </header>
        <main className="mx-auto w-full max-w-[1600px] min-w-0 flex-1 space-y-6 p-4 sm:p-6">
          <Outlet />
        </main>
      </SidebarInset>
    </SidebarProvider>
  )
}
