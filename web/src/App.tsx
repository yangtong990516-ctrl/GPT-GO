import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { BrowserRouter, Navigate, Route, Routes } from "react-router-dom"
import { ConsoleGate } from "@/components/console-gate"
import Layout from "@/components/layout"
import AccountsPage from "@/pages/accounts"
import DashboardPage from "@/pages/dashboard"
import EmailsPage from "@/pages/emails"
import ICloudPage from "@/pages/icloud"
import MailboxesPage from "@/pages/mailboxes"
import PaymentCheckPage from "@/pages/payment-check"
import RebindPage from "@/pages/rebind"
import RunsPage from "@/pages/runs"
import SettingsPage from "@/pages/settings"
import { TooltipProvider } from "@/components/ui/tooltip"

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 1,
      staleTime: 10_000,
      refetchOnWindowFocus: false,
    },
  },
})

export default function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <TooltipProvider>
        <BrowserRouter>
          <ConsoleGate>
            <Routes>
              <Route element={<Layout />}>
                <Route index element={<DashboardPage />} />
                <Route path="accounts" element={<AccountsPage />} />
                <Route path="payment-check" element={<PaymentCheckPage />} />
                <Route path="rebind" element={<RebindPage />} />
                <Route path="emails" element={<EmailsPage />} />
                <Route path="mailboxes" element={<MailboxesPage />} />
                <Route path="icloud" element={<ICloudPage />} />
                <Route path="runs" element={<RunsPage />} />
                {/* 日志已并入注册运行页(同屏查看) —— /logs 旧链接重定向到 /runs,避免死链 */}
                <Route path="logs" element={<Navigate to="/runs" replace />} />
                <Route path="logs/:runId" element={<Navigate to="/runs" replace />} />
                <Route path="settings" element={<SettingsPage />} />
                <Route path="*" element={<DashboardPage />} />
              </Route>
            </Routes>
          </ConsoleGate>
        </BrowserRouter>
      </TooltipProvider>
    </QueryClientProvider>
  )
}
