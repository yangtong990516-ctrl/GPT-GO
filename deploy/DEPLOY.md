# GPT-GO 线上部署指南

本文档记录完整的环境要求与部署步骤，含本次实测踩过的坑，保证新服务器一次成功。

## 环境要求

| 依赖 | 版本 | 说明 |
|---|---|---|
| OS | Ubuntu 22.04 / Debian 系 (x86_64) | macOS 亦可(开发) |
| Go | **1.26+** | 后端语言 |
| Node.js | **20+** (npm 10+) | 前端构建 |
| MongoDB | 5+ | 存储，程序用**独立库**，可与同机其它项目共存 |
| **clang** | **19+** | ⚠️ Linux 编 v8go **必须**，见下方坑 |

### 一键准备

```bash
git clone https://github.com/yangtong990516-ctrl/GPT-GO.git /opt/gpt-go
cd /opt/gpt-go && bash deploy/server-setup.sh
```

脚本会装好 Go/Node/clang-19/git/make，并提示后续步骤。

## 部署步骤

```bash
cd /opt/gpt-go
cp config/config.example.yaml config/config.yaml
# 编辑 config.yaml:mongodb.uri / database / server.host / server.port

./start.sh          # 自动构建前端 + 后端(-tags v8)并后台启动
```

访问 `http://<server>:<port>`，默认控制台账号 `admin / 1024`(**第一时间改密码**)。

## ⚠️ 关键坑（本次实测）

### 1. Linux 编 v8go 必须用 clang-19+

- v8go 的 `deps/include_libcxx`(V8 内置 libc++)用到 `__builtin_clzg` / `__builtin_ctzg` 等内建，**clang-19 才引入**。
- Ubuntu 22.04 默认源最高 **clang-15**，编不过（报 `'_Tp' does not refer to a value` / `use of undeclared identifier '__builtin_clzg'`)。
- 解决：加 LLVM 官方源装 `clang-19`(server-setup.sh 已自动做)。
- **Makefile 已自动优先选 clang-19**，无需手动设 CC。

### 2. libv8.a 不随 go module 下发

- v8go(robomotionio 新版）把预编译静态库 `libv8.a`(~143MB）拆出，需单独下载到 `deps/{os}_{arch}/libv8.a`，否则链接报 `cannot find -lv8`。
- 解决：**Makefile 的 `fetch-libv8` 目标已自动下载**(build 时检测缺失就 fetch)，无需手动跑 `scripts/fetch-libv8.go`。

### 3. 与同机原项目隔离（重要）

本次与原版 `codex-auto` 同机共存，靠**三处隔离**互不影响：

| 项 | 原 codex-auto | GPT-GO |
|---|---|---|
| mongo 库 | `autoregister` | `gpt_go`(config.yaml `database`) |
| 端口 | `8000` | `8001`(config.yaml `server.port`) |
| 目录 | `/root/codex-auto-register-macos` | `/opt/gpt-go` |

改 `config.yaml` 的 `database` 和 `port` 即可实现任意隔离，互不动对方数据。

## 对外访问

### 方式 A：直接开端口（简单）

```bash
# config.yaml: server.host: 0.0.0.0, server.port: 8001
ufw allow 8001/tcp        # 或 iptables -I INPUT -p tcp --dport 8001 -j ACCEPT
./stop.sh && ./start.sh --no-build
```

访问 `http://<server>:8001`。**裸 IP+端口，无 HTTPS，仅限信任网络**。

### 方式 B：nginx 反代（推荐，可 HTTPS）

```nginx
server {
    listen 80;
    server_name gpt.example.com;
    location / {
        proxy_pass http://127.0.0.1:8001;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    }
}
# 配 443 + certbot 即可 HTTPS
```

## 常用运维

```bash
./start.sh          # 启动(自动构建,防重复/防孤儿)
./stop.sh           # 优雅停止
tail -f logs/server.log   # 看日志
git pull && ./stop.sh && ./start.sh   # 更新代码并重启(增量同步)
```

## 进程守护（可选，systemd)

```ini
# /etc/systemd/system/gpt-go.service
[Unit]
Description=GPT-GO
After=network.target mongod.service

[Service]
WorkingDirectory=/opt/gpt-go
ExecStart=/opt/gpt-go/bin/gpt-go-server -config config/config.yaml
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
```

`systemctl enable --now gpt-go`（用 systemd 时别再用 start.sh，二选一）。
