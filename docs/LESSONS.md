# 经验教训登记（LESSONS）

> 记录本项目开发中因「未先读当前代码就凭记忆/旧印象推断」而导致的错误，
> 作为硬性工作规则，防止同类问题复发。**每次动手前先读代码，不凭记忆下结论。**

---

## L-001（2026-09，必须遵守）

**错误**：在装配 OTP 取件链路时，我凭「邮箱池记录可能缺 pollUrl」的旧记忆，
断言「jsoncode 需要邮箱池记录带轮询 URL（pollUrl/accessUrl），现在可能不带」。

**事实**（后经查 GPT-GO 现有代码证实）：
- `internal/model/email.go` 的 `EmailRecord.AccessURL` 字段**一直存在**；
- 各邮箱源 service（mailcode / remail）在导入时**都会生成并填充** `AccessURL`
  （mailcode → `/api/mail?email=X`，remail → `/v1/pickup?...&token=Y`）；
- `resource_adapter.ReserveEmails` 也已把 `d.AccessURL` 填进 `ReservedEmail`。

**根因**：动手前**没有先读 GPT-GO 当前代码**（`model/email.go`、`service/email`、
`service/remail`、`service/mailcode` 的 AccessURL 生成），而是凭对「别的项目/
旧版本」的印象做推断，违背了「以当前项目事实为准」。

**后果**：给出错误结论，险些按错误前提去「补一个根本不缺的能力」，浪费一轮。

**规则（强制）**：
1. **任何关于「某能力/字段/接口是否存在」的论断，必须先 `grep`/`read` 当前代码确认，
   禁止凭记忆或对其他项目的印象下结论。**
2. 涉及「接入 / 是否已接入」的问题，先查：`internal/model/*`（数据模型）→
   `internal/store/*`（存储）→ `internal/service/*`（业务）→ `internal/apiserver/*`
   （装配/路由），四层都看过再回答。
3. 推断前先在回复里**引用代码证据**（文件+行），没有证据就不下「有/没有」的结论，
   改说「我需要先查 X 文件确认」。

**关联**：本条与早前「凭旧记忆低估 authflow 已有实现，被迫公开更正」是同一类问题
（未读最新代码）。两条合并为本规则，**再犯即视为流程违规**。
