# totp — RFC 6238 TOTP / RFC 4226 HOTP（已有账号 2FA 用）

> 纯算法、无网络依赖、Go 标准库实现（`crypto/hmac`+`sha1`+`base32`+`binary`）。
> 用于已有账号登录命中 `mfa_challenge`（两步验证）时现算 6 位动态码。
> 对齐 codex-auto `auth_flow.py` 的 `_hotp`/`_totp_now`。

---

## 用法

```go
import "gpt-go/internal/service/signup/totp"

// 最常用：当前 30 秒窗口的 6 位码（mfa-challenge 提交用）
code, err := totp.Now(totpSecret)

// 指定时间（测试/对齐 RFC 向量用）
code, err := totp.NowAt(totpSecret, timeUnix)

// 完整控制（自定义窗口/位数）
code, err := totp.TOTP(totpSecret, timeUnix, 30, 6)
code, err := totp.HOTP(totpSecret, counter, 6)
```

- `totpSecret`：Base32 编码密钥（容忍小写 / 空格 / 缺 padding，自动规范化）。
- 出错返回 error（空密钥 / Base32 解码失败），**不静默产出错码**。

## 正确性印证（重要）

算法用 **RFC 官方测试向量**验证，不是只对拍「自己能跑」：

- `TestHOTP_RFC4226`：RFC 4226 Appendix D 的 counter 0..9 → 6 位码标准答案。
- `TestTOTP_RFC6238`：RFC 6238 Appendix B 的 5 个时间戳 → 8 位码标准答案（SHA1）。

**TOTP 算错一位 2FA 就过不了**，所以必须对拍跨实现的公共基准（RFC 向量），
这两个测试就是硬保证。

## 与登录链的集成

`authflow.submitMfaTOTPWithSecret(ctx, continueURL)` 内部调用 `totp.Now(secret)`：
从 continue_url 提取 challenge_id → 算 TOTP 码 → POST `/api/accounts/mfa/verify`。
登录状态机（`RunLogin`）在密码验证或 OTP 验证后命中 `mfa_challenge` 时自动调用。
