// 取码工具 tab:按邮箱+关键词拉取最新验证码(公共接口)。
import * as React from "react"
import { KeyRound } from "lucide-react"
import { HttpError } from "@/lib/api"
import { icloudApi, type ICloudCodeResult } from "@/lib/icloud-api"
import { useICloudMailboxes } from "@/lib/icloud-queries"
import { formatRelative } from "@/lib/format"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

export function CodeTab({ enabled }: { enabled: boolean }) {
  const mailboxes = useICloudMailboxes({ page: 1, pageSize: 100, status: "available" }, enabled)
  const [email, setEmail] = React.useState("")
  const [keyword, setKeyword] = React.useState("AI Platform")
  const [after, setAfter] = React.useState("")
  const [pending, setPending] = React.useState(false)
  const [result, setResult] = React.useState<ICloudCodeResult | null>(null)
  const [error, setError] = React.useState<string | null>(null)

  const mailboxItems = mailboxes.data?.items ?? []

  const doFetch = async () => {
    if (!email.trim()) {
      setError("请输入或选择邮箱地址")
      return
    }
    setPending(true)
    setResult(null)
    setError(null)
    try {
      const r = await icloudApi.get<ICloudCodeResult>(
        `/v1/mailboxes/${encodeURIComponent(email.trim())}/code`,
        {
          keyword: keyword.trim() || undefined,
          after: after ? new Date(after).toISOString() : undefined,
          allow_stale: true,
        },
      )
      setResult(r)
    } catch (e) {
      if (e instanceof HttpError && e.status === 401) {
        setError("公共接口鉴权失败(401)——请先在「系统设置」配置公共 API Key,或改用后台登录态取码")
      } else {
        setError(e instanceof HttpError ? e.message : "取码失败")
      }
    } finally {
      setPending(false)
    }
  }

  return (
    <div className="grid gap-4 lg:grid-cols-2">
      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="text-sm">取码工具</CardTitle>
          <CardDescription>按邮箱与关键词提取最新一封邮件中的验证码</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="grid gap-2">
            <Label>邮箱(从邮箱池选择)</Label>
            <Select
              value={mailboxItems.some((m) => m.email === email) ? email : ""}
              onValueChange={setEmail}
            >
              <SelectTrigger>
                <SelectValue placeholder="选择一个可用邮箱…" />
              </SelectTrigger>
              <SelectContent>
                {mailboxItems.map((m) => (
                  <SelectItem key={m.id} value={m.email}>
                    {m.email}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="grid gap-2">
            <Label>或手动输入邮箱</Label>
            <Input
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder="xxx@privaterelay.appleid.com"
              className="font-mono"
            />
          </div>
          <div className="grid gap-2">
            <Label>关键词</Label>
            <Input value={keyword} onChange={(e) => setKeyword(e.target.value)} placeholder="AI Platform" />
          </div>
          <div className="grid gap-2">
            <Label>只取该时间之后的邮件(可空)</Label>
            <Input type="datetime-local" value={after} onChange={(e) => setAfter(e.target.value)} />
          </div>
          <Button className="w-full" onClick={doFetch} disabled={pending}>
            <KeyRound /> {pending ? "取码中…" : "获取验证码"}
          </Button>
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="text-sm">结果</CardTitle>
          <CardDescription>最新匹配邮件的验证码</CardDescription>
        </CardHeader>
        <CardContent>
          {error ? (
            <Alert variant="destructive">
              <AlertTitle>取码失败</AlertTitle>
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          ) : result ? (
            <div className="space-y-4">
              <p className="text-center font-mono text-4xl font-semibold tracking-widest">
                {result.code || "-"}
              </p>
              <dl className="space-y-1.5 text-sm">
                <div className="flex justify-between gap-3">
                  <dt className="text-muted-foreground">邮箱</dt>
                  <dd className="font-mono text-xs break-all">{result.email}</dd>
                </div>
                <div className="flex justify-between gap-3">
                  <dt className="text-muted-foreground">主题</dt>
                  <dd className="text-xs break-all">{result.subject || "-"}</dd>
                </div>
                <div className="flex justify-between gap-3">
                  <dt className="text-muted-foreground">发件人</dt>
                  <dd className="text-xs break-all">{result.from || "-"}</dd>
                </div>
                <div className="flex justify-between gap-3">
                  <dt className="text-muted-foreground">接收时间</dt>
                  <dd className="text-xs">{formatRelative(result.received_at)}</dd>
                </div>
              </dl>
            </div>
          ) : (
            <p className="text-muted-foreground py-10 text-center text-sm">
              填写左侧条件后点击「获取验证码」
            </p>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
