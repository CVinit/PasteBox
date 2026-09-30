package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"pastebox/internal/app"
)

const transferClaimColumns = `id, transfer_id, share_id, kind, operation_id, token, token_hash, status, expires_at, completed_at, created_at`

// AllocateTransferClaim spends one claim slot and stores the claim in the same
// transaction, under the transfer row lock. That lock is what makes the quota
// authoritative across instances: every claim for one transfer serializes on
// the row, so the count can never be read and advanced by two requests at once.
//
// A repeated operation id returns the claim that already spent the slot instead
// of spending a second one, so a retried claim stays idempotent.
func (s *TransferStore) AllocateTransferClaim(ctx context.Context, claim app.TransferClaim) (app.TransferClaim, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return app.TransferClaim{}, false, fmt.Errorf("begin allocate transfer claim: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	var status string
	var quota int
	var claimed int
	if err := tx.QueryRow(ctx, `
SELECT status, claim_quota, claimed_count
FROM transfers
WHERE id = $1
FOR UPDATE
`, claim.TransferID).Scan(&status, &quota, &claimed); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return app.TransferClaim{}, false, ErrTransferNotFound
		}
		return app.TransferClaim{}, false, fmt.Errorf("lock transfer for claim: %w", err)
	}
	if status != app.TransferStatusPublished {
		return app.TransferClaim{}, false, ErrTransferNotPublishable
	}
	if claim.OperationID != "" {
		existing, err := claimByOperation(ctx, tx, claim.TransferID, claim.OperationID)
		if err == nil {
			return commitExistingClaim(ctx, tx, existing)
		}
		if !errors.Is(err, app.ErrTransferClaimNotFound) {
			return app.TransferClaim{}, false, err
		}
	}
	if claimed >= quota {
		return app.TransferClaim{}, false, app.ErrTransferClaimQuotaExhausted
	}

	if _, err := tx.Exec(ctx, `
INSERT INTO transfer_claims (`+transferClaimColumns+`)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
`, claim.ID, claim.TransferID, claim.ShareID, claim.Kind, claim.OperationID, claim.Token, claim.TokenHash, claim.Status, claim.ExpiresAt, claim.CompletedAt, claim.CreatedAt); err != nil {
		if isUniqueViolation(err, "transfer_claims_operation_idx") {
			// Another instance committed the same operation between our lookup
			// and insert; the row lock makes that unlikely, but the retry must
			// still return the winner instead of failing the claim.
			existing, retryErr := claimByOperation(ctx, tx, claim.TransferID, claim.OperationID)
			if retryErr != nil {
				return app.TransferClaim{}, false, retryErr
			}
			return commitExistingClaim(ctx, tx, existing)
		}
		return app.TransferClaim{}, false, fmt.Errorf("create transfer claim: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE transfers
SET claimed_count = claimed_count + 1, updated_at = $2
WHERE id = $1
`, claim.TransferID, claim.CreatedAt); err != nil {
		return app.TransferClaim{}, false, fmt.Errorf("advance transfer claim count: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return app.TransferClaim{}, false, fmt.Errorf("commit transfer claim: %w", err)
	}
	return claim, true, nil
}

func (s *TransferStore) TransferClaimByTokenHash(ctx context.Context, tokenHash string) (app.TransferClaim, error) {
	return queryTransferClaim(ctx, s.pool, `
SELECT `+transferClaimColumns+`
FROM transfer_claims
WHERE token_hash = $1
`, tokenHash)
}

func (s *TransferStore) CompleteTransferClaim(ctx context.Context, id string, now time.Time) (app.TransferClaim, error) {
	return queryTransferClaim(ctx, s.pool, `
UPDATE transfer_claims
SET status = 'completed', completed_at = COALESCE(completed_at, $2)
WHERE id = $1
RETURNING `+transferClaimColumns, id, now)
}

// claimByOperation finds the claim an operation id already made, so a retried
// claim returns it instead of spending a second slot.
func claimByOperation(ctx context.Context, queryer transferQueryer, transferID string, operationID string) (app.TransferClaim, error) {
	return queryTransferClaim(ctx, queryer, `
SELECT `+transferClaimColumns+`
FROM transfer_claims
WHERE transfer_id = $1 AND operation_id = $2
`, transferID, operationID)
}

// commitExistingClaim finishes a transaction that only re-read a claim, and
// reports created=false so the caller knows the slot was already spent.
func commitExistingClaim(ctx context.Context, tx pgx.Tx, claim app.TransferClaim) (app.TransferClaim, bool, error) {
	if err := tx.Commit(ctx); err != nil {
		return app.TransferClaim{}, false, fmt.Errorf("commit transfer claim retry: %w", err)
	}
	return claim, false, nil
}

func queryTransferClaim(ctx context.Context, queryer transferQueryer, sql string, args ...any) (app.TransferClaim, error) {
	claim, err := scanTransferClaim(queryer.QueryRow(ctx, sql, args...))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return app.TransferClaim{}, app.ErrTransferClaimNotFound
		}
		return app.TransferClaim{}, err
	}
	return claim, nil
}

func scanTransferClaim(row rowScanner) (app.TransferClaim, error) {
	var claim app.TransferClaim
	var completedAt pgtype.Timestamptz
	if err := row.Scan(
		&claim.ID,
		&claim.TransferID,
		&claim.ShareID,
		&claim.Kind,
		&claim.OperationID,
		&claim.Token,
		&claim.TokenHash,
		&claim.Status,
		&claim.ExpiresAt,
		&completedAt,
		&claim.CreatedAt,
	); err != nil {
		return app.TransferClaim{}, fmt.Errorf("scan transfer claim: %w", err)
	}
	claim.CompletedAt = optionalTime(completedAt)
	return claim, nil
}
