// iCloud 邮箱管理主页面:八个功能 tab。
// 登录由全局控制台门禁(components/console-gate.tsx)统一处理,本页不再单独鉴权。
// 布局对齐原项目:控制台 / Apple 账号 / 邮箱池 / 创建隐私邮箱 / 本地导出 / 系统设置,
// 另加 取码工具(公共取码) 与 事件日志。
import * as React from "react"
import { useQueryClient } from "@tanstack/react-query"
import { RefreshCw } from "lucide-react"
import { PageHeader } from "@/components/page-header"
import { Button } from "@/components/ui/button"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { AccountsTab } from "./accounts-tab"
import { DashboardTab } from "./dashboard-tab"
import { EventsTab } from "./events-tab"
import { ExportsTab } from "./exports-tab"
import { MailboxesTab } from "./mailboxes-tab"
import { PublicCodeTab } from "./public-code-tab"
import { SettingsTab } from "./settings-tab"
import { TasksTab } from "./tasks-tab"

export default function ICloudPage() {
  const qc = useQueryClient()
  const [tab, setTab] = React.useState("dashboard")
  // 进入本页即代表已通过全局登录,数据查询直接启用。
  const enabled = true

  return (
    <div>
      <PageHeader
        title="iCloud 邮箱管理"
        description="Apple 隐私邮箱创建、取码、收信与账号登录态管理"
        actions={
          <Button
            variant="outline"
            size="sm"
            onClick={() => qc.invalidateQueries({ queryKey: ["icloud"] })}
          >
            <RefreshCw /> 刷新
          </Button>
        }
      />

      <Tabs value={tab} onValueChange={setTab}>
        <TabsList className="mb-4 flex-wrap">
          <TabsTrigger value="dashboard">控制台</TabsTrigger>
          <TabsTrigger value="accounts">Apple 账号</TabsTrigger>
          <TabsTrigger value="mailboxes">邮箱池</TabsTrigger>
          <TabsTrigger value="tasks">创建隐私邮箱</TabsTrigger>
          <TabsTrigger value="exports">本地导出</TabsTrigger>
          <TabsTrigger value="settings">系统设置</TabsTrigger>
          <TabsTrigger value="public-code">取码工具</TabsTrigger>
          <TabsTrigger value="events">事件日志</TabsTrigger>
        </TabsList>

        <TabsContent value="dashboard">
          <DashboardTab enabled={enabled} onGoTasks={() => setTab("tasks")} />
        </TabsContent>
        <TabsContent value="accounts">
          <AccountsTab enabled={enabled} />
        </TabsContent>
        <TabsContent value="mailboxes">
          <MailboxesTab enabled={enabled} />
        </TabsContent>
        <TabsContent value="tasks">
          <TasksTab enabled={enabled} />
        </TabsContent>
        <TabsContent value="exports">
          <ExportsTab enabled={enabled} />
        </TabsContent>
        <TabsContent value="settings">
          <SettingsTab enabled={enabled} />
        </TabsContent>
        <TabsContent value="public-code">
          <PublicCodeTab enabled={enabled} />
        </TabsContent>
        <TabsContent value="events">
          <EventsTab enabled={enabled} />
        </TabsContent>
      </Tabs>
    </div>
  )
}
