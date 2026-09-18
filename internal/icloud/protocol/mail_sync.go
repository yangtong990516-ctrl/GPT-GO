package protocol

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"time"
)

type MailSyncMode string

const (
	MailSyncModeVerification MailSyncMode = "verification"
	MailSyncModeAllRecent    MailSyncMode = "all_recent"
)

type MailSyncOptions struct {
	Mode              MailSyncMode
	Keyword           string
	After             time.Time
	Limit             int
	FullScan          bool
	UseCursor         bool
	CursorUID         string
	CursorUIDValidity string
}

type MailSyncBatchResult struct {
	MessagesByMailbox map[string][]ICloudSyncedMessage
	UIDValidity       string
	LastUID           string
	Scanned           int
	Matched           int
	HasMore           bool
	InitializedCursor bool
}

func (options MailSyncOptions) VerificationOnly() bool {
	return options.Mode != MailSyncModeAllRecent
}

func canonicalMailID(messageID, from, subject string, receivedAt time.Time) string {
	messageID = strings.ToLower(strings.TrimSpace(messageID))
	messageID = strings.Trim(messageID, "<>")
	if messageID != "" {
		return "message-id:" + messageID
	}
	payload := strings.Join([]string{
		strings.ToLower(strings.TrimSpace(from)),
		strings.ToLower(strings.TrimSpace(subject)),
		receivedAt.UTC().Format(time.RFC3339),
	}, "\x00")
	return fmt.Sprintf("mail-hash:%x", sha256.Sum256([]byte(payload)))
}

func countMatchedMessages(messages map[string][]ICloudSyncedMessage) int {
	total := 0
	for _, items := range messages {
		total += len(items)
	}
	return total
}
