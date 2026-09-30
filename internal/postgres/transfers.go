package postgres

import (
	"context"
	"errors"
	"fmt"
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

const transferColumns = `id, user_id, paste_id, status, idempotency_key, share_id, password_hash, login_required, expires_at, published_at, canceled_at, created_at, updated_at`

const transferItemColumns = `transfer_id, item_id, file_name, content_type, size_bytes, attachment_id, status, created_at, updated_at`

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
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
`, transfer.ID, transfer.UserID, transfer.PasteID, transfer.Status, transfer.IdempotencyKey, transfer.ShareID, transfer.PasswordHash, transfer.LoginRequired, transfer.ExpiresAt, transfer.PublishedAt, transfer.CanceledAt, transfer.CreatedAt, transfer.UpdatedAt); err != nil {
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
    expires_at = $7,
    published_at = $8,
    canceled_at = $9,
    updated_at = $10
WHERE id = $1
`, transfer.ID, transfer.Status, transfer.IdempotencyKey, transfer.ShareID, transfer.PasswordHash, transfer.LoginRequired, transfer.ExpiresAt, transfer.PublishedAt, transfer.CanceledAt, transfer.UpdatedAt); err != nil {
		return fmt.Errorf("update transfer: %w", err)
	}
	return nil
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
	if err := row.Scan(
		&transfer.ID,
		&transfer.UserID,
		&transfer.PasteID,
		&transfer.Status,
		&transfer.IdempotencyKey,
		&transfer.ShareID,
		&transfer.PasswordHash,
		&transfer.LoginRequired,
		&transfer.ExpiresAt,
		&publishedAt,
		&canceledAt,
		&transfer.CreatedAt,
		&transfer.UpdatedAt,
	); err != nil {
		return app.Transfer{}, fmt.Errorf("scan transfer: %w", err)
	}
	transfer.PublishedAt = optionalTime(publishedAt)
	transfer.CanceledAt = optionalTime(canceledAt)
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
