# otp — 通用「取邮箱验证码」Provider 体系（仅邮箱，已剔除 SMS）

> **一句话**：任何需要「从邮箱读验证码」的模块（注册 / 已有账号登录 / 改密 / 改绑邮箱），
> 都 import 这个包 + 注册对应 provider，**不要各写一份取码/防误判逻辑**。
> 对齐 codex-auto `mail_providers/base.py` 的三层设计。

---

## 1. 为什么独立成包

「取验证码」是**横切关注点**：注册要、登录要、改密要、改绑也要。
如果不抽象，每个业务各写一份「轮询收件箱 + 正则抠 6 位码」，规则还不一致
（codex-auto 血泪：mail_outlook 和 mail_cf 曾各写一份防误判，行为漂移）。

收敛到 `signup/otp`：**加一种新邮箱 = 新建 1 个文件 + 注册表加 1 行，调用方零改动。**

---

## 2. 三层架构

```
otp.Provider（接口）        ← 调用方只认这个
  ├── Caps()               能力声明（Kind/Pooled/Ephemeral）
  ├── WaitForOTP(ctx,email,timeout,issuedAfter)  阻塞取码（带防串号时间窗）
  ├── PeekOTP(...)          非破坏性预读（省一封重复码）
  ├── CreateMailbox()       返回本次用的邮箱（Ephemeral provider 用）
  ├── MarkDead()/Exhausted() 号池语义（Pooled provider 用）
  ↓
otp.Register(kind, Factory)  ← 各 provider 在 init() 注册
otp.New(ctx, kind, settings, account)  ← 唯一构造入口（未知 kind 报致命错）
  ↓
具体 provider：
  └── mailcode.go  自建 Mailcow + mailcode-api（用号池的 accessUrl 轮询读信）
```

---

## 3. 关键设计（都是血泪教训，务必理解）

### 3.1 `issuedAfter` 防串号时间窗 —— 最重要
`WaitForOTP(ctx, email, timeout, issuedAfterUnix)` **只接受 issuedAfter 之后到达的邮件**，
避免并发注册时读到上一轮遗留的旧验证码。

- **发码前取时间戳**（`otpSentAt := nowUnix()` 在 `sendOTP()` 之前）——否则 `otpSentAt`
  晚于邮件到达时间，会被 `issuedAfter` 过滤掉，永远收不到。
- register_password 后服务端切流程，旧 OTP 立即失效，必须以**新发码时间**为准。

### 3.2 `PeekOTP` 非破坏性预读 —— 省一封重复码
`get_auth_url` 带 login_hint 时，OpenAI 会**抢跑发码**（比正式提交早 20 秒），
一轮能收 3 封**码完全一样**的信。先 peek 命中就不用再发。

**三条铁律**：① 不阻塞（wait=0 打一次就走）② 拿不到返回 `"",nil`（不抛异常）
③ **非破坏性**（看过的邮件不能记进 seen，否则紧接着的 WaitForOTP 就再也看不见）。

### 3.3 `extract_otp` 防误判正则
共用一套（`ExtractOTP`）：优先 `<span>6位</span>` → 跳过 MIME header →
剔除邮箱地址 / 时间戳 / hex 颜色 → 兜底匹配 6 位数字。

### 3.4 `Pooled` × `Ephemeral` 两个正交能力维度
决定调用方的 fast-fail / mark_dead / 换号行为（见 provider.go 包注释的能力维度表）。
**iCloud 失败根因**：`Pooled=false + Ephemeral=false` → OpenAI 当老号要密码 → 401。

---

## 4. 怎么用（其他模块接入示例）

```go
import "gpt-go/internal/service/signup/otp"

// 方式一：注册表工厂（推荐，按号池里存的 kind 构造）
provider, err := otp.New(ctx, "mailcode",
    map[string]any{"baseURL": "https://mail.example.com"},   // settings
    map[string]any{"email": "a@x.com", "accessUrl": "..."},  // 号池记录
)
if err != nil { /* 未知 kind 或配置错 */ }

// 方式二：直接由 accessUrl 构造（号池记录里有 accessUrl 时最简）
provider := otp.NewMailcodeProvider(emailRecord.AccessURL)

// 取码（发码前先取时间戳）
sentAt := time.Now().Unix()
sendOTPEmail(email)                       // 你的发码动作
code, err := provider.WaitForOTP(ctx, email, 60, sentAt)
if otp.IsFatal(err) {
    // 号废了（凭据失效/被封）→ 标记失败，换下一个
} else if err != nil {
    // 超时/网络问题 → 号是无辜的，可放回池子
}
```

---

## 5. 加一种新邮箱（如 Outlook / Gmail IMAP）

1. 新建 `otp/outlook.go`，实现 `otp.Provider` 接口（至少 `Caps` + `WaitForOTP`）。
2. 文件里写 `func init() { otp.Register("outlook", newOutlookFromConfig) }`。
3. 调用方零改动，`otp.New(ctx, "outlook", ...)` 即可用。

参考 `mailcode.go` 的完整实现。

---

## 6. 已实现 / 待扩展

| provider | 状态 | 说明 |
|---|---|---|
| `mailcode` | ✅ 已实现 | 自建 Mailcow + mailcode-api，accessUrl 轮询 |
| `outlook` / `gmail` / `cf_temp` / `imap` | ⏳ 待扩展 | 按 §5 加文件即可，调用方不用改 |

**已剔除 SMS**：本包只做邮箱验证码。短信接码（add-phone 风控）若未来需要，
参照本包模式另建 `signup/sms`，不与邮箱 OTP 合并（codex-auto 也是两套独立体系）。
