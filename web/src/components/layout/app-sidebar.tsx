import { Link, useRouterState } from "@tanstack/react-router"
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuBadge,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarRail,
  useSidebar,
} from "@/components/ui/sidebar"
import { BrandIcon, NAV_GROUPS } from "@/components/layout/nav"
import { useLanes } from "@/hooks/use-lanes"
import { useOverview } from "@/hooks/use-overview"
import { useStreamState } from "@/hooks/use-stream"

export function AppSidebar() {
  const pathname = useRouterState({ select: (s) => s.location.pathname })
  const { setOpenMobile, isMobile } = useSidebar()
  const lanes = useLanes().data
  const overview = useOverview().data
  const stream = useStreamState()
  const lanesUp = lanes?.filter((l) => l.status === "up").length
  const liveConns = stream?.activeConnections

  const badgeFor = (to: string) => {
    if (to === "/lanes" && lanes) return `${lanesUp}/${overview?.lanes.target ?? lanes.length}`
    if (to === "/connections" && liveConns !== undefined) return String(liveConns)
    return null
  }

  return (
    <Sidebar collapsible="icon">
      <SidebarHeader>
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton size="lg" asChild tooltip="lanepool">
              <Link to="/" onClick={() => isMobile && setOpenMobile(false)}>
                <div className="flex aspect-square size-8 items-center justify-center rounded-lg bg-primary text-primary-foreground">
                  <BrandIcon className="size-4" />
                </div>
                <div className="grid flex-1 text-left leading-tight">
                  <span className="truncate font-semibold">lanepool</span>
                  <span className="truncate text-xs text-muted-foreground">Rotating proxy</span>
                </div>
              </Link>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarHeader>
      <SidebarContent>
        {NAV_GROUPS.map((group) => (
          <SidebarGroup key={group.label}>
            <SidebarGroupLabel>{group.label}</SidebarGroupLabel>
            <SidebarGroupContent>
              <SidebarMenu>
                {group.items.map((item) => {
                  const active = item.to === "/" ? pathname === "/" : pathname.startsWith(item.to)
                  const badge = badgeFor(item.to)
                  return (
                    <SidebarMenuItem key={item.to}>
                      <SidebarMenuButton asChild isActive={active} tooltip={item.label}>
                        <Link to={item.to} onClick={() => isMobile && setOpenMobile(false)}>
                          <item.icon />
                          <span>{item.label}</span>
                        </Link>
                      </SidebarMenuButton>
                      {badge ? <SidebarMenuBadge className="tabular">{badge}</SidebarMenuBadge> : null}
                    </SidebarMenuItem>
                  )
                })}
              </SidebarMenu>
            </SidebarGroupContent>
          </SidebarGroup>
        ))}
      </SidebarContent>
      <SidebarFooter>
        <p className="px-2 pb-1 text-[11px] text-muted-foreground group-data-[collapsible=icon]:hidden">lanepool v2</p>
      </SidebarFooter>
      <SidebarRail />
    </Sidebar>
  )
}
