# GPT-GO iCloud 模块前端重建技术规范(所有视图重建必须遵守)

## 目标
把 iCloud-Privacy-Mail-v2 的 Vue 视图功能 1:1 重建为 React 组件,**功能与布局结构照原样**,仅 UI 换成 GPT-GO 的 shadcn/ui 风格。一个功能都不许漏。

## 技术栈与约束
- React 18 + TypeScript,函数组件 + Hooks。不使用 class 组件。
- 数据层:**必须用已就绪的 `@/lib/icloud-queries.ts` 的 react-query hooks**(useICloud*),不要自己 fetch。缺方法就在该文件补,并保持与后端一致(后端 69 条路由已 1:1 迁移,挂载在 `/api/icloud`)。
- UI 组件:**必须用 `@/components/ui/*`**(shadcn 风格):button, card, input, textarea, label, select, switch, checkbox, badge, table, tabs, dialog, alert-dialog(确认), dropdown-menu, separator, skeleton, tooltip, scroll-area, popover, alert, sonner(toast)。
- 图标:`lucide-react`。
- toast:`import { toast } from "sonner"`,成功 `toast.success`,失败 `toast.error`。
- 确认对话框:用 `@/components/ui/alert-dialog` 的 AlertDialog(对齐原项目的确认文案,特别是「彻底删除」「远程清理」的警示文案)。
- 错误文案辅助:`import { errMsg } from "./shared"`(返回中文错误)。
- 状态徽章/进度条:`./shared` 里有 MailboxStatusBadge / ICloudStatusBadge / LevelBadge / ProgressBar / EmptyRow / LoginStateDot,直接用;不够用就在 shared.tsx 加。
- 样式:tailwind,卡片用 Card 组件,与 GPT-GO 其它页面风格一致(参考 `web/src/pages/settings.tsx` 的卡片用法)。
- 每个视图导出 `export function XxxTab({ enabled }: { enabled: boolean })`,查询 hook 传 enabled。文件放 `web/src/pages/icloud/`,命名 `<name>-tab.tsx`。

## API 基址
所有 iCloud 接口经 `icloudApi`(自动加 `/api/icloud` 前缀、解 `{success,data}` 信封、401 处理)。路径写相对路径如 `/mailboxes`、`/apple-mail/cleanup`。

## 关键后端语义(已确认,照此对接)
- 删除邮箱:`DELETE /mailboxes/:id`,彻底删除加 `?local_only=0`(默认),仅删本地加 `?local_only=1`。前端用 `useICloudDeleteMailboxOne({id, localOnly})`。
- 批量彻底删除:输入邮箱列表 → `useICloudResolveMailboxEmails(emails)` 解析出 `{items,missing}` → 逐个 `useICloudDeleteMailboxOne({id, localOnly:false})`,**前端维护删除队列+进度 toast**(执行中/排队中/已完成/成功/失败),按账号限流并发(同一账号同时只删一个)。
- 彻底删除全部 Apple 邮件:`useICloudAppleMailCleanupStart()` → POST `/apple-mail/cleanup {account_ids:[],scope:'all',strategy:'move_then_destroy',purge_local:true}`,启动后端任务;`useICloudAppleMailCleanupStatus(enabled)` 轮询(运行中2s、空闲8s),展示 discovered/moved_to_trash/destroyed/local_removed;`useICloudAppleMailCleanupCancel()` 取消。
- 远程清理:单个 `useICloudRemoteCleanMailbox(id)`;批量 `useICloudRemoteCleanMailboxes({mailbox_ids?|account_id?})`。
- 批量同步邮件:`useICloudSyncAllMessages({account_id?,full_scan?})` 触发;`useICloudSyncAllMessagesStatus(enabled)` 轮询进度(processed/total/created/updated/fallbacks/failed)。
- 取码(后台带缓存):`useICloudMailboxCode(id, {allow_stale})`,GET `/mailboxes/:id/code?allow_stale=1`。
- 单封邮件详情:`useICloudMailboxMessage(mailboxId, messageId, enabled)`。
- 渠道回退:创建隐私邮箱 channel 下拉 `auto`(新接口 apple_account 优先,失败回退旧接口 icloud_web)/ `apple_account`(仅新)/ `icloud_web`(仅旧)。
- 导出:`useICloudExportRuntime/ExportMailboxAPIs/ExportMailboxEmails`,返回内容前端触发 .txt/.json 下载。
- 数据库维护:`useICloudDatabaseStatus` + `useICloudDatabaseAction("backup"|"check"|"optimize")`。
- 更新/公告:`useICloudUpdateStatus`。
- SSE 实时刷新:暂用 react-query 轮询(refetchInterval)即可,不必实现 EventSource。

## 输出要求
直接产出可编译的 `.tsx` 文件内容。完成后必须能通过 `cd web && npx tsc -b` 类型检查。功能完整性 > 代码精简,**宁可长也不许漏功能**。
