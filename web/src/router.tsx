import { QueryClient } from "@tanstack/react-query"
import {
  createRootRouteWithContext,
  createRoute,
  createRouter,
  lazyRouteComponent,
  Outlet,
  redirect,
} from "@tanstack/react-router"
import { AppShell } from "@/components/layout/app-shell"
import { RouteError, NotFound, FullPageSpinner } from "@/pages/system"
import { LoginPage } from "@/pages/auth/login"
import { SetupPage } from "@/pages/auth/setup"
import type { LogsTab } from "@/pages/logs"
import { SETTINGS_TABS, type SettingsTab } from "@/pages/settings/tabs"
import { isApiError } from "@/lib/api"
import { meQueryOptions, setupQueryOptions } from "@/hooks/use-auth"

export interface RouterContext {
  queryClient: QueryClient
}

const rootRoute = createRootRouteWithContext<RouterContext>()({
  component: Outlet,
  notFoundComponent: NotFound,
  errorComponent: RouteError,
  pendingComponent: FullPageSpinner,
})

/** Resolves whether the logged-in admin exists; null on 401. Other errors propagate. */
async function loadMe(qc: QueryClient) {
  try {
    return await qc.ensureQueryData(meQueryOptions)
  } catch (err) {
    if (isApiError(err) && err.status === 401) return null
    throw err
  }
}

const setupRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/setup",
  beforeLoad: async ({ context }) => {
    const status = await context.queryClient.ensureQueryData(setupQueryOptions)
    if (!status.needsSetup) throw redirect({ to: "/" })
  },
  component: SetupPage,
})

const loginRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/login",
  validateSearch: (search: Record<string, unknown>): { redirect?: string } => ({
    redirect: typeof search.redirect === "string" && search.redirect.startsWith("/") ? search.redirect : undefined,
  }),
  beforeLoad: async ({ context }) => {
    const status = await context.queryClient.ensureQueryData(setupQueryOptions)
    if (status.needsSetup) throw redirect({ to: "/setup" })
    const me = await loadMe(context.queryClient)
    if (me) throw redirect({ to: "/" })
  },
  component: LoginPage,
})

const appRoute = createRoute({
  getParentRoute: () => rootRoute,
  id: "app",
  beforeLoad: async ({ context, location }) => {
    const status = await context.queryClient.ensureQueryData(setupQueryOptions)
    if (status.needsSetup) throw redirect({ to: "/setup" })
    const me = await loadMe(context.queryClient)
    if (!me) {
      const target = location.href
      throw redirect({ to: "/login", search: { redirect: target !== "/" ? target : undefined } })
    }
    return { me }
  },
  component: AppShell,
})

const overviewRoute = createRoute({ getParentRoute: () => appRoute, path: "/", component: lazyRouteComponent(() => import("@/pages/overview"), "OverviewPage") })
const lanesRoute = createRoute({ getParentRoute: () => appRoute, path: "/lanes", component: lazyRouteComponent(() => import("@/pages/lanes"), "LanesPage") })
const connectionsRoute = createRoute({ getParentRoute: () => appRoute, path: "/connections", component: lazyRouteComponent(() => import("@/pages/connections"), "ConnectionsPage") })
const usersRoute = createRoute({ getParentRoute: () => appRoute, path: "/users", component: lazyRouteComponent(() => import("@/pages/users"), "UsersPage") })
const logsRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/logs",
  validateSearch: (search: Record<string, unknown>): { tab?: LogsTab } => ({
    tab: search.tab === "events" ? "events" : undefined,
  }),
  component: lazyRouteComponent(() => import("@/pages/logs"), "LogsPage"),
})
const analyticsRoute = createRoute({ getParentRoute: () => appRoute, path: "/analytics", component: lazyRouteComponent(() => import("@/pages/analytics"), "AnalyticsPage") })
const burnedRoute = createRoute({ getParentRoute: () => appRoute, path: "/burned", component: lazyRouteComponent(() => import("@/pages/burned"), "BurnedPage") })
const providersRoute = createRoute({ getParentRoute: () => appRoute, path: "/providers", component: lazyRouteComponent(() => import("@/pages/providers"), "ProvidersPage") })
const alertsRoute = createRoute({ getParentRoute: () => appRoute, path: "/alerts", component: lazyRouteComponent(() => import("@/pages/alerts"), "AlertsPage") })
const settingsRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/settings",
  validateSearch: (search: Record<string, unknown>): { tab?: SettingsTab } => ({
    tab: SETTINGS_TABS.includes(search.tab as SettingsTab) ? (search.tab as SettingsTab) : undefined,
  }),
  component: lazyRouteComponent(() => import("@/pages/settings"), "SettingsPage"),
})

const routeTree = rootRoute.addChildren([
  setupRoute,
  loginRoute,
  appRoute.addChildren([
    overviewRoute,
    lanesRoute,
    connectionsRoute,
    usersRoute,
    logsRoute,
    analyticsRoute,
    burnedRoute,
    providersRoute,
    alertsRoute,
    settingsRoute,
  ]),
])

export function createAppRouter(queryClient: QueryClient) {
  return createRouter({
    routeTree,
    context: { queryClient },
    defaultPreload: "intent",
    defaultPreloadStaleTime: 0,
    defaultPendingMs: 300,
    defaultPendingComponent: FullPageSpinner,
    scrollRestoration: true,
  })
}

declare module "@tanstack/react-router" {
  interface Register {
    router: ReturnType<typeof createAppRouter>
  }
}
