# GPT-GO

ChatGPT 账号资产一站式本地管理控制台(Go + React + MongoDB)。

## 功能

- **注册运行**:批量注册 ChatGPT 账号(数量 / 目标国家 / 邮箱来源,实时并发与日志)
- **账号池**:录入、筛选、批量验活、批量查优惠资格、批量补 2FA+密码
- **支付检测**:按区域线路创建优惠 Checkout(只建单不付款),并发实时读取 0 元试用金额与可用支付渠道
- **邮箱池 / 邮箱换绑**:Outlook 等邮箱导入、取件、批量换绑
- **iCloud 隐私邮箱**:iCloud+ Hide My Email 开通与管理
- **代理池**:国家/分组管理、测速、按注册国自动租代理

## 技术栈

- 后端:Go 1.26(Gin + MongoDB Driver)
- 前端:React + Vite + TypeScript + shadcn/ui + react-query
- 存储:MongoDB(敏感字段 AES-GCM 加密)

## 快速开始

### 1. 依赖

- Go 1.26+
- Node.js 18+(前端构建)
- MongoDB 5+(本地 `mongodb://127.0.0.1:27017` 即可)

### 2. 配置

```bash
cp config/config.example.yaml config/config.yaml
# 按需编辑 config/config.yaml(主要是 mongodb.uri 和 server.host)
```

### 3. 启动 / 停止

```bash
./start.sh        # 构建前端+后端并后台启动(防重复/防孤儿)
./start.sh --fg   # 前台运行(调试)
./stop.sh         # 优雅停止
```

启动后访问 `http://127.0.0.1:8000`,默认控制台账号 `admin / 1024`(可在「系统设置」修改)。

### 手动构建(不用脚本时)

```bash
cd web && npm install && npm run build   # 前端 → web/dist
cd .. && go build -o bin/gpt-go-server ./cmd/server   # 后端
./bin/gpt-go-server -config config/config.yaml
```

## 部署到线上

1. `cp config/config.example.yaml config/config.yaml`
2. 改 `server.host: 0.0.0.0`(对外开放)和 `mongodb.uri`(真实库,可带认证)
3. **务必第一时间登录控制台改默认密码**,并配置防火墙只放行必要端口
4. `./start.sh` 启动;建议配 systemd / supervisor 守护

敏感数据(账号 token/密码/2FA)由程序自动生成 AES 密钥加密存入 MongoDB `metadata.secret_key`,配置文件无需管理任何密钥。

## 目录结构

```
cmd/server         入口
internal/
  apiserver/       HTTP 路由与各模块装配
  service/         业务逻辑(注册/账号/支付/邮箱/iCloud/代理…)
  store/           MongoDB 持久化(含内存 Mock 供测试)
  config/          配置加载
web/               React 前端(src 源码,dist 构建产物)
config/            配置文件(example 为模板)
start.sh/stop.sh   启停脚本(防孤儿)
```

## 环境变量(可选,覆盖配置)

| 变量 | 作用 |
|---|---|
| `GPT_GO_WEB_DIST` | 前端产物目录(默认 `web/dist`) |
| `GPT_GO_AUTH_DISABLED=1` | 关闭登录门禁(仅本地调试) |
| `GPT_GO_SEED_DEMO=1` | 启动灌入演示数据 |
| `GPT_GO_ICLOUD_DISABLE_BG=1` | 禁用 iCloud 后台任务 |
