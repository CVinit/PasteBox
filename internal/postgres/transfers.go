package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"pastebox/internal/app"
)

var (
	ErrTransferNotFound    = errors.Join(errors.New("postgres transfer not found"), app.ErrStoreNotFound)
	ErrTransferItemMissing = errors.Join(errors.New("postgres transfer item not found"), app.ErrStoreNotFound)
	// ErrTransferCanceled is returned when a canceled transfer is published.
	ErrTransferCanceled = errors.Join(errors.New("postgres transfer canceled"), app.ErrTransferStoreCanceled)
	// ErrTransferNotPublishable covers transfers whose file items have not all
	// finished uploading, and transfers that carry neither files nor text.
	ErrTransferNotPublishable = errors.Join(errors.New("postgres transfer not publishable"), app.ErrStoreConflict)
)

const transferColumns = `id, user_id, paste_id, status, idempotency_key, share_id, password_hash, login_required, claim_quota, claimed_count, burn_after_reading, expires_at, published_at, canceled_at, destroyed_at, destroy_reason, created_at, updated_at`

const transferItemColumns = `transfer_id, item_id, file_name, content_type, size_bytes, attachment_id, status, created_at, updated_at`

// transferColumnsPrefixed is transferColumns with a table alias, so a query
// that joins transfers with other rows selects the order scanTransfer reads. It
// is derived rather than repeated, so the two lists cannot drift apart.
var transferColumnsPrefixed = aliasColumns("t", transferColumns)

// aliasColumns qualifies every column of a comma-separated list with one table
// alias.
func aliasColumns(alias string, columns string) string {
	parts := strings.Split(columns, ", ")
	for i, part := range parts {
		parts[i] = alias + "." + part
	}
	return strings.Join(parts, ", ")
}

type transferQueryer interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type TransferStore struct {
	pool *pgxpool.Pool
}

func NewTransferStore(pool *pgxpool.Pool) *TransferStore {
	return &TransferStore{pool: pool}
}

func (s *TransferStore) CreateTransfer(ctx context.Context, transfer app.Transfer) error {
	if _, err := s.pool.Exec(ctx, `
INSERT INTO transfers (`+transferColumns+`)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
`, transfer.ID, transfer.UserID, transfer.PasteID, transfer.Status, transfer.IdempotencyKey, transfer.ShareID, transfer.PasswordHash, transfer.LoginRequired, transfer.ClaimQuota, transfer.ClaimedCount, transfer.BurnAfterReading, transfer.ExpiresAt, transfer.PublishedAt, transfer.CanceledAt, transfer.DestroyedAt, transfer.DestroyReason, transfer.CreatedAt, transfer.UpdatedAt); err != nil {
		if isUniqueViolation(err, "transfers_user_idempotency_key_idx") || isUniqueViolation(err, "transfers_pkey") {
			return errors.Join(fmt.Errorf("create transfer: %w", err), app.ErrStoreConflict)
		}
		return fmt.Errorf("create transfer: %w", err)
	}
	return nil
}

func (s *TransferStore) TransferByID(ctx context.Context, id string) (app.Transfer, error) {
	return queryTransfer(ctx, s.pool, `
SELECT `+transferColumns+`
FROM transfers
WHERE id = $1
`, id)
}

func (s *TransferStore) TransferByIdempotencyKey(ctx context.Context, userID string, key string) (app.Transfer, error) {
	return queryTransfer(ctx, s.pool, `
SELECT `+transferColumns+`
FROM transfers
WHERE user_id = $1 AND idempotency_key = $2
`, userID, key)
}

// TransferByShareID finds the transfer a published share belongs to. A share
// without a transfer is a legacy share and reports not-found, which is how the
// service tells the two access rules apart.
func (s *TransferStore) TransferByShareID(ctx context.Context, shareID string) (app.Transfer, error) {
	return queryTransfer(ctx, s.pool, `
SELECT `+transferColumns+`
FROM transfers
WHERE share_id = $1 AND share_id <> ''
`, shareID)
}

func (s *TransferStore) ListTransfersByUser(ctx context.Context, userID string) ([]app.Transfer, error) {
	rows, err := s.pool.Query(ctx, `
SELECT `+transferColumns+`
FROM transfers
WHERE user_id = $1
ORDER BY created_at DESC, id DESC
`, userID)
	if err != nil {
		return nil, fmt.Errorf("query transfers by user: %w", err)
	}
	defer rows.Close()
	transfers := []app.Transfer{}
	for rows.Next() {
		transfer, err := scanTransfer(rows)
		if err != nil {
			return nil, err
		}
		transfers = append(transfers, transfer)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read transfers: %w", err)
	}
	return transfers, nil
}

func (s *TransferStore) UpdateTransfer(ctx context.Context, transfer app.Transfer) error {
	if _, err := s.pool.Exec(ctx, `
UPDATE transfers
SET status = $2,
    idempotency_key = $3,
    share_id = $4,
    password_hash = $5,
    login_required = $6,
    claim_quota = $7,
    claimed_count = $8,
    burn_after_reading = $9,
    expires_at = $10,
    published_at = $11,
    canceled_at = $12,
    destroyed_at = $13,
    destroy_reason = $14,
    updated_at = $15
WHERE id = $1
`, transfer.ID, transfer.Status, transfer.IdempotencyKey, transfer.ShareID, transfer.PasswordHash, transfer.LoginRequired, transfer.ClaimQuota, transfer.ClaimedCount, transfer.BurnAfterReading, transfer.ExpiresAt, transfer.PublishedAt, transfer.CanceledAt, transfer.DestroyedAt, transfer.DestroyReason, transfer.UpdatedAt); err != nil {
		return fmt.Errorf("update transfer: %w", err)
	}
	return nil
}

// CountLiveTransferClaims counts the claim sessions that are still open. A
// completed or expired session is over, so it no longer holds the send open.
func (s *TransferStore) CountLiveTransferClaims(ctx context.Context, transferID string, now time.Time) (int, error) {
	var live int
	if err := s.pool.QueryRow(ctx, `
SELECT count(*)
FROM transfer_claims
WHERE transfer_id = $1 AND status = 'active' AND expires_at > $2
`, transferID, now).Scan(&live); err != nil {
		return 0, fmt.Errorf("count live transfer claims: %w", err)
	}
	return live, nil
}

// ListBurnableTransfers returns the burn-after-reading sends that have to be
// destroyed now: either their lifetime is over, or every claim slot was spent
// and no session is still open. It is the query behind the sweep that keeps
// destruction reliable when nobody touches the send again.
func (s *TransferStore) ListBurnableTransfers(ctx context.Context, now time.Time, limit int) ([]app.Transfer, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
SELECT `+transferColumns+`
FROM transfers
WHERE burn_after_reading
  AND status = 'published'
  AND (
    expires_at <= $1
    OR (
      claimed_count >= claim_quota
      AND NOT EXISTS (
        SELECT 1
        FROM transfer_claims claims
        WHERE claims.transfer_id = transfers.id
          AND claims.status = 'active'
          AND claims.expires_at > $1
      )
    )
  )
ORDER BY expires_at ASC, id ASC
LIMIT $2
`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("query burnable transfers: %w", err)
	}
	defer rows.Close()
	transfers := []app.Transfer{}
	for rows.Next() {
		transfer, err := scanTransfer(rows)
		if err != nil {
			return nil, err
		}
		transfers = append(transfers, transfer)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read burnable transfers: %w", err)
	}
	return transfers, nil
}

// DestroyTransfer moves a published transfer to its terminal state in one
// transaction: the transfer stops authorizing access, its share is revoked, its
// content is marked for deletion and the cleanup job that releases the bytes is
// queued. Committing the denial together with the cleanup intent is what makes
// the promise hold — access is refused before any byte is deleted, and a send
// can never be left destroyed with nothing scheduled to release its content.
//
// Which sends may be destroyed is the service's decision (its burn-after-reading
// switch); the store only refuses transfers that are not published.
//
// The job id comes from the caller so the service keeps owning identifier
// generation, the way it does for every other record it stores.
func (s *TransferStore) DestroyTransfer(ctx context.Context, transferID string, cleanupJobID string, now time.Time, reason string) (app.Transfer, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return app.Transfer{}, false, fmt.Errorf("begin destroy transfer: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	transfer, err := queryTransfer(ctx, tx, `
SELECT `+transferColumns+`
FROM transfers
WHERE id = $1
FOR UPDATE
`, transferID)
	if err != nil {
		return app.Transfer{}, false, err
	}
	switch transfer.Status {
	case app.TransferStatusDestroyed:
		// A retry must not enqueue a second cleanup or mint a second terminal
		// state.
		if err := tx.Commit(ctx); err != nil {
			return app.Transfer{}, false, fmt.Errorf("commit destroy transfer retry: %w", err)
		}
		return transfer, false, nil
	case app.TransferStatusPublished:
	default:
		return app.Transfer{}, false, ErrTransferNotPublishable
	}

	updated, err := queryTransfer(ctx, tx, `
UPDATE transfers
SET status = 'destroyed', destroyed_at = $2, destroy_reason = $3, updated_at = $2
WHERE id = $1
RETURNING `+transferColumns, transferID, now, reason)
	if err != nil {
		return app.Transfer{}, false, err
	}
	if updated.ShareID != "" {
		if _, err := tx.Exec(ctx, `
UPDATE shares
SET revoked_at = COALESCE(revoked_at, $2)
WHERE id = $1
`, updated.ShareID, now); err != nil {
			return app.Transfer{}, false, fmt.Errorf("revoke destroyed transfer share: %w", err)
		}
	}
	tag, err := tx.Exec(ctx, `
UPDATE pastes
SET status = 'pending_delete', updated_at = $2
WHERE id = $1 AND status = 'active'
`, updated.PasteID, now)
	if err != nil {
		return app.Transfer{}, false, fmt.Errorf("mark destroyed transfer content: %w", err)
	}
	if tag.RowsAffected() > 0 {
		if _, err := tx.Exec(ctx, `
UPDATE attachments
SET status = 'pending_delete'
WHERE paste_id = $1 AND status = 'active'
`, updated.PasteID); err != nil {
			return app.Transfer{}, false, fmt.Errorf("mark destroyed transfer attachments: %w", err)
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO jobs (id, kind, target_id, status, attempts, last_error, run_after, claimed_by, lease_expires_at, created_at, updated_at)
VALUES ($1, 'cleanup', $2, 'pending', 0, '', $3, '', NULL, $3, $3)
`, cleanupJobID, updated.PasteID, now); err != nil {
			return app.Transfer{}, false, fmt.Errorf("queue destroyed transfer cleanup: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return app.Transfer{}, false, fmt.Errorf("commit destroy transfer: %w", err)
	}
	return updated, true, nil
}

func (s *TransferStore) CreateTransferItem(ctx context.Context, item app.TransferItem) error {
	if _, err := s.pool.Exec(ctx, `
INSERT INTO transfer_items (`+transferItemColumns+`)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
`, item.TransferID, item.ItemID, item.FileName, item.ContentType, item.Size, item.AttachmentID, item.Status, item.CreatedAt, item.UpdatedAt); err != nil {
		return fmt.Errorf("create transfer item: %w", err)
	}
	return nil
}

func (s *TransferStore) TransferItem(ctx context.Context, transferID string, itemID string) (app.TransferItem, error) {
	return queryTransferItem(ctx, s.pool, `
SELECT `+transferItemColumns+`
FROM transfer_items
WHERE transfer_id = $1 AND item_id = $2
`, transferID, itemID)
}

func (s *TransferStore) ListTransferItems(ctx context.Context, transferID string) ([]app.TransferItem, error) {
	rows, err := s.pool.Query(ctx, `
SELECT `+transferItemColumns+`
FROM transfer_items
WHERE transfer_id = $1
ORDER BY created_at ASC, item_id ASC
`, transferID)
	if err != nil {
		return nil, fmt.Errorf("query transfer items: %w", err)
	}
	defer rows.Close()
	items := []app.TransferItem{}
	for rows.Next() {
		item, err := scanTransferItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read transfer items: %w", err)
	}
	return items, nil
}

func (s *TransferStore) UpdateTransferItem(ctx context.Context, item app.TransferItem) error {
	if _, err := s.pool.Exec(ctx, `
UPDATE transfer_items
SET file_name = $3,
    content_type = $4,
    size_bytes = $5,
    attachment_id = $6,
    status = $7,
    updated_at = $8
WHERE transfer_id = $1 AND item_id = $2
`, item.TransferID, item.ItemID, item.FileName, item.ContentType, item.Size, item.AttachmentID, item.Status, item.UpdatedAt); err != nil {
		return fmt.Errorf("update transfer item: %w", err)
	}
	return nil
}

// PublishTransfer locks the transfer row, re-checks that every declared item is
// uploaded, stores the share and flips the transfer to published in one
// transaction. A retry of an already published transfer returns the stored row
// instead of minting a second share.
func (s *TransferStore) PublishTransfer(ctx context.Context, transferID string, share app.Share, now time.Time, allowNoItems bool) (app.Transfer, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return app.Transfer{}, fmt.Errorf("begin publish transfer: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	transfer, err := queryTransfer(ctx, tx, `
SELECT `+transferColumns+`
FROM transfers
WHERE id = $1
FOR UPDATE
`, transferID)
	if err != nil {
		return app.Transfer{}, err
	}
	switch transfer.Status {
	case app.TransferStatusPublished:
		if err := tx.Commit(ctx); err != nil {
			return app.Transfer{}, fmt.Errorf("commit publish transfer: %w", err)
		}
		return transfer, nil
	case app.TransferStatusCanceled:
		return app.Transfer{}, ErrTransferCanceled
	}

	// Every declared item must be uploaded and its attachment must still belong
	// to this transfer's paste, checked under the same row lock that guards the
	// publish so the check cannot be raced. A transfer with no declared items is
	// only publishable when the caller says its record carries content, because
	// what counts as content is the service's rule.
	var total int
	var pending int
	if err := tx.QueryRow(ctx, `
SELECT count(*), count(*) FILTER (
	WHERE items.status <> 'uploaded'
	   OR items.attachment_id = ''
	   OR attachments.id IS NULL
	   OR attachments.paste_id <> $2
	   OR attachments.status <> 'active'
)
FROM transfer_items items
LEFT JOIN attachments ON attachments.id = items.attachment_id
WHERE items.transfer_id = $1
`, transferID, transfer.PasteID).Scan(&total, &pending); err != nil {
		return app.Transfer{}, fmt.Errorf("check transfer items: %w", err)
	}
	if pending > 0 || (total == 0 && !allowNoItems) {
		return app.Transfer{}, ErrTransferNotPublishable
	}

	if _, err := tx.Exec(ctx, shareInsert, shareInsertArgs(share)...); err != nil {
		return app.Transfer{}, shareInsertError("create transfer share", err)
	}

	updated, err := queryTransfer(ctx, tx, `
UPDATE transfers
SET status = 'published', share_id = $2, published_at = $3, updated_at = $3
WHERE id = $1
RETURNING `+transferColumns, transferID, share.ID, now)
	if err != nil {
		return app.Transfer{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return app.Transfer{}, fmt.Errorf("commit publish transfer: %w", err)
	}
	return updated, nil
}

func queryTransfer(ctx context.Context, queryer transferQueryer, sql string, args ...any) (app.Transfer, error) {
	transfer, err := scanTransfer(queryer.QueryRow(ctx, sql, args...))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return app.Transfer{}, ErrTransferNotFound
		}
		return app.Transfer{}, err
	}
	return transfer, nil
}

func queryTransferItem(ctx context.Context, queryer transferQueryer, sql string, args ...any) (app.TransferItem, error) {
	item, err := scanTransferItem(queryer.QueryRow(ctx, sql, args...))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return app.TransferItem{}, ErrTransferItemMissing
		}
		return app.TransferItem{}, err
	}
	return item, nil
}

func scanTransfer(row rowScanner) (app.Transfer, error) {
	var transfer app.Transfer
	var publishedAt pgtype.Timestamptz
	var canceledAt pgtype.Timestamptz
	var destroyedAt pgtype.Timestamptz
	if err := row.Scan(
		&transfer.ID,
		&transfer.UserID,
		&transfer.PasteID,
		&transfer.Status,
		&transfer.IdempotencyKey,
		&transfer.ShareID,
		&transfer.PasswordHash,
		&transfer.LoginRequired,
		&transfer.ClaimQuota,
		&transfer.ClaimedCount,
		&transfer.BurnAfterReading,
		&transfer.ExpiresAt,
		&publishedAt,
		&canceledAt,
		&destroyedAt,
		&transfer.DestroyReason,
		&transfer.CreatedAt,
		&transfer.UpdatedAt,
	); err != nil {
		return app.Transfer{}, fmt.Errorf("scan transfer: %w", err)
	}
	transfer.PublishedAt = optionalTime(publishedAt)
	transfer.CanceledAt = optionalTime(canceledAt)
	transfer.DestroyedAt = optionalTime(destroyedAt)
	return transfer, nil
}

func scanTransferItem(row rowScanner) (app.TransferItem, error) {
	var item app.TransferItem
	if err := row.Scan(
		&item.TransferID,
		&item.ItemID,
		&item.FileName,
		&item.ContentType,
		&item.Size,
		&item.AttachmentID,
		&item.Status,
		&item.CreatedAt,
		&item.UpdatedAt,
	); err != nil {
		return app.TransferItem{}, fmt.Errorf("scan transfer item: %w", err)
	}
	return item, nil
}
