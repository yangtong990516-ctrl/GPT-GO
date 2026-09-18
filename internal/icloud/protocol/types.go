package protocol

import (
	"errors"

	"gpt-go/internal/icloud/domain"
)

type ICloudSession = domain.ICloudSession
type LoginState = domain.LoginState
type SessionCookie = domain.SessionCookie
type Mailbox = domain.Mailbox

const (
	LoginStateICloudWeb    = domain.LoginStateICloudWeb
	LoginStateAppleAccount = domain.LoginStateAppleAccount
	LoginStateICloudIMAP   = domain.LoginStateICloudIMAP
)

type Error struct {
	code      string
	message   string
	retryable bool
}

func (e Error) Error() string {
	return e.message
}

type codedError = Error

func errCode(code, message string, retryable bool) error {
	return Error{code: code, message: message, retryable: retryable}
}

func ErrorDetails(err error) (code, message string, retryable bool) {
	var coded Error
	if errors.As(err, &coded) {
		return coded.code, coded.message, coded.retryable
	}
	if err == nil {
		return "", "", false
	}
	return "protocol_error", err.Error(), false
}
