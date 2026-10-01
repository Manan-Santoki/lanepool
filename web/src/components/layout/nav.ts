import {
  ActivityIcon,
  BarChart3Icon,
  BellIcon,
  CableIcon,
  FlameIcon,
  LayoutDashboardIcon,
  NetworkIcon,
  ScrollTextIcon,
  ServerIcon,
  SettingsIcon,
  UsersIcon,
  type LucideIcon,
} from "lucide-react"

export interface NavItem {
  to: "/" | "/lanes" | "/connections" | "/users" | "/logs" | "/analytics" | "/burned" | "/providers" | "/alerts" | "/settings"
  label: string
  icon: LucideIcon
}

export const NAV_GROUPS: { label: string; items: NavItem[] }[] = [
  {
    label: "Monitor",
    items: [
      { to: "/", label: "Overview", icon: LayoutDashboardIcon },
      { to: "/lanes", label: "Lanes", icon: NetworkIcon },
      { to: "/connections", label: "Live connections", icon: CableIcon },
      { to: "/logs", label: "Logs", icon: ScrollTextIcon },
      { to: "/analytics", label: "Analytics", icon: BarChart3Icon },
    ],
  },
  {
    label: "Manage",
    items: [
      { to: "/users", label: "Users", icon: UsersIcon },
      { to: "/burned", label: "Burned IPs", icon: FlameIcon },
      { to: "/providers", label: "Providers", icon: ServerIcon },
      { to: "/alerts", label: "Alerts", icon: BellIcon },
      { to: "/settings", label: "Settings", icon: SettingsIcon },
    ],
  },
]

export const ALL_NAV = NAV_GROUPS.flatMap((g) => g.items)
export { ActivityIcon as BrandIcon }
