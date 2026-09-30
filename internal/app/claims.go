package app

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
)

const (
	// TransferClaimKindFile marks a claim that opens a bounded download session
	// covering every file of the batch.
	TransferClaimKindFile = "file"
	// TransferClaimKindText marks a claim that hands out a text body once.
	TransferClaimKindText = "text"

	TransferClaimStatusActive    = "active"
	TransferClaimStatusCompleted = "completed"

	// DefaultTransferClaimQuota is how many anonymous claims a send grants when
	// the sender does not choose one.
	DefaultTransferClaimQuota = 1
	// MaxTransferClaimQuota bounds how many slots one send may grant.
	MaxTransferClaimQuota = 100
	// TransferClaimSessionTTL bounds one file claim session. The session also
	// never outlives the share it belongs to.
	TransferClaimSessionTTL = 30 * time.Minute
	// TransferClaimTextWindow bounds how long a text claim can be replayed after
	// a network failure. Past it the claim is gone, so a text send never leaves
	// a permanently readable entry behind.
	TransferClaimTextWindow = 2 * time.Minute
	// maxTransferClaimOperationID bounds the client operation id that makes a
	// claim retry idempotent.
	maxTransferClaimOperationID = 128
)

func claimQuotaExhaustedError() *Error {
	return E(http.StatusGone, "claim_quota_exhausted", "all claim slots of this transfer have been used")
}

func claimRequiredError() *Error {
	return E(http.StatusForbidden, "claim_required", "claim this transfer before reading or downloading it")
}

func claimEndedError() *Error {
	return E(http.StatusGone, "claim_ended", "this claim has ended")
}

func viewTransferClaim(claim *TransferClaim) TransferClaimView {
	return TransferClaimView{
		ID:          claim.ID,
		Kind:        claim.Kind,
		Status:      claim.Status,
		ExpiresAt:   claim.ExpiresAt,
		CompletedAt: claim.CompletedAt,
		CreatedAt:   claim.CreatedAt,
	}
}

func transferClaimOperationKey(transferID string, operationID string) string {
	return transferID + "\x00" + operationID
}

func (s *Service) cacheTransferClaimLocked(claim TransferClaim) *TransferClaim {
	cached := claim
	s.claimsByID[cached.ID] = &cached
	s.claimIDByTokenHash[cached.TokenHash] = cached.ID
	if cached.OperationID != "" {
		s.claimIDByOperation[transferClaimOperationKey(cached.TransferID, cached.OperationID)] = cached.ID
	}
	return &cached
}

// transferForShareLocked returns the transfer a published share belongs to, or
// nil for a legacy share that never went through a transfer. Legacy shares keep
// their own access rules, so this lookup is what decides whether the claim gate
// applies.
func (s *Service) transferForShareLocked(ctx context.Context, shareID string) (*Transfer, error) {
	if shareID == "" {
		return nil, nil
	}
	if s.content.Transfers != nil {
		s.mu.Unlock()
		loaded, err := s.content.Transfers.TransferByShareID(ctx, shareID)
		s.mu.Lock()
		if err != nil {
			if isStoreNotFound(err) {
				return nil, nil
			}
			return nil, err
		}
		return s.cacheTransferLocked(loaded), nil
	}
	transferID := s.transferIDByShareID[shareID]
	if transferID == "" {
		return nil, nil
	}
	return s.transfersByID[transferID], nil
}

// transferClaimByTokenHashLocked returns (nil, nil) for an unknown credential.
func (s *Service) transferClaimByTokenHashLocked(ctx context.Context, hash string) (*TransferClaim, error) {
	if hash == "" {
		return nil, nil
	}
	if s.content.Transfers != nil {
		s.mu.Unlock()
		loaded, err := s.content.Transfers.TransferClaimByTokenHash(ctx, hash)
		s.mu.Lock()
		if err != nil {
			if isStoreNotFound(err) {
				return nil, nil
			}
			return nil, err
		}
		return s.cacheTransferClaimLocked(loaded), nil
	}
	claimID := s.claimIDByTokenHash[hash]
	if claimID == "" {
		return nil, nil
	}
	claim := s.claimsByID[claimID]
	if claim == nil {
		return nil, nil
	}
	return claim, nil
}

// transferClaimKindLocked decides whether a transfer hands out a text body or a
// file session from what the send actually carries, not from a client field.
func (s *Service) transferClaimKindLocked(ctx context.Context, transfer *Transfer) (string, error) {
	items, err := s.transferItemsLocked(ctx, transfer.ID)
	if err != nil {
		return "", err
	}
	if len(items) == 0 {
		return TransferClaimKindText, nil
	}
	return TransferClaimKindFile, nil
}

// claimForShareLocked returns the live claim a request presents for this share,
// or nil when it holds none. A stale or foreign credential never fails the
// page: it just means this caller has not claimed yet.
func (s *Service) claimForShareLocked(ctx context.Context, share *Share, claimToken string, now time.Time) (*TransferClaim, error) {
	if strings.TrimSpace(claimToken) == "" {
		return nil, nil
	}
	claim, err := s.transferClaimByTokenHashLocked(ctx, tokenHash(claimToken))
	if err != nil {
		return nil, err
	}
	if claim == nil || claim.ShareID != share.ID || claim.Status != TransferClaimStatusActive || !claim.ExpiresAt.After(now) {
		return nil, nil
	}
	return claim, nil
}

// validTransferClaimLocked resolves the live claim a request presents for a
// transfer-backed share. The claim must belong to the share in the path and
// still be open, so an ended session cannot be replayed and a claim cannot be
// used against another share.
func (s *Service) validTransferClaimLocked(ctx context.Context, shareToken string, claimToken string, viewerUserID string) (*Share, *Transfer, *TransferClaim, error) {
	if strings.TrimSpace(claimToken) == "" {
		return nil, nil, nil, claimRequiredError()
	}
	claim, err := s.transferClaimByTokenHashLocked(ctx, tokenHash(claimToken))
	if err != nil {
		return nil, nil, nil, err
	}
	if claim == nil {
		return nil, nil, nil, claimRequiredError()
	}
	if claim.Status == TransferClaimStatusCompleted || !claim.ExpiresAt.After(s.now().UTC()) {
		return nil, nil, nil, claimEndedError()
	}
	// The share is re-checked on every use, so revoking it or letting it expire
	// ends the sessions it authorized.
	share, _, err := s.validShareAccessLocked(ctx, shareToken, "", viewerUserID, false, true)
	if err != nil {
		return nil, nil, nil, err
	}
	if share.ID != claim.ShareID {
		return nil, nil, nil, claimRequiredError()
	}
	transfer, err := s.transferForShareLocked(ctx, share.ID)
	if err != nil {
		return nil, nil, nil, err
	}
	if transfer == nil {
		return nil, nil, nil, claimEndedError()
	}
	if transfer.Status == TransferStatusDestroyed {
		// The send already reached its terminal state, so a session that was
		// still open cannot keep reading content that is on its way out.
		return nil, nil, nil, transferDestroyedError()
	}
	return share, transfer, claim, nil
}

// sharedAccessViewLocked builds the recipient's view of an opened share. A
// transfer-backed share withholds its content until the caller holds a live
// claim, and reports the claim state so the page can offer the right action.
func (s *Service) sharedAccessViewLocked(ctx context.Context, share *Share, paste *Paste, claimToken string) (PasteView, ShareView, *TransferAccessView, error) {
	transfer, err := s.transferForShareLocked(ctx, share.ID)
	if err != nil {
		return PasteView{}, ShareView{}, nil, err
	}
	if transfer == nil {
		return s.viewPasteLocked(paste), s.viewShareLocked(share), nil, nil
	}
	if transfer.Status == TransferStatusDestroyed {
		return PasteView{}, ShareView{}, nil, transferDestroyedError()
	}
	kind, err := s.transferClaimKindLocked(ctx, transfer)
	if err != nil {
		return PasteView{}, ShareView{}, nil, err
	}
	access := &TransferAccessView{
		ID:              transfer.ID,
		Kind:            kind,
		ClaimQuota:      transfer.ClaimQuota,
		ClaimedCount:    transfer.ClaimedCount,
		ClaimsRemaining: max(transfer.ClaimQuota-transfer.ClaimedCount, 0),
		ExpiresAt:       share.ExpiresAt,
	}
	claim, err := s.claimForShareLocked(ctx, share, claimToken, s.now().UTC())
	if err != nil {
		return PasteView{}, ShareView{}, nil, err
	}
	if claim != nil {
		expiresAt := claim.ExpiresAt
		access.Claimed = true
		access.ClaimID = claim.ID
		access.ClaimExpiresAt = &expiresAt
	}
	pasteView := s.viewPasteLocked(paste)
	if claim == nil {
		// The claim is the only place content is handed out, so opening the page
		// cannot leak the body of a text send — or of a mixed send, which also
		// declares files — and cannot spend a slot by accident.
		pasteView.Text = ""
		pasteView.TextPreview = ""
	}
	return pasteView, s.viewShareLocked(share), access, nil
}

// allocateTransferClaimLocked spends one claim slot. created reports whether
// this request spent the slot; false means the operation id already claimed and
// the stored claim is returned unchanged, which keeps a retried claim
// idempotent without spending a second slot.
func (s *Service) allocateTransferClaimLocked(ctx context.Context, transfer *Transfer, share *Share, kind string, operationID string, now time.Time) (*TransferClaim, bool, error) {
	if s.content.Transfers == nil {
		if existing := s.claimByOperationLocked(transfer.ID, operationID); existing != nil {
			return existing, false, nil
		}
		if transfer.ClaimedCount >= transfer.ClaimQuota {
			return nil, false, claimQuotaExhaustedError()
		}
	}

	expiresAt := share.ExpiresAt
	windowEnd := now.Add(TransferClaimSessionTTL)
	if kind == TransferClaimKindText {
		windowEnd = now.Add(TransferClaimTextWindow)
	}
	if windowEnd.Before(expiresAt) {
		expiresAt = windowEnd
	}
	if !expiresAt.After(now) {
		return nil, false, E(http.StatusGone, "share_expired", "share is expired or revoked")
	}
	token := newToken()
	claim := TransferClaim{
		ID:          s.newID("clm"),
		TransferID:  transfer.ID,
		ShareID:     share.ID,
		Kind:        kind,
		OperationID: operationID,
		Token:       token,
		TokenHash:   tokenHash(token),
		Status:      TransferClaimStatusActive,
		ExpiresAt:   expiresAt,
		CreatedAt:   now,
	}

	if s.content.Transfers != nil {
		s.mu.Unlock()
		allocated, created, err := s.content.Transfers.AllocateTransferClaim(ctx, claim)
		s.mu.Lock()
		if err != nil {
			if errors.Is(err, ErrTransferClaimQuotaExhausted) {
				return nil, false, claimQuotaExhaustedError()
			}
			if isStoreNotFound(err) {
				return nil, false, E(http.StatusNotFound, "transfer_not_found", "transfer not found")
			}
			if errors.Is(err, ErrStoreConflict) {
				return nil, false, E(http.StatusGone, "transfer_not_published", "transfer is not published")
			}
			return nil, false, err
		}
		if created {
			// The store moved the quota, so the cached transfer is stale. The
			// sender's view must report the authoritative count.
			if refreshed, loadErr := s.transferByIDLocked(ctx, claim.TransferID); loadErr == nil {
				*transfer = *refreshed
			}
		}
		return s.cacheTransferClaimLocked(allocated), created, nil
	}

	transfer.ClaimedCount++
	transfer.UpdatedAt = now
	return s.cacheTransferClaimLocked(claim), true, nil
}

func (s *Service) claimByOperationLocked(transferID string, operationID string) *TransferClaim {
	claimID := s.claimIDByOperation[transferClaimOperationKey(transferID, operationID)]
	if claimID == "" {
		return nil
	}
	return s.claimsByID[claimID]
}

// ClaimTransferWithContext spends one anonymous claim slot and hands back the
// credential for the claimed session. It is the only action that consumes a
// slot: opening the page, resolving a pickup code and reading claim state do
// not.
func (s *Service) ClaimTransferWithContext(ctx context.Context, token string, password string, viewerUserID string, operationID string) (TransferClaimResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	operationID = strings.TrimSpace(operationID)
	if operationID == "" || len(operationID) > maxTransferClaimOperationID {
		return TransferClaimResult{}, E(http.StatusBadRequest, "invalid_claim_operation", "claim operation id is invalid")
	}
	passwordVerified, err := s.verifySharePasswordForAccess(ctx, token, password)
	if err != nil {
		return TransferClaimResult{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// The terminal state is checked before the share checks, so a send that was
	// destroyed answers with its own code instead of the generic expiry its
	// deleted content would produce.
	if err := s.refuseDestroyedByTokenLocked(ctx, token); err != nil {
		return TransferClaimResult{}, err
	}
	share, paste, err := s.validShareAccessLocked(ctx, token, password, viewerUserID, false, passwordVerified)
	if err != nil {
		return TransferClaimResult{}, err
	}
	transfer, err := s.transferForShareLocked(ctx, share.ID)
	if err != nil {
		return TransferClaimResult{}, err
	}
	if transfer == nil || transfer.Status != TransferStatusPublished {
		return TransferClaimResult{}, E(http.StatusNotFound, "transfer_not_found", "transfer not found")
	}
	kind, err := s.transferClaimKindLocked(ctx, transfer)
	if err != nil {
		return TransferClaimResult{}, err
	}
	now := s.now().UTC()
	claim, created, err := s.allocateTransferClaimLocked(ctx, transfer, share, kind, operationID, now)
	if err != nil {
		return TransferClaimResult{}, err
	}
	if !created {
		// A retried claim must not spend a second slot, and an ended session
		// must not come back to life.
		if claim.Kind != kind {
			return TransferClaimResult{}, E(http.StatusConflict, "claim_conflict", "claim operation was already used for another claim")
		}
		if claim.Status == TransferClaimStatusCompleted || !claim.ExpiresAt.After(now) {
			return TransferClaimResult{}, claimEndedError()
		}
	}
	return s.transferClaimResultLocked(claim, paste), nil
}

// transferClaimResultLocked is the one place a claim hands out content: the
// text body for a text transfer, the session credential for a file transfer.
func (s *Service) transferClaimResultLocked(claim *TransferClaim, paste *Paste) TransferClaimResult {
	result := TransferClaimResult{Claim: viewTransferClaim(claim), ClaimToken: claim.Token}
	if claim.Kind == TransferClaimKindText {
		result.Text = paste.Text
	}
	return result
}

// CompleteTransferClaimWithContext ends a file claim session on the recipient's
// request. Repeating it is a no-op, and it never spends or refunds a slot.
func (s *Service) CompleteTransferClaimWithContext(ctx context.Context, shareToken string, claimID string, claimToken string, viewerUserID string) (TransferClaimView, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	credential := strings.TrimSpace(claimToken)
	if credential == "" {
		return TransferClaimView{}, claimRequiredError()
	}
	claim, err := s.transferClaimByTokenHashLocked(ctx, tokenHash(credential))
	if err != nil {
		return TransferClaimView{}, err
	}
	if claim == nil || claim.ID != strings.TrimSpace(claimID) {
		return TransferClaimView{}, claimRequiredError()
	}
	if claim.Status == TransferClaimStatusCompleted {
		// Completing twice is a no-op, not a new session or a refunded slot. A
		// burn-after-reading send still gets its destruction attempt here, so a
		// failed one is retried by the next request instead of being forgotten.
		if err := s.maybeDestroyAfterClaimEndedLocked(ctx, claim); err != nil {
			return TransferClaimView{}, err
		}
		return viewTransferClaim(claim), nil
	}
	now := s.now().UTC()
	if !claim.ExpiresAt.After(now) {
		return TransferClaimView{}, claimEndedError()
	}
	if err := s.refuseDestroyedByTokenLocked(ctx, shareToken); err != nil {
		return TransferClaimView{}, err
	}
	share, _, err := s.validShareAccessLocked(ctx, shareToken, "", viewerUserID, false, true)
	if err != nil {
		return TransferClaimView{}, err
	}
	if share.ID != claim.ShareID {
		return TransferClaimView{}, claimRequiredError()
	}
	claim.Status = TransferClaimStatusCompleted
	claim.CompletedAt = &now
	if s.content.Transfers != nil {
		s.mu.Unlock()
		updated, err := s.content.Transfers.CompleteTransferClaim(ctx, claim.ID, now)
		s.mu.Lock()
		if err != nil {
			if isStoreNotFound(err) || errors.Is(err, ErrTransferClaimEnded) {
				return TransferClaimView{}, claimEndedError()
			}
			return TransferClaimView{}, err
		}
		claim = s.cacheTransferClaimLocked(updated)
	}
	// Ending the last session is what lets a burn-after-reading send go: nobody
	// can be handed its content any more.
	if err := s.maybeDestroyAfterClaimEndedLocked(ctx, claim); err != nil {
		return TransferClaimView{}, err
	}
	return viewTransferClaim(claim), nil
}
