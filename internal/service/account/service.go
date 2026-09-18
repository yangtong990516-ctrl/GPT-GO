// Package account implements the account-pool business logic (basic information
// subset), mirroring reference resource_service.py account methods 1:1.
package account

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"regexp"
	"strings"
	"time"

	"gpt-go/internal/model"
	emailsvc "gpt-go/internal/service/email"
	"gpt-go/internal/store"
	"gpt-go/internal/util"
)

// Service mirrors the account methods of ResourceService.
type Service struct {
	store store.AccountStore
}

// NewService returns an account Service.
func NewService(s store.AccountStore) *Service {
	return &Service{store: s}
}

var countryCodeRe = regexp.MustCompile(`^[A-Z]{2}$`)

// normalizeCountryCode mirrors normalize_country_code (resource_service.py:251).
func normalizeCountryCode(value string) string {
	v := strings.ToUpper(strings.TrimSpace(value))
	if countryCodeRe.MatchString(v) {
		return v
	}
	return "ZZ"
}

// List mirrors list_accounts (resource_service.py:476).
func (s *Service) List(ctx context.Context, f model.AccountListFilter, page, pageSize int) (model.AccountPage, error) {
	query := store.AccountQuery{
		Query:         strings.ToLower(strings.TrimSpace(f.Query)),
		Promotion:     f.Promotion,
		Alive:         f.Alive,
		Payment:       strings.ToLower(strings.TrimSpace(f.Payment)),
		ZeroPayment:   strings.ToLower(strings.TrimSpace(f.ZeroPayment)),
		PaymentStatus: strings.ToLower(strings.TrimSpace(f.PaymentStatus)),
	}
	// country: only normalize when non-empty (mirrors `if country.strip()`).
	if strings.TrimSpace(f.Country) != "" {
		query.Country = normalizeCountryCode(f.Country)
	}
	docs, total, err := s.store.List(ctx, query, page, pageSize)
	if err != nil {
		return model.AccountPage{}, err
	}
	items := make([]model.AccountRecord, 0, len(docs))
	for _, d := range docs {
		items = append(items, accountRecord(d))
	}
	return model.AccountPage{
		Items:    items,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

// Create mirrors create_account (resource_service.py:619).
func (s *Service) Create(ctx context.Context, in model.AccountCreate) (model.AccountRecord, error) {
	now := util.NowUTC()
	var country *string
	if in.RegistrationCountry != nil && strings.TrimSpace(*in.RegistrationCountry) != "" {
		c := normalizeCountryCode(*in.RegistrationCountry)
		country = &c
	}
	doc := store.AccountDocument{
		ID:                  newID(),
		Email:               strings.ToLower(strings.TrimSpace(in.Email)),
		EmailNormalized:     normalizeEmail(in.Email),
		ChatgptPassword:     in.ChatgptPassword,
		TotpSecret:          in.TotpSecret,
		EmailAccessURL:      in.EmailAccessURL,
		CreatedAt:           now,
		AccountType:         in.AccountType,
		PhoneBound:          in.PhoneBound,
		PromotionEligible:   in.PromotionEligible,
		RegistrationCountry: country,
		AccessTokenMissing:  false,
	}
	if in.AccountType == "" {
		doc.AccountType = model.AccountTypeFree
	}
	// 注册产物 token：落库 accessToken + accessTokenConfigured + 过期/更新时间
	// （对齐 codex store_account_access_token）。套餐检查/验活依赖 accessToken 非空。
	if tok := strings.TrimSpace(in.AccessToken); tok != "" {
		doc.AccessToken = tok
		doc.AccessTokenConfigured = true
		doc.AccessTokenUpdatedAt = &now
		doc.AccessTokenExpiresAt = in.AccessTokenExpiresAt
	}
	if in.RefreshToken != nil && strings.TrimSpace(*in.RefreshToken) != "" {
		rt := strings.TrimSpace(*in.RefreshToken)
		doc.RefreshToken = &rt
	}
	// token-heal 凭证:session_token + cookie_header(注册时落库,后续续期/验活用)。
	if st := strings.TrimSpace(in.SessionToken); st != "" {
		doc.SessionToken = st
		doc.SessionUpdatedAt = &now
	}
	if ch := strings.TrimSpace(in.CookieHeader); ch != "" {
		doc.CookieHeader = ch
	}
	inserted, err := s.store.Create(ctx, doc)
	if err != nil {
		return model.AccountRecord{}, err
	}
	if !inserted {
		return model.AccountRecord{}, util.DuplicateResource("账号已存在：" + doc.Email)
	}
	return accountRecord(doc), nil
}

// Delete mirrors delete_accounts (resource_service.py:661).
func (s *Service) Delete(ctx context.Context, ids []string) (model.DeleteResult, error) {
	n, err := s.store.Delete(ctx, ids)
	if err != nil {
		return model.DeleteResult{}, err
	}
	return model.DeleteResult{Deleted: n}, nil
}

// Stats mirrors overview_stats' account block (resource_service.py:2353-2434).
// 只统计账号维度；emails/proxies 块由对应模块后续补，本轮返回零值。
func (s *Service) Stats(ctx context.Context) (model.AccountStats, error) {
	today := util.NowUTC()
	today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)

	total, err := s.store.Count(ctx, nil)
	if err != nil {
		return model.AccountStats{}, err
	}
	todayCount, err := s.store.Count(ctx, func(d store.AccountDocument) bool {
		return !d.CreatedAt.Before(today)
	})
	if err != nil {
		return model.AccountStats{}, err
	}
	totpComplete, err := s.store.Count(ctx, func(d store.AccountDocument) bool {
		return strings.TrimSpace(d.TotpSecret) != ""
	})
	if err != nil {
		return model.AccountStats{}, err
	}
	plusTotal, err := s.store.Count(ctx, func(d store.AccountDocument) bool {
		return d.AccountType == model.AccountTypePlus
	})
	if err != nil {
		return model.AccountStats{}, err
	}
	plusBound, err := s.store.Count(ctx, func(d store.AccountDocument) bool {
		return d.AccountType == model.AccountTypePlus && d.PhoneBound != nil && *d.PhoneBound
	})
	if err != nil {
		return model.AccountStats{}, err
	}
	freeTotal, err := s.store.Count(ctx, func(d store.AccountDocument) bool {
		return d.AccountType == model.AccountTypeFree
	})
	if err != nil {
		return model.AccountStats{}, err
	}
	freeEligible, err := s.store.Count(ctx, func(d store.AccountDocument) bool {
		return d.AccountType == model.AccountTypeFree && d.PromotionEligible != nil && *d.PromotionEligible
	})
	if err != nil {
		return model.AccountStats{}, err
	}

	return model.AccountStats{
		Total:        total,
		Today:        todayCount,
		TotpComplete: totpComplete,
		Plus: model.PlusStats{
			Total:   plusTotal,
			Bound:   plusBound,
			Unbound: plusTotal - plusBound,
		},
		Free: model.FreeStats{
			Total:      freeTotal,
			Eligible:   freeEligible,
			Ineligible: freeTotal - freeEligible,
		},
	}, nil
}

// accountRecord mirrors _account_record (resource_service.py:2455) for the
// basic-information subset.
func accountRecord(d store.AccountDocument) model.AccountRecord {
	campaigns := d.PromotionCampaigns
	if campaigns == nil {
		campaigns = []model.PromotionCampaign{}
	}
	return model.AccountRecord{
		ID:                    d.ID,
		Email:                 d.Email,
		ChatgptPassword:       d.ChatgptPassword,
		TotpSecret:            d.TotpSecret,
		TotpStatus:            d.TotpStatus,
		TotpSecretConfigured:  strings.TrimSpace(d.TotpSecret) != "",
		EmailAccessURL:        emailsvc.DirectMailboxAccessURL(d.EmailAccessURL, d.Email),
		CreatedAt:             d.CreatedAt,
		AccountType:           d.AccountType,
		PhoneBound:            d.PhoneBound,
		AccessTokenConfigured: d.AccessTokenConfigured,
		AccessTokenExpiresAt:  d.AccessTokenExpiresAt,
		AccessTokenUpdatedAt:  d.AccessTokenUpdatedAt,
		RefreshToken:          d.RefreshToken,
		AccessTokenMissing:    d.AccessTokenMissing,
		AtRefillStatus:        d.AtRefillStatus,
		AtRefillError:         d.AtRefillError,
		AtRefillErrorAt:       d.AtRefillErrorAt,
		PlanCheckStatus:       d.PlanCheckStatus,
		PlanCheckedAt:         d.PlanCheckedAt,
		PlanCheckErrorCode:    d.PlanCheckErrorCode,
		SubscriptionPlan:      d.SubscriptionPlan,
		PlanExpiresAt:         d.PlanExpiresAt,
		PromotionCampaigns:    campaigns,
		TrialScanCheckedAt:    d.TrialScanCheckedAt,
		TrialScanCountries:    d.TrialScanCountries,
		TrialScanError:        d.TrialScanError,
		RegistrationCountry:   d.RegistrationCountry,
		RegistrationIP:        d.RegistrationIP,
		RegistrationIsp:       d.RegistrationIsp,
		AliveStatus:           d.AliveStatus,
		AliveCheckedAt:        d.AliveCheckedAt,
		AliveErrorCode:        d.AliveErrorCode,
		AliveHTTPStatus:       d.AliveHTTPStatus,
		RebindStatus:          d.RebindStatus,
		Remark:                d.Remark,
		PromotionEligible:     d.PromotionEligible,
		PaymentStatus:         d.PaymentStatus,
		PaymentMethods:        nonNilStrings(d.PaymentMethods),
		PaymentZeroMethods:    nonNilStrings(d.PaymentZeroMethods),
		PaymentCheckedAt:      d.PaymentCheckedAt,
		PaymentRoutes:         d.PaymentRoutes,
		SessionToken:          d.SessionToken,
		CookieHeader:          d.CookieHeader,
		SessionUpdatedAt:      d.SessionUpdatedAt,
	}
}

// nonNilStrings 保证 JSON 输出为 [] 而非 null（前端 paymentMethods 恒为数组）。
func nonNilStrings(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}

// normalizeEmail mirrors normalize_email (resource_service.py:148).
func normalizeEmail(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

// newID returns a random 32-hex-char id (mirrors uuid4).
func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
