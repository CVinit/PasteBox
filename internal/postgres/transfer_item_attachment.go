package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"pastebox/internal/app"
)

func (s *TransferStore) CommitTransferItemAttachment(ctx context.Context, input app.TransferItemAttachmentInput) (app.TransferItemAttachmentResult, error) {
	empty := app.TransferItemAttachmentResult{}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return empty, fmt.Errorf("begin transfer attachment: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	transfer, err := queryTransfer(ctx, tx, `SELECT `+transferColumns+` FROM transfers WHERE id=$1 FOR UPDATE`, input.TransferID)
	if err != nil {
		return empty, err
	}
	attachment := input.Attachment
	if transfer.Status != app.TransferStatusDraft || transfer.PasteID != attachment.PasteID || transfer.UserID != attachment.UserID {
		return empty, app.ErrStoreConflict
	}
	item, err := queryTransferItem(ctx, tx, `SELECT `+transferItemColumns+` FROM transfer_items WHERE transfer_id=$1 AND item_id=$2 FOR UPDATE`, input.TransferID, input.ItemID)
	if err != nil {
		return empty, err
	}
	lockCtx := context.WithValue(ctx, scopedTransactionKey{}, scopedTransaction{pool: s.pool, tx: tx})
	attachments := NewAttachmentStore(s.pool)
	if item.Status == app.TransferItemUploaded {
		existing, err := attachments.AttachmentByID(lockCtx, item.AttachmentID)
		if err != nil {
			return empty, err
		}
		if existing.SHA256 != attachment.SHA256 || existing.Size != attachment.Size {
			return empty, app.ErrStoreConflict
		}
		return app.TransferItemAttachmentResult{Attachment: existing}, nil
	}
	// A cancel, expiry, or takedown racing the object upload must not resurrect
	// the backing content. Lock the row before creating any durable side effects.
	var active bool
	if err := tx.QueryRow(ctx, `SELECT status='active' AND expires_at > CURRENT_TIMESTAMP FROM pastes WHERE id=$1 FOR UPDATE`, transfer.PasteID).Scan(&active); err != nil {
		return empty, err
	}
	if !active {
		return empty, app.ErrStoreConflict
	}
	if err := attachments.CreateAttachment(lockCtx, attachment); err != nil {
		return empty, err
	}
	if _, err := tx.Exec(ctx, `
UPDATE transfer_items SET attachment_id=$3, file_name=$4, content_type=$5,
size_bytes=$6, status='uploaded', updated_at=$7 WHERE transfer_id=$1 AND item_id=$2
`, input.TransferID, input.ItemID, attachment.ID, attachment.FileName, attachment.ContentType, attachment.Size, attachment.CreatedAt); err != nil {
		return empty, fmt.Errorf("bind transfer attachment: %w", err)
	}
	// Match aggregateScanStatus: malicious > scan_failed > pending > clean.
	if _, err := tx.Exec(ctx, `
UPDATE pastes SET updated_at=$2, scan_status=(
  SELECT CASE WHEN bool_or(scan_status='malicious') THEN 'malicious'
              WHEN bool_or(scan_status='scan_failed') THEN 'scan_failed'
              ELSE 'pending' END
  FROM attachments WHERE paste_id=$1 AND status='active'
) WHERE id=$1
`, transfer.PasteID, attachment.CreatedAt); err != nil {
		return empty, fmt.Errorf("update transfer paste: %w", err)
	}
	if err := NewJobStore(s.pool).CreateQueueItem(lockCtx, input.ScanJob); err != nil {
		return empty, err
	}
	if err := recordDailyMetric(ctx, tx, attachment.UserID, "upload", attachment.CreatedAt, attachment.Size); err != nil {
		return empty, err
	}
	if err := tx.Commit(ctx); err != nil {
		// Keep the reservation if the connection failed after COMMIT was sent:
		// deleting it could destroy bytes that the committed attachment owns.
		return app.TransferItemAttachmentResult{RetainReference: !errors.Is(err, pgx.ErrTxCommitRollback)}, fmt.Errorf("commit transfer attachment: %w", err)
	}
	return app.TransferItemAttachmentResult{Attachment: attachment, RetainReference: true}, nil
}

var _ app.TransferItemAttachmentStore = (*TransferStore)(nil)
