package app

import (
	"context"
	"io"
	"net/http"
	"time"
)

const (
	// TransferDestroyReasonClaimsEnded is the reason recorded when every claim
	// slot was spent and every claim session had ended.
	TransferDestroyReasonClaimsEnded = "claims_ended"
	// TransferDestroyReasonExpired is the reason recorded when the share
	// lifetime ran out before every slot was spent.
	TransferDestroyReasonExpired = "expired"
	// transferBurnSweepLimit bounds one sweep, so the query that keeps
	// destruction reliable without traffic stays small.
	transferBurnSweepLimit = 100
	// transferStreamCheckTimeout bounds one mid-download re-check, so a slow
	// store cannot stall a download that is otherwise fine.
	transferStreamCheckTimeout = 5 * time.Second
)

// TransferStreamRecheckInterval bounds how long a download may keep streaming
// after the claim session, share or transfer that authorized it stopped being
// valid. A stream that is already open is stopped at the next checkpoint
// instead of being allowed to finish on a decision that was only true when the
// download started. Bytes already handed to the client are not recalled, and a
// copy the recipient already saved stays theirs.
const TransferStreamRecheckInterval = 5 * time.Second

func transferDestroyedError() *Error {
	return E(http.StatusGone, "transfer_destroyed", "this transfer was destroyed and its content is being removed")
}

// transferBurnDueLocked reports why a burn-after-reading send has to be
// destroyed now, or an empty reason when it must stay. A send is due when its
// share lifetime is over, or when every claim slot was spent and no session is
// still open. A send that still has a slot to hand out keeps its content until
// its own lifetime ends, so a first reader never takes away what a later claim
// still needs.
//
// Revoking the share is deliberately not a destroy condition: the approved
// conditions are the claims ending and the lifetime ending, and destruction is
// irreversible, so a revoked send keeps its content until its lifetime would
// have ended anyway.
func (s *Service) transferBurnDueLocked(ctx context.Context, transfer *Transfer, share *Share, now time.Time) (string, error) {
	live := false
	if transfer != nil && transfer.BurnAfterReading && transfer.Status == TransferStatusPublished && share != nil && share.ExpiresAt.After(now) && transfer.ClaimedCount >= transfer.ClaimQuota {
		count, err := s.countLiveTransferClaimsLocked(ctx, transfer.ID, now)
		if err != nil {
			return "", err
		}
		live = count > 0
	}
	return transferBurnReason(transfer, share, now, live), nil
}

func transferBurnReason(transfer *Transfer, share *Share, now time.Time, hasLiveClaims bool) string {
	if transfer == nil || !transfer.BurnAfterReading || transfer.Status != TransferStatusPublished || share == nil {
		return ""
	}
	if !share.ExpiresAt.After(now) {
		return TransferDestroyReasonExpired
	}
	if transfer.ClaimedCount >= transfer.ClaimQuota && !hasLiveClaims {
		return TransferDestroyReasonClaimsEnded
	}
	return ""
}

// countLiveTransferClaimsLocked counts the claim sessions that are still open.
// A session that was completed or whose window ran out is over, so it no longer
// holds the send open.
func (s *Service) countLiveTransferClaimsLocked(ctx context.Context, transferID string, now time.Time) (int, error) {
	if s.content.Transfers != nil {
		s.mu.Unlock()
		live, err := s.content.Transfers.CountLiveTransferClaims(ctx, transferID, now)
		s.mu.Lock()
		if err != nil {
			return 0, err
		}
		return live, nil
	}
	live := 0
	for _, claim := range s.claimsByID {
		if claim.TransferID == transferID && claim.Status == TransferClaimStatusActive && claim.ExpiresAt.After(now) {
			live++
		}
	}
	return live, nil
}

// maybeDestroyBurnTransferLocked destroys a burn-after-reading send when it is
// due. It reports what went wrong rather than swallowing it, so a caller that
// just ended the last session can retry instead of leaving the send behind.
func (s *Service) maybeDestroyBurnTransferLocked(ctx context.Context, transfer *Transfer, share *Share, now time.Time) error {
	reason, err := s.transferBurnDueLocked(ctx, transfer, share, now)
	if err != nil || reason == "" {
		return err
	}
	return s.destroyTransferLocked(ctx, transfer, reason, now)
}

// refuseDestroyedByTokenLocked is the entry-point guard for a recipient
// holding a share token: it resolves the share and refuses the request when its
// send can no longer hand out content.
func (s *Service) refuseDestroyedByTokenLocked(ctx context.Context, token string) error {
	share, err := s.shareByTokenHashLocked(ctx, tokenHash(token))
	if err != nil {
		return err
	}
	return s.refuseDestroyedTransferLocked(ctx, share)
}

// refuseDestroyedTransferLocked answers the terminal state of a burn-after-reading
// send a caller is about to use, and destroys it first when it has just become
// due. The answer is therefore the same whether the worker sweep already ran or
// not: a send that can no longer hand out content is refused with its own code
// instead of a generic expiry.
func (s *Service) refuseDestroyedTransferLocked(ctx context.Context, share *Share) error {
	if share == nil {
		return nil
	}
	transfer, err := s.transferForShareLocked(ctx, share.ID)
	if err != nil || transfer == nil {
		return err
	}
	if transfer.Status == TransferStatusDestroyed {
		return transferDestroyedError()
	}
	if err := s.maybeDestroyBurnTransferLocked(ctx, transfer, share, s.now().UTC()); err != nil {
		return err
	}
	if transfer.Status == TransferStatusDestroyed {
		return transferDestroyedError()
	}
	return nil
}

// maybeDestroyAfterClaimEndedLocked gives a burn-after-reading send its
// destruction attempt once a claim session has ended, so the send that just
// handed out its last content is destroyed without waiting for the sweep.
func (s *Service) maybeDestroyAfterClaimEndedLocked(ctx context.Context, claim *TransferClaim) error {
	if claim == nil {
		return nil
	}
	share, err := s.shareByIDLocked(ctx, claim.ShareID)
	if err != nil {
		if isAppStatus(err, http.StatusNotFound) {
			return nil
		}
		return err
	}
	transfer, err := s.transferForShareLocked(ctx, share.ID)
	if err != nil || transfer == nil {
		return err
	}
	return s.maybeDestroyBurnTransferLocked(ctx, transfer, share, s.now().UTC())
}

// destroyTransferLocked commits a burn-after-reading send's terminal state and
// only then lets the background cleanup release the bytes. Access is refused
// from the committed state, so a cleanup that fails cannot reopen the send: the
// job is retried, the content is not restored.
//
// A store that can destroy a send atomically does the denial, the share
// revocation and the cleanup intent in one transaction. Otherwise the same
// order is kept step by step, terminal state first.
func (s *Service) destroyTransferLocked(ctx context.Context, transfer *Transfer, reason string, now time.Time) error {
	if transfer == nil || transfer.Status != TransferStatusPublished {
		return nil
	}
	if store, ok := s.content.Transfers.(AtomicTransferDestroyStore); ok {
		transferID := transfer.ID
		cleanupJobID := s.newID("job")
		s.mu.Unlock()
		updated, destroyed, err := store.DestroyTransfer(ctx, transferID, cleanupJobID, now, reason)
		s.mu.Lock()
		if err != nil {
			if isStoreNotFound(err) {
				// The send and its content are already gone.
				return nil
			}
			return err
		}
		*transfer = *s.cacheTransferLocked(updated)
		if !destroyed {
			return nil
		}
		if updated.ShareID != "" {
			// The transaction revoked the share; reload it so this process
			// answers with the revoked row instead of a stale one.
			if _, err := s.shareByIDLocked(ctx, updated.ShareID); err != nil && !isAppStatus(err, http.StatusNotFound) {
				return err
			}
		}
		return nil
	}

	transfer.Status = TransferStatusDestroyed
	transfer.DestroyedAt = &now
	transfer.DestroyReason = reason
	transfer.UpdatedAt = now
	if err := s.updateTransferLocked(ctx, transfer); err != nil {
		return err
	}
	if transfer.ShareID != "" {
		share, err := s.shareByIDLocked(ctx, transfer.ShareID)
		if err != nil {
			if !isAppStatus(err, http.StatusNotFound) {
				return err
			}
		} else if share.RevokedAt == nil {
			share.RevokedAt = &now
			if err := s.updateShareLocked(ctx, share); err != nil {
				return err
			}
		}
	}
	paste, err := s.pasteByIDLocked(ctx, transfer.PasteID)
	if err != nil {
		if isAppStatus(err, http.StatusNotFound) {
			return nil
		}
		return err
	}
	// The content is marked for deletion and the cleanup job is queued, which
	// is what actually releases the bytes and the object references.
	return s.discardTransferPasteLocked(ctx, paste, now)
}

// RunTransferBurnSweepWithContext destroys every burn-after-reading send that
// is due. It is what keeps destruction reliable when nobody touches the send
// again: a session that simply expired, or a lifetime that ran out, still ends
// with the content released. It reports how many sends it destroyed.
func (s *Service) RunTransferBurnSweepWithContext(ctx context.Context) (int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now().UTC()
	transfers, err := s.listBurnableTransfersLocked(ctx, now)
	if err != nil {
		return 0, err
	}
	destroyed := 0
	for i := range transfers {
		transfer := &transfers[i]
		share, err := s.shareByIDLocked(ctx, transfer.ShareID)
		if err != nil {
			if isAppStatus(err, http.StatusNotFound) {
				// The content is already gone, so there is nothing to release.
				continue
			}
			return destroyed, err
		}
		reason, err := s.transferBurnDueLocked(ctx, transfer, share, now)
		if err != nil {
			return destroyed, err
		}
		if reason == "" {
			continue
		}
		if err := s.destroyTransferLocked(ctx, transfer, reason, now); err != nil {
			return destroyed, err
		}
		destroyed++
	}
	return destroyed, nil
}

func (s *Service) listBurnableTransfersLocked(ctx context.Context, now time.Time) ([]Transfer, error) {
	if s.content.Transfers != nil {
		s.mu.Unlock()
		loaded, err := s.content.Transfers.ListBurnableTransfers(ctx, now, transferBurnSweepLimit)
		s.mu.Lock()
		if err != nil {
			return nil, err
		}
		out := make([]Transfer, 0, len(loaded))
		for _, transfer := range loaded {
			out = append(out, *s.cacheTransferLocked(transfer))
		}
		return out, nil
	}
	out := []Transfer{}
	for _, transfer := range s.transfersByID {
		if transfer.BurnAfterReading && transfer.Status == TransferStatusPublished {
			out = append(out, *transfer)
		}
	}
	return out, nil
}

// transferStreamGuard re-checks, while bytes are being written, that the claim
// session behind a download is still live. Ending a session, expiring or
// revoking the share, or destroying the send from another instance therefore
// stops the stream at the next checkpoint instead of letting it finish on a
// decision that was only true when the download started.
type transferStreamGuard struct {
	body     io.ReadCloser
	check    func() error
	interval time.Duration
	now      func() time.Time
	last     time.Time
	failed   error
}

func (g *transferStreamGuard) Read(p []byte) (int, error) {
	if g.failed != nil {
		return 0, g.failed
	}
	if !g.now().Before(g.last.Add(g.interval)) {
		if err := g.check(); err != nil {
			g.failed = err
			_ = g.body.Close()
			return 0, err
		}
		g.last = g.now()
	}
	return g.body.Read(p)
}

func (g *transferStreamGuard) Close() error {
	return g.body.Close()
}

// guardTransferStream wraps a transfer-backed download body so it stops when
// the session that authorized it ends. Legacy shares keep streaming as before:
// they never had a claim session to lose.
func (s *Service) guardTransferStream(ctx context.Context, shareToken string, claimToken string, viewerUserID string, body io.ReadCloser) io.ReadCloser {
	if body == nil {
		return nil
	}
	guard := &transferStreamGuard{
		body:     body,
		interval: TransferStreamRecheckInterval,
		now:      s.now,
	}
	guard.last = guard.now()
	guard.check = func() error {
		// The request context is the right one here: if the client is gone the
		// check fails, and the stream stops, which is what a dead reader wants.
		checkCtx, cancel := context.WithTimeout(ctx, transferStreamCheckTimeout)
		defer cancel()
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, _, _, err := s.validTransferClaimLocked(checkCtx, shareToken, claimToken, viewerUserID); err != nil {
			return err
		}
		return nil
	}
	return guard
}
