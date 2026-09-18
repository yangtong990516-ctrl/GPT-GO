import { Link } from "react-router-dom"
import {
  CheckCircle2,
  Crown,
  Globe,
  Mail,
  MailCheck,
  ShieldAlert,
  Sparkles,
  Users,
  XCircle,
} from "lucide-react"
import { PageHeader, StatCard } from "@/components/page-header"
import { TableSkeleton } from "@/components/data-table"
import { Badge } from "@/components/ui/badge"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { useOverviewStats, useSentinelVersion } from "@/lib/queries"
import { formatRelative } from "@/lib/format"

export default function DashboardPage() {
  const stats = useOverviewStats()
  const sentinel = useSentinelVersion()

  if (stats.isPending) {
    return (
      <div>
        <PageHeader title="总览" description="账号、邮箱、代理资源全貌" />
        <TableSkeleton rows={4} cols={4} />
      </div>
    )
  }

  const s = stats.data
  const acc = s?.accounts
  const em = s?.emails
  const px = s?.proxies
  const sv = sentinel.data

  return (
    <div className="space-y-6">
      <PageHeader title="总览" description="账号、邮箱、代理资源全貌" />

      {/* Accounts */}
      <section>
        <div className="mb-3 flex items-center justify-between">
          <h2 className="flex items-center gap-2 text-sm font-medium">
            <Users className="size-4" /> 账号池
          </h2>
          <Link to="/accounts" className="text-primary text-sm hover:underline">
            管理账号 →
          </Link>
        </div>
        <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
          <StatCard label="账号总数" value={acc?.total ?? 0} hint={`今日新增 ${acc?.today ?? 0}`} />
          <StatCard
            label="Plus 账号"
            value={acc?.plus.total ?? 0}
            icon={<Crown className="size-4 text-amber-500" />}
            hint={`已绑手机 ${acc?.plus.bound ?? 0} / 未绑 ${acc?.plus.unbound ?? 0}`}
          />
          <StatCard
            label="Free 账号"
            value={acc?.free.total ?? 0}
            hint={`有优惠资格 ${acc?.free.eligible ?? 0} / 无资格 ${acc?.free.ineligible ?? 0}`}
          />
          <StatCard
            label="TOTP 完整"
            value={acc?.totpComplete ?? 0}
            icon={<Sparkles className="size-4 text-violet-500" />}
            hint="已配置 2FA 密钥的账号"
          />
        </div>
      </section>

      {/* Emails */}
      <section>
        <div className="mb-3 flex items-center justify-between">
          <h2 className="flex items-center gap-2 text-sm font-medium">
            <Mail className="size-4" /> 邮箱池
          </h2>
          <Link to="/emails" className="text-primary text-sm hover:underline">
            管理邮箱 →
          </Link>
        </div>
        <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
          <StatCard
            label="可用邮箱"
            value={em?.available ?? 0}
            icon={<MailCheck className="size-4 text-emerald-500" />}
            hint={`保留 ${em?.reserved ?? 0}`}
          />
          <StatCard
            label="失败邮箱"
            value={em?.failed ?? 0}
            icon={<XCircle className="size-4 text-red-500" />}
            hint={`隔离中 ${em?.quarantined ?? 0}`}
          />
          <StatCard label="Mailcom 别名" value={em?.aliases ?? 0} />
          <StatCard
            label="开通来源"
            value={(em?.mailcode ?? 0) + (em?.remail ?? 0)}
            hint={`Mailcode ${em?.mailcode ?? 0} / Remail ${em?.remail ?? 0}`}
          />
        </div>
      </section>

      {/* Proxies + Sentinel */}
      <div className="grid gap-6 lg:grid-cols-2">
        <section>
          <div className="mb-3 flex items-center justify-between">
            <h2 className="flex items-center gap-2 text-sm font-medium">
              <Globe className="size-4" /> 代理池
            </h2>
            <Link to="/settings" className="text-primary text-sm hover:underline">
              管理代理 →
            </Link>
          </div>
          <div className="grid grid-cols-2 gap-3">
            <StatCard label="代理总数" value={px?.total ?? 0} hint={`启用 ${px?.enabled ?? 0}`} />
            <StatCard
              label="可用代理"
              value={px?.available ?? 0}
              icon={<CheckCircle2 className="size-4 text-emerald-500" />}
            />
            <StatCard label="已占用" value={px?.used ?? 0} />
            <StatCard
              label="隔离"
              value={px?.quarantined ?? 0}
              icon={<ShieldAlert className="size-4 text-amber-500" />}
            />
          </div>
        </section>

        {/* Sentinel */}
        <section>
          <div className="mb-3 flex items-center justify-between">
            <h2 className="flex items-center gap-2 text-sm font-medium">
              <ShieldAlert className="size-4" /> Sentinel 版本监控
            </h2>
            <Link to="/settings" className="text-primary text-sm hover:underline">
              设置 →
            </Link>
          </div>
          <Card>
            <CardHeader>
              <CardTitle className="flex items-center gap-2 text-base">
                内置版本
                <Badge variant="secondary" className="font-mono">
                  {sv?.configured_version ?? "…"}
                </Badge>
              </CardTitle>
              <CardDescription>
                上游最新：{sv?.version || "未检测到"}；最近检查{" "}
                {formatRelative(sv?.last_checked_at)}
              </CardDescription>
            </CardHeader>
            <CardContent className="space-y-2 text-sm">
              <div className="flex items-center justify-between">
                <span className="text-muted-foreground">CDN 可达性</span>
                {sv?.reachable ? (
                  <Badge variant="secondary" className="text-emerald-600 dark:text-emerald-400">
                    可达
                  </Badge>
                ) : (
                  <Badge variant="destructive">不可达</Badge>
                )}
              </div>
              <div className="flex items-center justify-between">
                <span className="text-muted-foreground">版本过期</span>
                {sv?.is_expired === true ? (
                  <Badge variant="destructive">已过期</Badge>
                ) : sv?.is_expired === false ? (
                  <Badge variant="secondary" className="text-emerald-600 dark:text-emerald-400">
                    最新
                  </Badge>
                ) : (
                  <Badge variant="outline">未知</Badge>
                )}
              </div>
              <div className="flex items-center justify-between">
                <span className="text-muted-foreground">检查使用代理</span>
                <span>{sv?.proxy_used ? "是" : "否"}</span>
              </div>
              {sv?.check_error && (
                <p className="text-destructive text-xs">错误：{sv.check_error}</p>
              )}
            </CardContent>
          </Card>
        </section>
      </div>
    </div>
  )
}
