// Package stats implements the global stats overview, mirroring
// resource_service.overview_stats (resource_service.py:2353) and
// GET /api/stats/overview.
package stats

import (
	"context"
	"time"

	"gpt-go/internal/model"
	"gpt-go/internal/store"
)

// Service computes the aggregate account/email/proxy statistics.
type Service struct {
	accounts store.AccountStore
	emails   store.EmailStore
	proxies  store.ProxyStore
}

// NewService returns a stats Service.
func NewService(accounts store.AccountStore, emails store.EmailStore, proxies store.ProxyStore) *Service {
	return &Service{accounts: accounts, emails: emails, proxies: proxies}
}

// Overview returns the aggregate statistics (mirrors overview_stats).
func (s *Service) Overview(ctx context.Context) (model.OverviewStats, error) {
	today := time.Now().UTC().Truncate(24 * time.Hour)

	// Account stats.
	accountsTotal, err := s.accounts.Count(ctx, nil)
	if err != nil {
		return model.OverviewStats{}, err
	}
	accountsToday, err := s.accounts.Count(ctx, func(d store.AccountDocument) bool {
		return !d.CreatedAt.Before(today)
	})
	if err != nil {
		return model.OverviewStats{}, err
	}
	totpComplete, err := s.accounts.Count(ctx, func(d store.AccountDocument) bool {
		return d.TotpSecret != ""
	})
	if err != nil {
		return model.OverviewStats{}, err
	}
	plusTotal, err := s.accounts.Count(ctx, func(d store.AccountDocument) bool {
		return d.AccountType == "plus"
	})
	if err != nil {
		return model.OverviewStats{}, err
	}
	plusBound, err := s.accounts.Count(ctx, func(d store.AccountDocument) bool {
		return d.AccountType == "plus" && d.PhoneBound != nil && *d.PhoneBound
	})
	if err != nil {
		return model.OverviewStats{}, err
	}
	freeTotal, err := s.accounts.Count(ctx, func(d store.AccountDocument) bool {
		return d.AccountType == "free"
	})
	if err != nil {
		return model.OverviewStats{}, err
	}
	freeEligible, err := s.accounts.Count(ctx, func(d store.AccountDocument) bool {
		return d.AccountType == "free" && d.PromotionEligible != nil && *d.PromotionEligible
	})
	if err != nil {
		return model.OverviewStats{}, err
	}

	// 验活状态分布（GPT-GO 扩展，为前端大盘）：alive/dead/unknown/running/unchecked。
	aliveStatusIs := func(want string) func(store.AccountDocument) bool {
		return func(d store.AccountDocument) bool {
			return d.AliveStatus != nil && *d.AliveStatus == want
		}
	}
	aliveStats := model.AliveStats{}
	if aliveStats.Alive, err = s.accounts.Count(ctx, aliveStatusIs(model.AliveAlive)); err != nil {
		return model.OverviewStats{}, err
	}
	if aliveStats.Dead, err = s.accounts.Count(ctx, aliveStatusIs(model.AliveDead)); err != nil {
		return model.OverviewStats{}, err
	}
	if aliveStats.Unknown, err = s.accounts.Count(ctx, aliveStatusIs(model.AliveUnknown)); err != nil {
		return model.OverviewStats{}, err
	}
	if aliveStats.Running, err = s.accounts.Count(ctx, aliveStatusIs(model.AliveRunning)); err != nil {
		return model.OverviewStats{}, err
	}
	// unchecked = 总数 − 已检测各态（aliveStatus 为空/缺失/未识别）。
	aliveStats.Unchecked = accountsTotal - aliveStats.Alive - aliveStats.Dead - aliveStats.Unknown - aliveStats.Running
	if aliveStats.Unchecked < 0 {
		aliveStats.Unchecked = 0
	}

	// Registered emails (for available exclusion).
	registered, err := s.emails.RegisteredEmails(ctx)
	if err != nil {
		return model.OverviewStats{}, err
	}
	excluded := map[string]bool{}
	for _, e := range registered {
		excluded[e] = true
	}

	// Email stats: available is per-source, excluding registered accounts.
	available := func(source string) (int, error) {
		return s.emails.Count(ctx, func(d store.EmailDocument) bool {
			if d.Status != "available" {
				return false
			}
			if excluded[d.EmailNormalized] {
				return false
			}
			return sourceMatches(source, d.SourceType)
		})
	}
	emailsAvailable, err := available("all")
	if err != nil {
		return model.OverviewStats{}, err
	}
	emailAliases, err := available("mailcom_alias")
	if err != nil {
		return model.OverviewStats{}, err
	}
	emailsMailcode, err := available("mailcode")
	if err != nil {
		return model.OverviewStats{}, err
	}
	emailsRemail, err := available("remail")
	if err != nil {
		return model.OverviewStats{}, err
	}
	emailsReserved, err := s.emails.Count(ctx, func(d store.EmailDocument) bool { return d.Status == "reserved" })
	if err != nil {
		return model.OverviewStats{}, err
	}
	emailsFailed, err := s.emails.Count(ctx, func(d store.EmailDocument) bool { return d.Status == "failed" })
	if err != nil {
		return model.OverviewStats{}, err
	}
	emailsQuarantined, err := s.emails.Count(ctx, func(d store.EmailDocument) bool { return d.Status == "quarantined" })
	if err != nil {
		return model.OverviewStats{}, err
	}

	// Proxy stats.
	proxiesTotal, err := s.proxies.Count(ctx, nil)
	if err != nil {
		return model.OverviewStats{}, err
	}
	proxiesEnabled, err := s.proxies.Count(ctx, func(d model.ProxyDocument) bool { return d.Enabled })
	if err != nil {
		return model.OverviewStats{}, err
	}
	proxiesAvailable, err := s.proxies.Count(ctx, func(d model.ProxyDocument) bool { return d.Status == model.ProxyStatusAvailable })
	if err != nil {
		return model.OverviewStats{}, err
	}
	proxiesUsed, err := s.proxies.Count(ctx, func(d model.ProxyDocument) bool { return d.Status == model.ProxyStatusUsed })
	if err != nil {
		return model.OverviewStats{}, err
	}
	proxiesQuarantined, err := s.proxies.Count(ctx, func(d model.ProxyDocument) bool { return d.Status == model.ProxyStatusQuarantined })
	if err != nil {
		return model.OverviewStats{}, err
	}

	return model.OverviewStats{
		Accounts: model.AccountStats{
			Total:        accountsTotal,
			Today:        accountsToday,
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
			PlusRatio: percent1(plusTotal, accountsTotal),
			Alive:     aliveStats,
		},
		Emails: model.EmailStats{
			Available:   emailsAvailable,
			Reserved:    emailsReserved,
			Failed:      emailsFailed,
			Quarantined: emailsQuarantined,
			Aliases:     emailAliases,
			Mailcode:    emailsMailcode,
			Remail:      emailsRemail,
		},
		Proxies: model.ProxyStats{
			Total:       proxiesTotal,
			Enabled:     proxiesEnabled,
			Available:   proxiesAvailable,
			Used:        proxiesUsed,
			Quarantined: proxiesQuarantined,
		},
	}, nil
}

// sourceMatches mirrors email_source_filter (resource_service.py:185).
func sourceMatches(source, sourceType string) bool {
	switch source {
	case "mailcom_alias":
		return sourceType == "mailcom_alias"
	case "mailcode":
		return sourceType == "mailcode"
	case "remail":
		return sourceType == "remail"
	case "standard":
		switch sourceType {
		case "mailcom_alias", "mailcode":
			return false
		}
		return true
	default:
		return true
	}
}

// percent1 计算 part/total*100 保留 1 位小数（total=0 → 0），对齐 codex round(...,1)。
func percent1(part, total int) float64 {
	if total <= 0 {
		return 0
	}
	return float64(int(float64(part)*100/float64(total)*10+0.5)) / 10
}
