package protocol

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

type AuthFlow string

const (
	AuthFlowICloudWeb    AuthFlow = "icloud_web"
	AuthFlowAppleAccount AuthFlow = "apple_account"
)

type AuthStartResult struct {
	Session   ICloudSession `json:"-"`
	PendingID string        `json:"pending_id,omitempty"`
	Needs2FA  bool          `json:"needs_2fa"`
	Message   string        `json:"message"`
	AppleID   string        `json:"apple_id"`
	ExpiresAt time.Time     `json:"expires_at,omitempty"`
}

type AuthFacade struct {
	client  *AppleAuthClient
	pending *appleAuthPendingStore
}

func NewAuthFacade() *AuthFacade {
	return &AuthFacade{client: NewAppleAuthClient(), pending: newAppleAuthPendingStore()}
}

func (f *AuthFacade) Start(ctx context.Context, flow AuthFlow, appleID, password, defaultHost, clientID, twoFactorMethod string) (AuthStartResult, error) {
	var result appleAuthStartResult
	var err error
	switch flow {
	case AuthFlowAppleAccount:
		result, err = f.client.StartAppleAccountManageLogin(ctx, appleID, password, f.pending, twoFactorMethod)
	default:
		result, err = f.client.StartLogin(ctx, appleID, password, defaultHost, clientID, f.pending, twoFactorMethod)
	}
	if err != nil {
		return AuthStartResult{}, err
	}
	return AuthStartResult{
		Session:   result.Session,
		PendingID: result.PendingID,
		Needs2FA:  result.Needs2FA,
		Message:   result.Message,
		AppleID:   result.AppleID,
		ExpiresAt: result.ExpiresAt,
	}, nil
}

func (f *AuthFacade) Submit2FA(ctx context.Context, pendingID, code string, phoneNumber json.RawMessage) (ICloudSession, error) {
	pendingID = strings.TrimSpace(pendingID)
	pending, ok := f.pending.get(pendingID)
	if !ok {
		return ICloudSession{}, errCode("apple_login_pending_expired", "Apple 登录已过期，请重新输入账号密码发起登录", true)
	}
	var session ICloudSession
	var err error
	if pending.Session.isAppleAccountManage() {
		session, err = f.client.SubmitAppleAccountManage2FA(ctx, pending, code, phoneNumber)
	} else {
		session, err = f.client.Submit2FA(ctx, pending, code)
	}
	if err != nil {
		return ICloudSession{}, err
	}
	f.pending.delete(pendingID)
	return session, nil
}
