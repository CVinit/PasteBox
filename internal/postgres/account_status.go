package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"pastebox/internal/app"
)

// AccountStatusStore reads the compact account state behind the status stream.
// It reads an indexed history page plus recent and active-editor markers.
// Claim existence is included in the page query, not fetched once per send.
type AccountStatusStore struct {
	pool *pgxpool.Pool
}

func NewAccountStatusStore(pool *pgxpool.Pool) *AccountStatusStore {
	return &AccountStatusStore{pool: pool}
}

// AccountStatus returns the sender's sends and the change markers of their
// records in two queries. The share is joined and the declared file count comes
// from an indexed subquery, so a status tick is a fixed number of queries
// whatever the account holds; each page has at most AccountStatusPageSize sends.
// Two API instances report the same answer because both read the committed
// rows.
func (s *AccountStatusStore) AccountStatus(ctx context.Context, userID string, options ...app.AccountStatusOptions) (app.AccountStatus, error) {
	opts := app.AccountStatusOptions{}
	if len(options) > 0 {
		opts = options[0]
	}
	status := app.AccountStatus{Transfers: []app.TransferStatus{}, Pastes: []app.PasteStatus{}}
	args := []any{userID, app.AccountStatusPageSize + 1}
	cursorFilter := ""
	if opts.BeforeTransferID != "" {
		// Keep the tuple predicate outside an OR so even a generic prepared
		// plan can seek the composite index instead of scanning skipped pages.
		cursorFilter = " AND (created_at, id) < (SELECT created_at, id FROM transfers WHERE id=$3 AND user_id=$1)"
		args = append(args, opts.BeforeTransferID)
	}

	rows, err := s.pool.Query(ctx, `
SELECT `+transferColumnsPrefixed+`,
       COALESCE(p.title, ''),
       COALESCE(p.status, ''),
       COALESCE(sh.token_ciphertext, ''),
       COALESCE(sh.pickup_code, ''),
       sh.expires_at,
       sh.revoked_at,
       (SELECT count(*) FROM transfer_items i WHERE i.transfer_id = t.id),
       EXISTS (SELECT 1 FROM transfer_claims c WHERE c.transfer_id = t.id AND c.status = 'active' AND c.expires_at > CURRENT_TIMESTAMP)
FROM (
  SELECT * FROM transfers
  WHERE user_id = $1`+cursorFilter+`
  ORDER BY created_at DESC, id DESC
  LIMIT $2
) t
LEFT JOIN pastes p ON p.id = t.paste_id
LEFT JOIN shares sh ON sh.id = t.share_id
WHERE t.user_id = $1
ORDER BY t.created_at DESC, t.id DESC
`, args...)
	if err != nil {
		return app.AccountStatus{}, fmt.Errorf("query account status transfers: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		record, err := scanTransferStatus(rows)
		if err != nil {
			return app.AccountStatus{}, err
		}
		status.Transfers = append(status.Transfers, record)
	}
	if err := rows.Err(); err != nil {
		return app.AccountStatus{}, fmt.Errorf("read account status transfers: %w", err)
	}

	if len(status.Transfers) > app.AccountStatusPageSize {
		status.Transfers = status.Transfers[:app.AccountStatusPageSize]
		status.NextTransferCursor = status.Transfers[app.AccountStatusPageSize-1].Transfer.ID
	}

	pasteRows, err := s.pool.Query(ctx, `
(SELECT id, status, updated_at FROM pastes WHERE user_id = $1
 ORDER BY updated_at DESC, id DESC LIMIT $2)
UNION
(SELECT id, status, updated_at FROM pastes WHERE user_id = $1 AND id = $3)
ORDER BY updated_at DESC, id DESC
`, userID, app.AccountStatusPageSize, opts.ActivePasteID)
	if err != nil {
		return app.AccountStatus{}, fmt.Errorf("query account status pastes: %w", err)
	}
	defer pasteRows.Close()
	for pasteRows.Next() {
		var paste app.PasteStatus
		if err := pasteRows.Scan(&paste.ID, &paste.Status, &paste.UpdatedAt); err != nil {
			return app.AccountStatus{}, fmt.Errorf("scan account status paste: %w", err)
		}
		status.Pastes = append(status.Pastes, paste)
	}
	if err := pasteRows.Err(); err != nil {
		return app.AccountStatus{}, fmt.Errorf("read account status pastes: %w", err)
	}
	return status, nil
}

func scanTransferStatus(row rowScanner) (app.TransferStatus, error) {
	var record app.TransferStatus
	var shareExpiresAt pgtype.Timestamptz
	var shareRevokedAt pgtype.Timestamptz
	if err := row.Scan(
		&record.Transfer.ID,
		&record.Transfer.UserID,
		&record.Transfer.PasteID,
		&record.Transfer.Status,
		&record.Transfer.IdempotencyKey,
		&record.Transfer.ShareID,
		&record.Transfer.PasswordHash,
		&record.Transfer.LoginRequired,
		&record.Transfer.ClaimQuota,
		&record.Transfer.ClaimedCount,
		&record.Transfer.BurnAfterReading,
		&record.Transfer.ExpiresAt,
		&record.Transfer.PublishedAt,
		&record.Transfer.CanceledAt,
		&record.Transfer.DestroyedAt,
		&record.Transfer.DestroyReason,
		&record.Transfer.CreatedAt,
		&record.Transfer.UpdatedAt,
		&record.Title,
		&record.CleanupStatus,
		&record.ShareToken,
		&record.PickupCode,
		&shareExpiresAt,
		&shareRevokedAt,
		&record.ItemCount,
		&record.HasLiveClaims,
	); err != nil {
		return app.TransferStatus{}, fmt.Errorf("scan account status transfer: %w", err)
	}
	if shareExpiresAt.Valid {
		record.ShareExpiresAt = shareExpiresAt.Time
	}
	record.ShareRevokedAt = optionalTime(shareRevokedAt)
	return record, nil
}
