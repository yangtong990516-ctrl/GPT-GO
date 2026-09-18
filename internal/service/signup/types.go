package signup

// AuthResult 是认证结果，对齐 Python AuthResult（auth_flow.py）。
// 字段与 Python to_dict() 一一对应。
type AuthResult struct {
	Email            string `json:"email"`
	Password         string `json:"password"`
	SessionToken     string `json:"session_token"`
	AccessToken      string `json:"access_token"`
	DeviceID         string `json:"device_id"`
	CSRFToken        string `json:"csrf_token"`
	IDToken          string `json:"id_token"`
	RefreshToken     string `json:"refresh_token"`
	CookieHeader     string `json:"cookie_header"`
	TotpSecret       string `json:"totp_secret"`
	TotpFactorID     string `json:"totp_factor_id"`
	ExitIP           string `json:"exit_ip"`
	AddPhoneRequired bool   `json:"add_phone_required"`
}

// IsValid 对齐 Python AuthResult.is_valid()：session_token 且 access_token 都非空。
func (r *AuthResult) IsValid() bool {
	return r.SessionToken != "" && r.AccessToken != ""
}

// RegistrationResult 是 run_protocol_registration 的成功返回，对齐 Python 返回 dict。
type RegistrationResult struct {
	OK                bool   `json:"ok"`
	AccountID         string `json:"account_id"`
	Email             string `json:"email"`
	Password          string `json:"password"`
	AccessToken       string `json:"access_token"`
	RefreshToken      string `json:"refresh_token"`
	DeviceID          string `json:"device_id"`
	SessionToken      string `json:"session_token"`
	CookieHeader      string `json:"cookie_header"`
	TotpSecret        string `json:"totp_secret"`
	PlusTrialEligible *bool  `json:"plus_trial_eligible,omitempty"`
	ProxyID           string `json:"proxy_id"`
	ProxyCountry      string `json:"proxy_country,omitempty"`

	// 失败/拒绝字段
	Rejected string `json:"rejected,omitempty"` // 如 "not_trial"
	Reason   string `json:"reason,omitempty"`
}

// BatchResult 是 run_protocol_batch 的返回，对齐 Python 返回 dict。
type BatchResult struct {
	OK        int               `json:"ok"`
	Failed    int               `json:"failed"`
	Cancelled int               `json:"cancelled"`
	Results   []BatchItemResult `json:"results"`
	Errors    []BatchItemResult `json:"errors"`
}

// BatchItemResult 是单个账号的批处理结果，对齐 Python worker 返回 dict。
type BatchItemResult struct {
	OK        bool   `json:"ok"`
	Index     int    `json:"index"`
	Code      string `json:"code"`
	Error     string `json:"error,omitempty"`
	Email     string `json:"email,omitempty"`
	Cancelled bool   `json:"cancelled,omitempty"`
}
