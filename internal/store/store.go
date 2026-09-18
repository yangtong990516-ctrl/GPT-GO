// Package store defines the email-pool storage contract and its mock
// implementation. The Mongo-backed implementation lands when MongoDB is set up
// locally via Docker (per project decision); until then the mock drives
// development and 1:1 behavior verification.
package store

import (
	"context"
	"time"

	"gpt-go/internal/util"
)

// EmailDocument is the persisted shape of an email record, mirroring the Mongo
// document written by resource_service.upsert_email / read by list_emails.
type EmailDocument struct {
	ID              string     `bson:"_id"`
	Email           string     `bson:"email"`
	EmailNormalized string     `bson:"emailNormalized"`
	AccessURL       string     `bson:"accessUrl"`
	ImportedAt      time.Time  `bson:"importedAt"`
	Status          string     `bson:"status"`
	MailboxKind     string     `bson:"mailboxKind"`
	SourceType      string     `bson:"sourceType"`
	RandomScore     int64      `bson:"randomScore"`
	ParentEmail     string     `bson:"parentEmail"`     // "" when absent
	MailboxPassword string     `bson:"mailboxPassword"` // "" when absent
	StatusReason    string     `bson:"statusReason"`    // "" when absent
	StatusUpdatedAt *time.Time `bson:"statusUpdatedAt"` // nil when absent
	ErrorCode       string     `bson:"errorCode"`       // "" when absent

	// ── 换绑（rebind 模块）预留/占用字段 ──
	// RebindReservedBy 预留归属（runID），防止同一邮箱被并发换绑任务重复分配。
	RebindReservedBy string `bson:"rebindReservedBy"`
	// ReservedAt 预留时间；卡死任务超过阈值后可被回收。
	ReservedAt *time.Time `bson:"reservedAt"`
	// UsagePurpose 占用用途（rebind / register / …），used 状态审计用。
	UsagePurpose string `bson:"usagePurpose"`
}

// EmailStore is the email-pool storage contract used by the service layer.
type EmailStore interface {
	// List filters and returns a page plus total count. ordered importedAt desc.
	List(ctx context.Context, q EmailQuery) ([]EmailDocument, int, error)
	// SetStatus updates status/statusReason/statusUpdatedAt/errorCode and
	// clears reservedBy/reservedAt. Returns the updated record or ErrNotFound.
	SetStatus(ctx context.Context, id, status string, statusReason string, statusUpdatedAt time.Time, errorCode string) (*EmailDocument, error)
	// ResetFailed resets status=="failed" to "available" (clears reservedBy/
	// reservedAt/statusReason). ids nil => all failed. Returns modified count.
	ResetFailed(ctx context.Context, ids []string) (int, error)
	// Delete removes documents by id. Returns deleted count.
	Delete(ctx context.Context, ids []string) (int, error)
	// Upsert inserts if emailNormalized absent; returns true if inserted.
	Upsert(ctx context.Context, d EmailDocument) (bool, error)
	// ListForExport returns available emails (excluded registered accounts),
	// ordered importedAt desc. ids nil => all available.
	ListForExport(ctx context.Context, ids []string) ([]EmailDocument, error)
	// RegisteredEmails returns distinct emailNormalized from accounts.
	RegisteredEmails(ctx context.Context) ([]string, error)
	// Count returns the number of documents matching pred (no implicit
	// registered-account exclusion; the caller decides).
	Count(ctx context.Context, pred func(EmailDocument) bool) (int, error)

	// ── 换绑（rebind 模块）──

	// ReserveForRebind 原子预留一个 available 邮箱给换绑任务：
	// 仅当 status==available 时置 status=reserved + rebindReservedBy=runID +
	// reservedAt=now，返回该邮箱；无可用邮箱返回 nil,nil（对齐 codex reserve）。
	ReserveForRebind(ctx context.Context, runID string) (*EmailDocument, error)
	// ReserveForRebindByID 按 ID 预留指定邮箱（前端配对模式）：
	// 仅当该邮箱 status==available 时置 reserved；已被预留/消耗返回 nil,nil。
	ReserveForRebindByID(ctx context.Context, emailID, runID string) (*EmailDocument, error)
	// ConsumeRebindEmail 换绑成功后消费预留邮箱：仅当 rebindReservedBy==runID
	// 时置 status=used + usagePurpose=rebind 并清预留字段；返回是否成功消费。
	ConsumeRebindEmail(ctx context.Context, emailID, runID string) (bool, error)
	// ReleaseRebindReservation 任务失败/取消时释放预留（status 回 available）；
	// 仅当 rebindReservedBy==runID 时生效，返回是否释放。
	ReleaseRebindReservation(ctx context.Context, emailID, runID string) (bool, error)
}

// EmailQuery captures list filters, mirroring list_emails parameters.
type EmailQuery struct {
	Status string // "" == all
	Source string // "" == all
	Query  string // search term (already normalized/lowercased by service)
	Page   int
	Size   int
}

// ErrNotFound is returned when a requested document does not exist.
var ErrNotFound = &StoreError{Kind: "not_found"}

// StoreError is a typed storage error.
type StoreError struct {
	Kind string
	Msg  string
}

func (e *StoreError) Error() string {
	if e.Msg != "" {
		return e.Msg
	}
	return e.Kind
}

// Unwrap lets util.WriteError map storage errors to HTTP responses via
// errors.As: not_found becomes 404 resource_not_found; anything else falls
// through to the generic 500.
func (e *StoreError) Unwrap() error {
	if e.Kind == "not_found" {
		return util.ResourceNotFound("邮箱不存在")
	}
	return nil
}
