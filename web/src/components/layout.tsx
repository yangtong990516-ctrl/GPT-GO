import * as React from "react"
import { NavLink, Outlet } from "react-router-dom"
import {
  Activity,
  Cloud,
  CreditCard,
  LogOut,
  MailQuestion,
  Inbox,
  KeyRound,
  LayoutDashboard,
  Mail,
  Moon,
  Rocket,
  Settings2,
  Sun,
  Users,
} from "lucide-react"
import { toast } from "sonner"
import { cn } from "@/lib/utils"
import { HttpError } from "@/lib/api"
import { useConsoleChangePassword, useConsoleLogout } from "@/lib/console-auth"
import { useHealth } from "@/lib/queries"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Separator } from "@/components/ui/separator"
import { Toaster } from "@/components/ui/sonner"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"

const NAV = [
  { to: "/", label: "总览", icon: LayoutDashboard, end: true },
  { to: "/runs", label: "注册运行", icon: Rocket },
  { to: "/accounts", label: "账号池", icon: Users },
  { to: "/emails", label: "邮箱池", icon: Mail },
  { to: "/mailboxes", label: "邮箱开通", icon: Inbox },
  { to: "/icloud", label: "iCloud 邮箱", icon: Cloud },
  { to: "/rebind", label: "邮箱换绑", icon: MailQuestion },
  { to: "/payment-check", label: "支付检测", icon: CreditCard },
  { to: "/settings", label: "系统设置", icon: Settings2 },
]

function useTheme() {
  const [dark, setDark] = React.useState(() => {
    if (typeof window === "undefined") return false
    const saved = localStorage.getItem("gpt-go-theme")
    if (saved) return saved === "dark"
    return window.matchMedia("(prefers-color-scheme: dark)").matches
  })
  React.useEffect(() => {
    document.documentElement.classList.toggle("dark", dark)
    localStorage.setItem("gpt-go-theme", dark ? "dark" : "light")
  }, [dark])
  return { dark, toggle: () => setDark((d) => !d) }
}

// LogoutButton 退出全局控制台登录(回到登录门禁页)。
function LogoutButton() {
  const logout = useConsoleLogout()
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          variant="ghost"
          size="icon-sm"
          disabled={logout.isPending}
          onClick={() =>
            logout.mutate(undefined, {
              onSuccess: () => {
                // 硬刷新回根路径:React 应用完全重启,ConsoleGate 重新评估 → 必然回到登录页。
                // (仅 qc.clear() 会让查询陷入中间态,导致停留在旧页面/白屏。)
                window.location.href = "/"
              },
              onError: () => toast.error("退出失败"),
            })
          }
        >
          <LogOut />
        </Button>
      </TooltipTrigger>
      <TooltipContent>退出登录</TooltipContent>
    </Tooltip>
  )
}

// ConsolePasswordButton 导航栏「控制台密码」按钮:点击弹出对话框改全局进入密码。
// 改后后端使所有会话失效,刷新回登录页。
function ConsolePasswordButton() {
  const changePassword = useConsoleChangePassword()
  const [open, setOpen] = React.useState(false)
  const [oldPassword, setOldPassword] = React.useState("")
  const [newPassword, setNewPassword] = React.useState("")
  const [confirm, setConfirm] = React.useState("")

  const reset = () => {
    setOldPassword("")
    setNewPassword("")
    setConfirm("")
  }

  const submit = (e: React.FormEvent) => {
    e.preventDefault()
    if (newPassword !== confirm) {
      toast.error("两次输入的新密码不一致")
      return
    }
    changePassword.mutate(
      { old_password: oldPassword, new_password: newPassword },
      {
        onSuccess: () => {
          toast.success("密码已修改,请重新登录")
          setOpen(false)
          // 后端已使所有会话失效,刷新页面回登录页。
          setTimeout(() => window.location.reload(), 600)
        },
        onError: (err) =>
          toast.error(err instanceof HttpError ? err.message : "修改失败"),
      },
    )
  }

  return (
    <>
      <Tooltip>
        <TooltipTrigger asChild>
          <Button variant="ghost" size="icon-sm" onClick={() => setOpen(true)}>
            <KeyRound />
          </Button>
        </TooltipTrigger>
        <TooltipContent>控制台密码</TooltipContent>
      </Tooltip>

      <Dialog
        open={open}
        onOpenChange={(v) => {
          setOpen(v)
          if (!v) reset()
        }}
      >
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle>修改控制台密码</DialogTitle>
            <DialogDescription>
              修改整个控制台的进入密码(默认 1024)。修改后所有会话失效,需重新登录。
            </DialogDescription>
          </DialogHeader>
          <form onSubmit={submit} className="grid gap-4">
            <div className="grid gap-2">
              <Label htmlFor="nav-old-pass">原密码</Label>
              <Input
                id="nav-old-pass"
                type="password"
                value={oldPassword}
                onChange={(e) => setOldPassword(e.target.value)}
                autoComplete="current-password"
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="nav-new-pass">新密码</Label>
              <Input
                id="nav-new-pass"
                type="password"
                value={newPassword}
                onChange={(e) => setNewPassword(e.target.value)}
                autoComplete="new-password"
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="nav-confirm-pass">确认新密码</Label>
              <Input
                id="nav-confirm-pass"
                type="password"
                value={confirm}
                onChange={(e) => setConfirm(e.target.value)}
                autoComplete="new-password"
              />
            </div>
            <DialogFooter>
              <Button
                type="submit"
                disabled={
                  !oldPassword || !newPassword || !confirm || changePassword.isPending
                }
              >
                {changePassword.isPending ? "修改中…" : "确认修改"}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </>
  )
}

function HealthBadge() {
  const { data, isError } = useHealth()
  const ok = !isError && data?.status === "ok"
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Badge
          variant={ok ? "secondary" : "destructive"}
          className="cursor-default gap-1.5"
        >
          <span
            className={cn(
              "size-1.5 rounded-full",
              ok ? "bg-emerald-500" : "animate-pulse bg-current",
            )}
          />
          {isError ? "离线" : ok ? "正常" : "降级"}
        </Badge>
      </TooltipTrigger>
      <TooltipContent>
        <p>服务状态：{isError ? "无法连接后端" : data?.status}</p>
        {data && (
          <p>
            MongoDB：{data.mongodb.status}（{data.mongodb.database}）
          </p>
        )}
      </TooltipContent>
    </Tooltip>
  )
}

export default function Layout() {
  const { dark, toggle } = useTheme()
  return (
    <div className="bg-background text-foreground flex min-h-svh">
      {/* Sidebar */}
      <aside className="bg-card sticky top-0 flex h-svh w-52 shrink-0 flex-col border-r">
        <div className="flex items-center gap-2 px-3 py-4">
          <div className="bg-primary text-primary-foreground flex size-8 items-center justify-center rounded-lg">
            <Activity className="size-4" />
          </div>
          <div className="leading-tight">
            <p className="text-sm font-semibold">GPT-GO</p>
            <p className="text-muted-foreground text-xs">本地控制台</p>
          </div>
        </div>
        <Separator />
        <nav className="flex-1 space-y-1 overflow-y-auto px-2 py-2">
          {NAV.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              end={item.end}
              className={({ isActive }) =>
                cn(
                  "flex items-center gap-2.5 rounded-lg px-3 py-2 text-sm transition-colors",
                  isActive
                    ? "bg-primary/10 text-primary font-medium"
                    : "text-muted-foreground hover:bg-muted hover:text-foreground",
                )
              }
            >
              <item.icon className="size-4" />
              {item.label}
            </NavLink>
          ))}
        </nav>
        <Separator />
        <div className="flex items-center justify-between gap-2 p-3">
          <HealthBadge />
          <div className="flex items-center gap-1">
            <ConsolePasswordButton />
            <Tooltip>
              <TooltipTrigger asChild>
                <Button variant="ghost" size="icon-sm" onClick={toggle}>
                  {dark ? <Sun /> : <Moon />}
                </Button>
              </TooltipTrigger>
              <TooltipContent>{dark ? "切换浅色" : "切换深色"}</TooltipContent>
            </Tooltip>
            <LogoutButton />
          </div>
        </div>
      </aside>

      {/* Main */}
      <main className="min-w-0 flex-1">
        <div className="mx-auto w-full max-w-[1400px] p-6">
          <Outlet />
        </div>
      </main>
      <Toaster richColors position="top-right" />
    </div>
  )
}
