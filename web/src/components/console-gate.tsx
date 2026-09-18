// 全局控制台登录门禁:未登录时整屏显示登录页,登录后渲染子内容。
// 整个 GPT-GO 控制台共用一道密码(默认 admin / 1024,可在系统设置修改)。
import * as React from "react"
import { toast } from "sonner"
import {
  CreditCard,
  Eye,
  EyeOff,
  Loader2,
  Lock,
  MailCheck,
  ShieldCheck,
  User,
  UserCheck,
} from "lucide-react"
import { useConsoleAuthStatus, useConsoleLogin } from "@/lib/console-auth"
import { HttpError } from "@/lib/api"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Skeleton } from "@/components/ui/skeleton"

function LoginScreen() {
  const login = useConsoleLogin()
  const [username, setUsername] = React.useState("")
  const [password, setPassword] = React.useState("")
  const [showPassword, setShowPassword] = React.useState(false)

  const submit = (e: React.FormEvent) => {
    e.preventDefault()
    login.mutate(
      { username: username.trim(), password },
      {
        onSuccess: () => toast.success("登录成功"),
        onError: (err) =>
          toast.error(err instanceof HttpError ? err.message : "登录失败"),
      },
    )
  }

  const features = [
    { icon: UserCheck, text: "账号池管理 · 验活 / 查优惠 / 补 2FA" },
    { icon: CreditCard, text: "支付类型检测 · 并发实时结果" },
    { icon: MailCheck, text: "邮箱池 · 换绑 · iCloud 隐私邮箱" },
  ]

  return (
    <div className="relative flex min-h-screen items-center justify-center overflow-hidden bg-gradient-to-br from-slate-950 via-slate-900 to-slate-950 px-4">
      {/* 背景装饰光斑 */}
      <div className="pointer-events-none absolute -left-32 -top-32 h-96 w-96 rounded-full bg-primary/20 blur-3xl" />
      <div className="pointer-events-none absolute -bottom-32 -right-32 h-96 w-96 rounded-full bg-primary/10 blur-3xl" />

      <div className="relative z-10 grid w-full max-w-4xl overflow-hidden rounded-2xl border border-white/10 bg-slate-900/80 shadow-2xl backdrop-blur md:grid-cols-2">
        {/* 左侧品牌区(移动端隐藏) */}
        <div className="relative hidden flex-col justify-between bg-gradient-to-br from-primary/90 to-primary/60 p-10 text-primary-foreground md:flex">
          <div>
            <div className="flex items-center gap-2.5">
              <div className="flex h-10 w-10 items-center justify-center rounded-xl bg-white/15 backdrop-blur">
                <ShieldCheck className="h-6 w-6" />
              </div>
              <span className="text-xl font-bold tracking-tight">GPT-GO</span>
            </div>
            <p className="mt-6 text-2xl font-semibold leading-snug">
              ChatGPT 账号资产
              <br />
              一站式管理控制台
            </p>
            <p className="mt-3 text-sm text-primary-foreground/80">
              注册、验活、优惠资格、支付检测、邮箱换绑——全流程本地化管理。
            </p>
          </div>
          <ul className="mt-10 space-y-3">
            {features.map((f) => (
              <li key={f.text} className="flex items-center gap-3 text-sm text-primary-foreground/90">
                <f.icon className="h-4 w-4 shrink-0" />
                {f.text}
              </li>
            ))}
          </ul>
        </div>

        {/* 右侧登录表单 */}
        <div className="flex flex-col justify-center p-8 sm:p-10">
          <div className="mb-6 flex items-center gap-2.5 md:hidden">
            <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-primary/15">
              <ShieldCheck className="h-5 w-5 text-primary" />
            </div>
            <span className="text-lg font-bold text-slate-100">GPT-GO</span>
          </div>

          <h1 className="text-2xl font-semibold text-slate-100">欢迎回来</h1>
          <p className="mt-1.5 text-sm text-slate-400">登录以进入控制台</p>

          <form onSubmit={submit} className="mt-8 grid gap-5">
            <div className="grid gap-2">
              <Label htmlFor="gate-user" className="text-slate-300">用户名</Label>
              <div className="relative">
                <User className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-slate-500" />
                <Input
                  id="gate-user"
                  value={username}
                  onChange={(e) => setUsername(e.target.value)}
                  placeholder="admin"
                  autoComplete="username"
                  autoFocus
                  className="border-slate-700 bg-slate-800/60 pl-9 text-slate-100 placeholder:text-slate-500 focus-visible:ring-primary"
                />
              </div>
            </div>
            <div className="grid gap-2">
              <Label htmlFor="gate-pass" className="text-slate-300">密码</Label>
              <div className="relative">
                <Lock className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-slate-500" />
                <Input
                  id="gate-pass"
                  type={showPassword ? "text" : "password"}
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  placeholder="••••"
                  autoComplete="current-password"
                  className="border-slate-700 bg-slate-800/60 pl-9 pr-10 text-slate-100 placeholder:text-slate-500 focus-visible:ring-primary"
                />
                <button
                  type="button"
                  onClick={() => setShowPassword((v) => !v)}
                  className="absolute right-3 top-1/2 -translate-y-1/2 text-slate-500 hover:text-slate-300"
                  tabIndex={-1}
                >
                  {showPassword ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                </button>
              </div>
            </div>
            <Button
              type="submit"
              className="mt-1 w-full"
              size="lg"
              disabled={!username.trim() || !password || login.isPending}
            >
              {login.isPending ? (
                <span className="flex items-center gap-2">
                  <Loader2 className="h-4 w-4 animate-spin" /> 登录中…
                </span>
              ) : (
                "登 录"
              )}
            </Button>
          </form>
          <p className="mt-6 text-center text-xs text-slate-500">
            默认账号 admin / 1024,可在「系统设置」修改
          </p>
        </div>
      </div>
    </div>
  )
}

function GateLoading() {
  return (
    <div className="flex min-h-screen items-center justify-center bg-background">
      <div className="w-full max-w-sm space-y-3 px-4">
        <Skeleton className="h-10 w-full" />
        <Skeleton className="h-10 w-full" />
        <Skeleton className="h-10 w-full" />
      </div>
    </div>
  )
}

// ConsoleGate 包裹整个应用:认证状态加载中显示骨架,未认证显示登录页,已认证渲染 children。
export function ConsoleGate({ children }: { children: React.ReactNode }) {
  const auth = useConsoleAuthStatus()

  if (auth.isPending) return <GateLoading />
  // 后端不可用或查询失败时,保守地显示登录页(登录接口会再报具体错误)。
  if (auth.isError) return <LoginScreen />
  if (!auth.data?.authenticated) return <LoginScreen />

  return <>{children}</>
}
