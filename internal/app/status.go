package app

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"
)

// The status states are the single vocabulary behind every live surface: the
// sender's records list, the success page and the recipient's page all render
// one of these words, so "可领取 / 已领完 / 已失效" cannot drift between the
// sender and the recipient side.
const (
	// TransferStatusStateDraft is a send whose files have not all arrived.
	TransferStatusStateDraft = "draft"
	// TransferStatusStateCanceled is a send the sender abandoned before
	// publishing; its content is on its way out.
	TransferStatusStateCanceled = "canceled"
	// TransferStatusStateClaimable is a published send that still has a slot to
	// hand out.
	TransferStatusStateClaimable = "claimable"
	// TransferStatusStateClaimed is a published send this caller already holds
	// a live claim on, so the page keeps showing the session it paid for.
	TransferStatusStateClaimed = "claimed"
	// TransferStatusStateExhausted is a published send whose slots are all
	// spent. Sessions opened before the last slot stay valid.
	TransferStatusStateExhausted = "exhausted"
	// TransferStatusStateExpired is a send whose lifetime is over.
	TransferStatusStateExpired = "expired"
	// TransferStatusStateRevoked is a send whose link and pickup code were
	// revoked. Revoking is not destruction, so the content stays until the
	// lifetime ends.
	TransferStatusStateRevoked = "revoked"
	// TransferStatusStateDestroyed is the terminal state of a burn-after-reading
	// send: access is refused and the bytes are being released.
	TransferStatusStateDestroyed = "destroyed"
)

// TransferStatusView is the claim state a status surface may render for one
// send. It is deliberately narrower than TransferView: it carries no file
// names, no attachment identifiers and no content, so subscribing to a status
// channel can never reveal more than the caller is already entitled to. It is
// also never the answer to a claim request, which is the only action that hands
// out content.
type TransferStatusView struct {
	TransferID string `json:"transferId"`
	// State is the one word the surface renders; see the state constants.
	State string `json:"state"`
	// Kind is "file" or "text", so the page labels the claim action correctly.
	Kind string `json:"kind,omitempty"`
	// BurnAfterReading tells the page whether this send destroys itself.
	BurnAfterReading bool `json:"burnAfterReading"`
	ClaimQuota       int  `json:"claimQuota"`
	ClaimedCount     int  `json:"claimedCount"`
	ClaimsRemaining  int  `json:"claimsRemaining"`
	// Claimed reports whether this caller holds a live claim right now.
	Claimed        bool       `json:"claimed"`
	ClaimID        string     `json:"claimId,omitempty"`
	ClaimExpiresAt *time.Time `json:"claimExpiresAt,omitempty"`
	ExpiresAt      time.Time  `json:"expiresAt"`
	// DestroyedAt, DestroyReason and CleanupStatus are the terminal-state
	// contract shared with the burn-after-reading policy: the same reason names
	// the same ending on the sender's and the recipient's side.
	DestroyedAt   *time.Time `json:"destroyedAt,omitempty"`
	DestroyReason string     `json:"destroyReason,omitempty"`
	CleanupStatus string     `json:"cleanupStatus,omitempty"`
}

// TransferRecordView is one row of the sender's own send records. It adds the
// sender-only fields — the link, the pickup code, how many files the send
// declares and the title of the record it created — to the shared status
// vocabulary.
type TransferRecordView struct {
	TransferID string `json:"transferId"`
	Title      string `json:"title,omitempty"`
	State      string `json:"state"`

	BurnAfterReading bool `json:"burnAfterReading"`
	ClaimQuota       int  `json:"claimQuota"`
	ClaimedCount     int  `json:"claimedCount"`
	ClaimsRemaining  int  `json:"claimsRemaining"`

	ItemCount  int    `json:"itemCount"`
	ShareToken string `json:"shareToken,omitempty"`
	ShareURL   string `json:"shareUrl,omitempty"`
	PickupCode string `json:"pickupCode,omitempty"`

	ExpiresAt     time.Time  `json:"expiresAt"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
	PublishedAt   *time.Time `json:"publishedAt,omitempty"`
	CanceledAt    *time.Time `json:"canceledAt,omitempty"`
	DestroyedAt   *time.Time `json:"destroyedAt,omitempty"`
	DestroyReason string     `json:"destroyReason,omitempty"`
	CleanupStatus string     `json:"cleanupStatus,omitempty"`
}

// PasteStatusView is the change marker of one record. It carries no body: the
// status channel only tells a surface that something moved, and the surface
// re-reads the record it is allowed to show through the ordinary content API.
type PasteStatusView struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// AccountStatusView is the whole account snapshot behind the account status
// stream. Every call re-reads the store, so two API instances report the same
// state and no in-memory broadcast is treated as the truth.
type AccountStatusView struct {
	Transfers []TransferRecordView `json:"transfers"`
	Pastes    []PasteStatusView    `json:"pastes"`
}

// shareState rebuilds the part of the published share the status state reads,
// from the snapshot's own fields. Only the link's presence and lifetime are
// set, so it is never passed anywhere that could read more of a share row than
// the snapshot actually fetched.
func (r TransferStatus) shareState() *Share {
	if r.Transfer.ShareID == "" {
		return nil
	}
	return &Share{
		ID:        r.Transfer.ShareID,
		Token:     r.ShareToken,
		ExpiresAt: r.ShareExpiresAt,
		RevokedAt: r.ShareRevokedAt,
	}
}

// TransferStatus is one send as a store reports it for the status snapshot: the
// transfer row, the state of the share it published and how many files it
// declares, with no attachment rows and no content.
type TransferStatus struct {
	Transfer Transfer
	// Title names the record the send created, so a records list has something
	// to show before the sender opens anything.
	Title          string
	ShareToken     string
	ShareExpiresAt time.Time
	ShareRevokedAt *time.Time
	PickupCode     string
	// CleanupStatus is the backing record's status, which is the cleanup
	// boundary the burn policy reports.
	CleanupStatus string
	ItemCount     int
}

// PasteStatus is one record's change marker.
type PasteStatus struct {
	ID        string
	Status    string
	UpdatedAt time.Time
}

// AccountStatus is the compact account state a status stream re-reads on every
// tick. A store returns it in a bounded number of queries regardless of how
// many sends or records the account has.
type AccountStatus struct {
	Transfers []TransferStatus
	Pastes    []PasteStatus
}

// AccountStatusWithContext is the authoritative snapshot behind the account
// status stream. It re-reads the store on every call, so a reconnecting client
// gets the current state instead of a replay of events it may have missed.
func (s *Service) AccountStatusWithContext(ctx context.Context, userID string) (AccountStatusView, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.activeUserLocked(ctx, userID); err != nil {
		return AccountStatusView{}, err
	}
	status, err := s.accountStatusLocked(ctx, userID)
	if err != nil {
		return AccountStatusView{}, err
	}
	now := s.now().UTC()
	view := AccountStatusView{
		Transfers: make([]TransferRecordView, 0, len(status.Transfers)),
		Pastes:    make([]PasteStatusView, 0, len(status.Pastes)),
	}
	for _, record := range status.Transfers {
		recordView, err := s.transferRecordViewLocked(ctx, record, now)
		if err != nil {
			return AccountStatusView{}, err
		}
		view.Transfers = append(view.Transfers, recordView)
	}
	for _, paste := range status.Pastes {
		view.Pastes = append(view.Pastes, PasteStatusView{
			ID:        paste.ID,
			Status:    paste.Status,
			UpdatedAt: paste.UpdatedAt,
		})
	}
	return view, nil
}

// ShareStatusWithContext is the non-consuming status read behind the share
// status stream. It answers for the share a caller already opened, or for the
// share they hold a live claim on, and it never spends a claim slot, a visit or
// a download. A caller that holds neither credential is refused, so the channel
// cannot be used to discover a share or to read somebody else's send.
//
// The page grant arrives as a decision the HTTP layer already made, because
// verifying its signature and its viewer binding needs the request cookie. The
// claim is resolved here, where claim validity lives.
func (s *Service) ShareStatusWithContext(ctx context.Context, token string, claimToken string, accessGrant bool) (TransferStatusView, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	share, err := s.shareByTokenHashLocked(ctx, tokenHash(token))
	if err != nil {
		return TransferStatusView{}, err
	}
	if share == nil {
		return TransferStatusView{}, E(http.StatusNotFound, "share_not_found", "share not found")
	}
	now := s.now().UTC()
	// The page grant is the credential an opened page holds; a live claim is
	// the credential a claimed session holds. Either one authorizes status
	// reads, and neither is handed out here.
	claim, err := s.claimForShareLocked(ctx, share, claimToken, now)
	if err != nil {
		return TransferStatusView{}, err
	}
	if !accessGrant && claim == nil {
		return TransferStatusView{}, E(http.StatusUnauthorized, "share_access_required", "open this share before reading its status")
	}
	transfer, err := s.transferForShareLocked(ctx, share.ID)
	if err != nil {
		return TransferStatusView{}, err
	}
	if transfer == nil {
		return TransferStatusView{}, E(http.StatusBadRequest, "transfer_status_unavailable", "this share is not a transfer")
	}
	return s.transferStatusViewLocked(ctx, transfer, share, claim, now)
}

// transferStatusViewLocked builds the credential-scoped status of one send. It
// reports the terminal state instead of refusing it, because a page that is
// already open has to be able to say that the send ended rather than looking
// broken.
func (s *Service) transferStatusViewLocked(ctx context.Context, transfer *Transfer, share *Share, claim *TransferClaim, now time.Time) (TransferStatusView, error) {
	state, reason, err := s.transferStateLocked(ctx, transfer, share, claim, now)
	if err != nil {
		return TransferStatusView{}, err
	}
	kind, err := s.transferClaimKindLocked(ctx, transfer)
	if err != nil {
		return TransferStatusView{}, err
	}
	view := TransferStatusView{
		TransferID:       transfer.ID,
		State:            state,
		Kind:             kind,
		BurnAfterReading: transfer.BurnAfterReading,
		ClaimQuota:       transfer.ClaimQuota,
		ClaimedCount:     transfer.ClaimedCount,
		ClaimsRemaining:  max(transfer.ClaimQuota-transfer.ClaimedCount, 0),
		ExpiresAt:        transfer.ExpiresAt,
		DestroyedAt:      transfer.DestroyedAt,
		DestroyReason:    transfer.DestroyReason,
	}
	if state == TransferStatusStateDestroyed && view.DestroyReason == "" {
		view.DestroyReason = reason
	}
	if claim != nil {
		expiresAt := claim.ExpiresAt
		view.Claimed = true
		view.ClaimID = claim.ID
		view.ClaimExpiresAt = &expiresAt
	}
	paste, err := s.pasteByIDLocked(ctx, transfer.PasteID)
	if err != nil {
		return TransferStatusView{}, err
	}
	view.CleanupStatus = paste.Status
	return view, nil
}

// transferRecordViewLocked builds one sender-facing records row.
func (s *Service) transferRecordViewLocked(ctx context.Context, record TransferStatus, now time.Time) (TransferRecordView, error) {
	transfer := record.Transfer
	// The snapshot already carries the share fields the state depends on, so a
	// records row never costs an extra share read per send.
	share := record.shareState()
	state, reason, err := s.transferStateLocked(ctx, &transfer, share, nil, now)
	if err != nil {
		return TransferRecordView{}, err
	}
	view := TransferRecordView{
		TransferID:       transfer.ID,
		Title:            record.Title,
		State:            state,
		BurnAfterReading: transfer.BurnAfterReading,
		ClaimQuota:       transfer.ClaimQuota,
		ClaimedCount:     transfer.ClaimedCount,
		ClaimsRemaining:  max(transfer.ClaimQuota-transfer.ClaimedCount, 0),
		ItemCount:        record.ItemCount,
		ShareToken:       record.ShareToken,
		PickupCode:       record.PickupCode,
		ExpiresAt:        transfer.ExpiresAt,
		CreatedAt:        transfer.CreatedAt,
		UpdatedAt:        transfer.UpdatedAt,
		PublishedAt:      transfer.PublishedAt,
		CanceledAt:       transfer.CanceledAt,
		DestroyedAt:      transfer.DestroyedAt,
		DestroyReason:    transfer.DestroyReason,
		CleanupStatus:    record.CleanupStatus,
	}
	if view.DestroyedAt == nil && state == TransferStatusStateDestroyed {
		// A send that has just become due is destroyed by the next access or
		// sweep; reporting the reason now keeps the records list from
		// promising a link that can no longer hand anything out.
		view.DestroyReason = reason
	}
	if record.ShareToken != "" {
		view.ShareURL = s.shareURLLocked(record.ShareToken)
	}
	return view, nil
}

// transferStateLocked names the one word a status surface renders for a send,
// and the reason when the send reached its terminal state. It never changes
// state: a status read must not destroy, revoke or spend anything.
func (s *Service) transferStateLocked(ctx context.Context, transfer *Transfer, share *Share, viewerClaim *TransferClaim, now time.Time) (string, string, error) {
	switch transfer.Status {
	case TransferStatusDraft:
		return TransferStatusStateDraft, "", nil
	case TransferStatusCanceled:
		return TransferStatusStateCanceled, "", nil
	case TransferStatusDestroyed:
		return TransferStatusStateDestroyed, transfer.DestroyReason, nil
	}
	// A published send is due for destruction before the worker has swept it,
	// and every access entry point already refuses it at that point, so the
	// status channel reports the same answer the next request would get.
	if reason, err := s.transferBurnDueLocked(ctx, transfer, share, now); err != nil {
		return "", "", err
	} else if reason != "" {
		return TransferStatusStateDestroyed, reason, nil
	}
	if share == nil || share.RevokedAt != nil {
		return TransferStatusStateRevoked, "", nil
	}
	if !share.ExpiresAt.After(now) || !transfer.ExpiresAt.After(now) {
		return TransferStatusStateExpired, "", nil
	}
	if viewerClaim != nil {
		return TransferStatusStateClaimed, "", nil
	}
	if transfer.ClaimedCount >= transfer.ClaimQuota {
		return TransferStatusStateExhausted, "", nil
	}
	return TransferStatusStateClaimable, "", nil
}

// accountStatusLocked reads the compact account state. The store path is one
// query per table; the in-memory path answers from the service maps, which is
// the same state the single-process mode serves everywhere else.
func (s *Service) accountStatusLocked(ctx context.Context, userID string) (AccountStatus, error) {
	if s.content.AccountStatus != nil {
		store := s.content.AccountStatus
		s.mu.Unlock()
		status, err := store.AccountStatus(ctx, userID)
		s.mu.Lock()
		if err != nil {
			return AccountStatus{}, err
		}
		return status, nil
	}
	status := AccountStatus{Transfers: []TransferStatus{}, Pastes: []PasteStatus{}}
	for _, transfer := range s.transfersByID {
		if transfer.UserID != userID {
			continue
		}
		record := TransferStatus{Transfer: *transfer, ItemCount: s.memoryTransferItemCountLocked(transfer.ID)}
		if transfer.ShareID != "" {
			if share := s.sharesByID[transfer.ShareID]; share != nil {
				record.ShareToken = share.Token
				record.ShareExpiresAt = share.ExpiresAt
				record.ShareRevokedAt = share.RevokedAt
				record.PickupCode = share.PickupCode
			}
		}
		if paste := s.pastesByID[transfer.PasteID]; paste != nil {
			record.Title = paste.Title
			record.CleanupStatus = paste.Status
		}
		status.Transfers = append(status.Transfers, record)
	}
	sort.Slice(status.Transfers, func(i, j int) bool {
		if !status.Transfers[i].Transfer.CreatedAt.Equal(status.Transfers[j].Transfer.CreatedAt) {
			return status.Transfers[i].Transfer.CreatedAt.After(status.Transfers[j].Transfer.CreatedAt)
		}
		return status.Transfers[i].Transfer.ID > status.Transfers[j].Transfer.ID
	})
	for _, paste := range s.pastesByID {
		if paste.UserID != userID {
			continue
		}
		status.Pastes = append(status.Pastes, PasteStatus{ID: paste.ID, Status: paste.Status, UpdatedAt: paste.UpdatedAt})
	}
	sort.Slice(status.Pastes, func(i, j int) bool {
		if !status.Pastes[i].UpdatedAt.Equal(status.Pastes[j].UpdatedAt) {
			return status.Pastes[i].UpdatedAt.After(status.Pastes[j].UpdatedAt)
		}
		return status.Pastes[i].ID > status.Pastes[j].ID
	})
	return status, nil
}

// memoryTransferItemCountLocked counts a send's declared files in the
// in-memory mode, where the items live in a map keyed by transfer and item id.
func (s *Service) memoryTransferItemCountLocked(transferID string) int {
	count := 0
	for key := range s.transferItems {
		if strings.HasPrefix(key, transferID+"\x00") {
			count++
		}
	}
	return count
}
