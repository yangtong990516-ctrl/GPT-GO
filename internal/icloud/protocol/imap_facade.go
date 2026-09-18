package protocol

import (
	"context"
	"time"
)

type IMAPSyncResult = MailSyncBatchResult

func SyncICloudIMAPMessagesDetailed(ctx context.Context, state LoginState, mailboxes []Mailbox, after time.Time, keyword string, maxMessages int) (IMAPSyncResult, error) {
	result, err := SyncICloudIMAPMessagesWithCursor(ctx, state, mailboxes, after, keyword, maxMessages)
	if err != nil {
		return IMAPSyncResult{}, err
	}
	return result, nil
}

func SyncICloudIMAPMessagesWithOptions(ctx context.Context, state LoginState, mailboxes []Mailbox, options MailSyncOptions) (IMAPSyncResult, error) {
	return syncICloudIMAPMessages(ctx, state, mailboxes, options)
}
