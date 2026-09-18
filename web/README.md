# GPT-GO Web 前端

基于 **React 19 + Vite + Tailwind CSS v4 + [shadcn/ui](https://github.com/shadcn-ui/ui)** 的 GPT-GO 本地控制台界面，对接 Go 后端（`internal/apiserver`）的全部接口。

## 页面与后端接口对应

| 页面 | 路由 | 对接接口 |
|------|------|---------|
| 总览 Dashboard | `/` | `GET /api/stats/overview`、`GET /api/health`、`GET /api/sentinel/version` |
| 账号池 | `/accounts` | `GET/POST /api/accounts`、`POST /api/accounts/bulk-delete` |
| 邮箱池 | `/emails` | `GET /api/emails`、`POST /api/emails/import`、`POST /api/emails/bulk-delete`、`POST /api/emails/reset-failed`、`POST /api/emails/:id/status`、`POST /api/emails/export` |
| 邮箱开通 | `/mailboxes` | 单页双区块（Mailcode 自建邮局 / Remail 接码平台）：`GET/PUT /api/mailcode/config`、`POST /api/mailcode/probe`、`POST /api/mailcode/create-mailboxes`；`GET/PUT /api/remail/config`、`POST /api/remail/probe`、`POST /api/remail/create-mailboxes`、`POST /api/remail/import-purchased-orders`、`GET /api/remail/wallet` |
| 系统设置 | `/settings` | 执行参数 `GET/PUT /api/settings/execution`、Sentinel `GET/PUT /api/sentinel/config`、`GET /api/sentinel/version`，以及代理池区块：`GET /api/proxies`、`POST /api/proxies/import`、`GET /api/proxies/countries`、`POST /api/proxies/test`、`GET/PATCH/DELETE /api/proxies/groups`、`POST /api/proxies/bulk-delete`、`DELETE /api/proxies`、`POST /api/proxies/restore-used`、`PATCH/DELETE /api/proxies/:id`、`POST /api/proxies/:id/status` |

## 开发

```bash
# 1. 启动后端（项目根目录，默认 127.0.0.1:8000）
go run ./cmd/server -config config/config.yaml

# 2. 启动前端开发服务器（web/ 目录，端口 5173，/api 自动代理到 8000）
cd web
pnpm install
pnpm dev
```

打开 http://127.0.0.1:5173 。

## 生产模式（单进程）

```bash
cd web && pnpm build          # 产出 web/dist
cd .. && go run ./cmd/server  # Go 服务自动挂载 web/dist
```

打开 http://127.0.0.1:8000 即可：API 走 `/api/*`，其余路径由 React Router 接管（SPA fallback 到 `index.html`）。静态目录可用环境变量 `GPT_GO_WEB_DIST` 覆盖。

## 技术栈

- React 19 + react-router-dom 7
- Tailwind CSS v4（`@tailwindcss/vite`）
- shadcn/ui（radix-nova preset）：button / card / table / dialog / alert-dialog / dropdown-menu / select / tabs / checkbox / switch / textarea / badge / skeleton / sonner / tooltip / scroll-area / popover / separator / sheet / input / label
- @tanstack/react-query：请求缓存、轮询（health 30s）、变更后自动失效
- lucide-react 图标；深浅色主题（localStorage 持久化）
