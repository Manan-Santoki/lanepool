import { getRouteApi, useNavigate } from "@tanstack/react-router"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { PageHeader } from "@/components/page-header"
import { AccountSettings } from "@/pages/settings/account"
import { AdminsSettings } from "@/pages/settings/admins"
import { AppSettingsTab } from "@/pages/settings/app"
import { EngineSettingsTab } from "@/pages/settings/engine"
import { TokensSettings } from "@/pages/settings/tokens"
import { SETTINGS_TABS, type SettingsTab } from "@/pages/settings/tabs"


const LABELS: Record<SettingsTab, string> = {
  engine: "Engine",
  app: "App",
  admins: "Admins",
  tokens: "API tokens",
  account: "Account",
}

const route = getRouteApi("/app/settings")

export function SettingsPage() {
  const { tab = "engine" } = route.useSearch()
  const navigate = useNavigate()
  return (
    <>
      <PageHeader title="Settings" description="Engine behaviour, app configuration, admins and API access." />
      <Tabs
        value={tab}
        onValueChange={(t) => void navigate({ to: "/settings", search: { tab: t as SettingsTab }, replace: true })}
        className="gap-4"
      >
        <div className="-mx-4 overflow-x-auto px-4 sm:mx-0 sm:px-0">
          <TabsList>
            {SETTINGS_TABS.map((t) => (
              <TabsTrigger key={t} value={t}>
                {LABELS[t]}
              </TabsTrigger>
            ))}
          </TabsList>
        </div>
        <TabsContent value="engine">
          <EngineSettingsTab />
        </TabsContent>
        <TabsContent value="app">
          <AppSettingsTab />
        </TabsContent>
        <TabsContent value="admins">
          <AdminsSettings />
        </TabsContent>
        <TabsContent value="tokens">
          <TokensSettings />
        </TabsContent>
        <TabsContent value="account">
          <AccountSettings />
        </TabsContent>
      </Tabs>
    </>
  )
}
