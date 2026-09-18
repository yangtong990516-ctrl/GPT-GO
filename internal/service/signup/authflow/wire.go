// wire.go 装配层：把 core 地基、会话型拨号、sentinel(V8)、落库拼成 signup.Service 可用的
// FlowRunner。单独成文件是为了让 signup 根包不 import authflow（解包循环），由 main/装配层
// 调本包的 NewFlowRunner 得到 FlowRunner 注入 signup.Service。
//
// 关键对齐点：
//   - sentinel challenge 请求【必须与 TLS 会话同源】（否则 JA3/UA 与 sdk.js 画像不一致被风控）。
//     故用本文件的 coreChallengeFetcher——拿 authflow 的 core.Session 发 /sentinel/req，
//     与主线会话/指纹/cookie 同源。
package authflow

import (
	"context"
	"encoding/json"
	"fmt"

	"gpt-go/internal/service/signup"
	"gpt-go/internal/service/signup/core"
	"gpt-go/internal/service/signup/sentinel"
)

// NewFlowRunner 返回一个 signup.FlowRunner：拨号结果 → core.Bootstrap → authflow.RunRegister。
//
// solver 是 V8 求解器（P4 搞定后传入）；可为 nil（则 sentinel 步骤返回明确错误）。
// 这个函数由装配层调用，返回值注入 signup.Service（WithFlowRunner）。
func NewFlowRunner(solver sentinel.Solver) signup.FlowRunner {
	return func(ctx context.Context, req signup.FlowRequest) (*signup.AuthResultLike, error) {
		// solver 优先用 req 里带的（service 层注入），否则用装配时闭包的。
		sv := req.Solver
		if sv == nil {
			sv = solver
		}
		cfg := &signup.Config{Proxy: req.ProxyURL}

		// 用拨号实测的出口 IP 创建（core 立即 GeoIP 解国家+时区，国家动态实测）。
		flow, err := NewWithEgress(cfg, req.EgressIP, WithSolver(sv))
		if err != nil {
			return nil, err
		}
		defer flow.Close()

		// 环境指纹回传(供 service 层写 runlog):Identity 的 UA/Preset/AcceptLang/时区/国家,
		// 验证目标环境(如 JP)是否配对。GeoIP 实测国家/时区在 Bootstrap 装配时已绑定。
		if req.OnEnv != nil {
			id := flow.Boot().Identity
			env := map[string]any{
				"preset":      id.PresetName,
				"userAgent":   id.UserAgent,
				"acceptLang":  id.AcceptLang,
				"timezone":    id.Timezone,
				"country":     id.CountryCode,
				"egressIP":    req.EgressIP,
				"screen":      id.Screen,
				"browserType": string(id.Family),
			}
			if g := flow.Boot().Geo; g != nil {
				env["geoCountry"] = g.CountryCode
				env["geoTimezone"] = g.TimezoneID
			}
			req.OnEnv(env)
		}

		// 关键：把 core.Session 包成 ChallengeFetcher 注入 solver（challenge 与 TLS 同源）。
		// 若 solver 支持 SetFetcher/WithFetcher 则注入；否则 solver 用其默认（见 NOTE 待办）。
		InjectCoreFetcher(sv, flow.Boot().Session)

		res, err := flow.RunRegister(ctx, req.Email, req.Password, req.Mail, req.OTPTimeout)
		if err != nil {
			return nil, err
		}
		return &signup.AuthResultLike{
			Email:        res.Email,
			Password:     res.Password,
			AccessToken:  res.AccessToken,
			RefreshToken: res.RefreshToken,
			SessionToken: res.SessionToken,
			DeviceID:     res.DeviceID,
			CookieHeader: res.CookieHeader,
			TotpSecret:   res.TotpSecret,
		}, nil
	}
}

// coreChallengeFetcher 用 core.Session 发 /sentinel/req（challenge 与 TLS 会话同源）。
//
// 这是 TLS↔UA↔SO 三路自洽的延伸：challenge 请求复用注册同一 TLS 会话与 cookie jar，
// 避免「sdk.js 画像是 chrome-148、challenge 请求却是另一个 TLS 指纹」的裂缝。
type coreChallengeFetcher struct {
	sess *core.Session
}

// fetch 实现 sentinel.ChallengeFetcher 签名。
func (cf *coreChallengeFetcher) fetch(ctx context.Context, env sentinel.EnvPayload, flow sentinel.Flow, requestP string) (*sentinel.Challenge, error) {
	body := map[string]string{"p": requestP, "id": env.DeviceID, "flow": string(flow)}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	// challenge 是 sentinel.openai.com 域的 XHR（text/plain 提交，对齐实测）。
	extra := map[string]string{
		"Origin":  "https://sentinel.openai.com",
		"Referer": "https://sentinel.openai.com/backend-api/sentinel/frame.html?sv=" + sentinel.Version,
	}
	resp, err := cf.sess.PostWithHeaders(ctx, sentinel.ReqURL,
		"https://sentinel.openai.com/backend-api/sentinel/frame.html?sv="+sentinel.Version,
		"text/plain;charset=UTF-8", payload, extra)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("/sentinel/req HTTP %d", resp.StatusCode)
	}
	raw := resp.Bytes()
	var ch sentinel.Challenge
	if err := json.Unmarshal(raw, &ch); err != nil {
		return nil, fmt.Errorf("challenge 响应不是 JSON: %w", err)
	}
	ch.Raw = raw
	return &ch, nil
}

// InjectCoreFetcher 把 coreChallengeFetcher 注入 solver。
//
// 通过接口断言：若 solver 提供 SetFetcher(sentinel.ChallengeFetcher)，则注入 core 会话的
// fetcher（与主线 TLS 同源）；否则静默跳过——solver 必须实现 SetFetcher 才能保证对齐，
// V8Solver 已实现（见 sentinel/v8_solver.go）。
//
// 导出原因：除注册 FlowRunner 外，补 2FA（accountsecurity）也要在自建 Flow 上注入
// 同一 core 会话 fetcher，保证 sentinel challenge 与主线 TLS 同源。
func InjectCoreFetcher(solver sentinel.Solver, sess *core.Session) {
	if solver == nil || sess == nil {
		return
	}
	cf := &coreChallengeFetcher{sess: sess}
	type fetcherSetter interface {
		SetFetcher(sentinel.ChallengeFetcher)
	}
	if fs, ok := solver.(fetcherSetter); ok {
		fs.SetFetcher(cf.fetch)
	}
}

// 静态保证：coreChallengeFetcher.fetch 符合 sentinel.ChallengeFetcher 签名。
var _ = sentinel.ChallengeFetcher((&coreChallengeFetcher{}).fetch)
