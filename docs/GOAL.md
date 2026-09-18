# GOAL - GPT-GO 迁移目标

> 权威契约见 docs/CONTRACT.md。本文件记录目标、范围、验收条件；两者冲突时以 CONTRACT.md 为准。

## 1. 最终目标

把 codex-auto-register-macos/app/backend（Python/FastAPI）1:1 复刻为 Go 实现。
前后端分离（前端暂忽略），交付可构建、可测试、可运行、可部署的 Go 后端。

## 2. 范围

- In scope：Python 后端全部非废弃功能（REST API、WebSocket、配置、Mongo/文件存储、外部副作用）。
- Out of scope（用户明确）：
  - 前端（后续补充）。
  - 已废弃接口（HeroSMS/TigerSMS/VakSMS/SmsBower/payment-extractor/payment-probes 等），全部忽略，用户将翻新。
- 参考实现只读对照：/Users/iceman/Documents/workspace/codex-auto-register-macos/app/backend。

## 3. 兼容约束（不可改变）

- API 路径、方法、JSON 字段（camelCase）、null 语义、状态码、错误码与 Python 一致。
- 数据格式：Mongo 集合名/文档结构；settings/run-log 文件格式。
- 配置：YAML（见 CONTRACT 第 3 条）。

## 4. 用户明确要求（仅用户可改）

1. 新项目固定路径 /Users/iceman/Documents/workspace/GPT-GO，module gpt-go，路径不可漂移。
2. 废弃接口忽略。
3. 配置必须 YAML。
4. 每模块独立 package，包结构清晰；环境兼容本机 Go。
5. 通用能力（HTTP 请求、创建请求等）统一进工具类，禁止重复；工具类清单在 CONTRACT 第 5 条固定。
6. 1:1 复刻，不减少功能、不改语义、不夹带修复。
7. 每个迁移任务由用户决定；先验证 health 接口。

## 5. 暂定假设（非用户明确要求）

- A1. Web 框架 gin v1.10.0（本机 Go 1.24.13，gin v1.12 需 Go 1.25 以上）。
- A2. 监听 127.0.0.1:8000（对齐 Python）。
- A3. Mongo 驱动 go.mongodb.org/mongo-driver（后续任务引入）。
- A4. 工具类目录 internal/util/。

## 6. 验收条件

- 范围内功能全部迁移（无静默跳过）。
- /api/health 与 Python 契约逐字段一致（首个验证点）。
- go build / go test 通过；服务可启动并响应。
- 部署/配置/切换说明齐备。
- 未验证事项明确列出。
