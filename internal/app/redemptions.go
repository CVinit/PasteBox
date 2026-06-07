package app

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"pastebox/internal/plans"
)

type RedemptionBatch struct {
	ID                    string     `json:"id"`
	PlanID                string     `json:"planId"`
	DurationDays          int        `json:"durationDays"`
	Quantity              int        `json:"quantity"`
	ExpiresAt             *time.Time `json:"expiresAt,omitempty"`
	MaxTotalRedemptions   int        `json:"maxTotalRedemptions"`
	MaxRedemptionsPerUser int        `json:"maxRedemptionsPerUser"`
	AllowedEmails         []string   `json:"allowedEmails,omitempty"`
	AllowedDomains        []string   `json:"allowedDomains,omitempty"`
	Note                  string     `json:"note,omitempty"`
	Disabled              bool       `json:"disabled"`
	RedeemedCount         int        `json:"redeemedCount"`
	CreatedAt             time.Time  `json:"createdAt"`
	UpdatedAt             time.Time  `json:"updatedAt"`
}

type RedemptionCode struct {
	CodeHash   string     `json:"-"`
	Code       string     `json:"code,omitempty"`
	BatchID    string     `json:"batchId"`
	RedeemedBy string     `json:"redeemedBy,omitempty"`
	RedeemedAt *time.Time `json:"redeemedAt,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
}

type RedemptionRecord struct {
	ID        string    `json:"id"`
	CodeHash  string    `json:"-"`
	BatchID   string    `json:"batchId"`
	UserID    string    `json:"userId"`
	PlanID    string    `json:"planId"`
	CreatedAt time.Time `json:"createdAt"`
}

type RedemptionBatchInput struct {
	PlanID                string
	DurationDays          int
	Quantity              int
	ExpiresAt             *time.Time
	MaxTotalRedemptions   int
	MaxRedemptionsPerUser int
	AllowedEmails         []string
	AllowedDomains        []string
	Note                  string
	Disabled              bool
}

type RedemptionBatchView struct {
	RedemptionBatch
	Codes []RedemptionCode `json:"codes,omitempty"`
}

type RedemptionStore interface {
	CreateRedemptionBatch(ctx context.Context, batch RedemptionBatch, codes []RedemptionCode) error
	UpdateRedemptionBatch(ctx context.Context, batch RedemptionBatch) error
	UpdateRedemptionCode(ctx context.Context, code RedemptionCode) error
	CreateRedemptionRecord(ctx context.Context, record RedemptionRecord) error
	ListRedemptionBatches(ctx context.Context) ([]RedemptionBatch, error)
	ListRedemptionCodes(ctx context.Context) ([]RedemptionCode, error)
	ListRedemptionRecords(ctx context.Context) ([]RedemptionRecord, error)
}

func (s *Service) loadRedemptionCaches(ctx context.Context) error {
	if s.redemptions == nil {
		return nil
	}
	batches, err := s.redemptions.ListRedemptionBatches(ctx)
	if err != nil {
		return fmt.Errorf("load redemption batches: %w", err)
	}
	s.redemptionBatches = map[string]*RedemptionBatch{}
	for _, batch := range batches {
		s.cacheRedemptionBatchLocked(batch)
	}
	codes, err := s.redemptions.ListRedemptionCodes(ctx)
	if err != nil {
		return fmt.Errorf("load redemption codes: %w", err)
	}
	s.redemptionCodesByHash = map[string]*RedemptionCode{}
	for _, code := range codes {
		s.cacheRedemptionCodeLocked(code)
	}
	records, err := s.redemptions.ListRedemptionRecords(ctx)
	if err != nil {
		return fmt.Errorf("load redemption records: %w", err)
	}
	s.redemptionRecords = s.redemptionRecords[:0]
	for _, record := range records {
		s.cacheRedemptionRecordLocked(record)
	}
	return nil
}

func (s *Service) AdminCreateRedemptionBatch(actorID string, input RedemptionBatchInput) (RedemptionBatchView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireAdminLocked(actorID); err != nil {
		return RedemptionBatchView{}, err
	}
	batch, codes, err := s.buildRedemptionBatchLocked(input)
	if err != nil {
		return RedemptionBatchView{}, err
	}
	if s.redemptions != nil {
		if err := s.redemptions.CreateRedemptionBatch(context.Background(), batch, codes); err != nil {
			return RedemptionBatchView{}, err
		}
	}
	s.cacheRedemptionBatchLocked(batch)
	for _, code := range codes {
		s.cacheRedemptionCodeLocked(code)
	}
	if err := s.auditLocked(actorID, "admin.redemption_batch_create", batch.ID, map[string]any{
		"planId":       batch.PlanID,
		"quantity":     batch.Quantity,
		"durationDays": batch.DurationDays,
	}); err != nil {
		return RedemptionBatchView{}, err
	}
	return RedemptionBatchView{RedemptionBatch: batch, Codes: codes}, nil
}

func (s *Service) AdminListRedemptionBatches(actorID string) ([]RedemptionBatchView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireAdminLocked(actorID); err != nil {
		return nil, err
	}
	out := make([]RedemptionBatchView, 0, len(s.redemptionBatches))
	for _, batch := range s.redemptionBatches {
		view := RedemptionBatchView{RedemptionBatch: cloneRedemptionBatch(*batch)}
		for _, code := range s.redemptionCodesByHash {
			if code.BatchID == batch.ID {
				view.Codes = append(view.Codes, cloneRedemptionCode(*code, false))
			}
		}
		sort.Slice(view.Codes, func(i, j int) bool {
			return view.Codes[i].CreatedAt.Before(view.Codes[j].CreatedAt)
		})
		out = append(out, view)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

func (s *Service) AdminUpdateRedemptionBatch(actorID string, batchID string, disabled bool, note string) (RedemptionBatchView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireAdminLocked(actorID); err != nil {
		return RedemptionBatchView{}, err
	}
	batch := s.redemptionBatches[strings.TrimSpace(batchID)]
	if batch == nil {
		return RedemptionBatchView{}, E(http.StatusNotFound, "redemption_batch_not_found", "redemption batch not found")
	}
	batch.Disabled = disabled
	if strings.TrimSpace(note) != "" {
		batch.Note = strings.TrimSpace(note)
	}
	batch.UpdatedAt = s.now().UTC()
	if s.redemptions != nil {
		if err := s.redemptions.UpdateRedemptionBatch(context.Background(), *batch); err != nil {
			return RedemptionBatchView{}, err
		}
	}
	s.cacheRedemptionBatchLocked(*batch)
	if err := s.auditLocked(actorID, "admin.redemption_batch_update", batch.ID, map[string]any{"disabled": disabled}); err != nil {
		return RedemptionBatchView{}, err
	}
	return RedemptionBatchView{RedemptionBatch: cloneRedemptionBatch(*batch)}, nil
}

func (s *Service) RedeemCode(userID string, rawCode string) (UserView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, err := s.activeUserLocked(userID)
	if err != nil {
		return UserView{}, err
	}
	code := s.redemptionCodesByHash[tokenHash(strings.TrimSpace(rawCode))]
	if code == nil {
		return UserView{}, E(http.StatusNotFound, "redemption_code_invalid", "redemption code is invalid")
	}
	batch := s.redemptionBatches[code.BatchID]
	if batch == nil {
		return UserView{}, E(http.StatusNotFound, "redemption_code_invalid", "redemption code is invalid")
	}
	if err := s.validateRedemptionLocked(user, batch, code); err != nil {
		return UserView{}, err
	}
	now := s.now().UTC()
	code.RedeemedBy = user.ID
	code.RedeemedAt = &now
	batch.RedeemedCount++
	batch.UpdatedAt = now
	record := RedemptionRecord{ID: s.newID("red"), CodeHash: code.CodeHash, BatchID: batch.ID, UserID: user.ID, PlanID: batch.PlanID, CreatedAt: now}
	user.PlanID = batch.PlanID
	expires := now.Add(time.Duration(batch.DurationDays) * 24 * time.Hour)
	if user.PlanExpiresAt != nil && user.PlanExpiresAt.After(now) {
		expires = user.PlanExpiresAt.Add(time.Duration(batch.DurationDays) * 24 * time.Hour)
	}
	user.PlanExpiresAt = &expires
	user.UpdatedAt = now
	if s.redemptions != nil {
		if err := s.redemptions.UpdateRedemptionCode(context.Background(), *code); err != nil {
			return UserView{}, err
		}
		if err := s.redemptions.UpdateRedemptionBatch(context.Background(), *batch); err != nil {
			return UserView{}, err
		}
		if err := s.redemptions.CreateRedemptionRecord(context.Background(), record); err != nil {
			return UserView{}, err
		}
	}
	if err := s.updateUserLocked(user); err != nil {
		return UserView{}, err
	}
	s.cacheRedemptionCodeLocked(*code)
	s.cacheRedemptionBatchLocked(*batch)
	s.cacheRedemptionRecordLocked(record)
	if err := s.auditLocked(user.ID, "billing.redemption_redeemed", batch.ID, map[string]any{
		"planId":       batch.PlanID,
		"durationDays": batch.DurationDays,
	}); err != nil {
		return UserView{}, err
	}
	return s.viewUserLocked(user)
}

func (s *Service) buildRedemptionBatchLocked(input RedemptionBatchInput) (RedemptionBatch, []RedemptionCode, error) {
	planID := strings.ToLower(strings.TrimSpace(input.PlanID))
	if _, ok := plans.Find(s.catalog, planID); !ok {
		return RedemptionBatch{}, nil, E(http.StatusBadRequest, "invalid_plan", "plan does not exist")
	}
	if input.DurationDays <= 0 {
		return RedemptionBatch{}, nil, E(http.StatusBadRequest, "invalid_redemption_duration", "redemption duration must be positive")
	}
	if input.Quantity <= 0 || input.Quantity > 1000 {
		return RedemptionBatch{}, nil, E(http.StatusBadRequest, "invalid_redemption_quantity", "redemption quantity must be between 1 and 1000")
	}
	now := s.now().UTC()
	batch := RedemptionBatch{
		ID:                    s.newID("rb"),
		PlanID:                planID,
		DurationDays:          input.DurationDays,
		Quantity:              input.Quantity,
		ExpiresAt:             input.ExpiresAt,
		MaxTotalRedemptions:   input.MaxTotalRedemptions,
		MaxRedemptionsPerUser: input.MaxRedemptionsPerUser,
		AllowedEmails:         normalizeEmailList(input.AllowedEmails),
		AllowedDomains:        normalizeDomainList(input.AllowedDomains),
		Note:                  strings.TrimSpace(input.Note),
		Disabled:              input.Disabled,
		CreatedAt:             now,
		UpdatedAt:             now,
	}
	if batch.MaxTotalRedemptions <= 0 || batch.MaxTotalRedemptions > batch.Quantity {
		batch.MaxTotalRedemptions = batch.Quantity
	}
	if batch.MaxRedemptionsPerUser <= 0 {
		batch.MaxRedemptionsPerUser = 1
	}
	codes := make([]RedemptionCode, 0, batch.Quantity)
	for len(codes) < batch.Quantity {
		raw := strings.ToUpper("PB-" + strings.ReplaceAll(newToken()[:18], "_", "A"))
		hash := tokenHash(raw)
		if _, exists := s.redemptionCodesByHash[hash]; exists {
			continue
		}
		codes = append(codes, RedemptionCode{CodeHash: hash, Code: raw, BatchID: batch.ID, CreatedAt: now})
	}
	return batch, codes, nil
}

func (s *Service) validateRedemptionLocked(user *User, batch *RedemptionBatch, code *RedemptionCode) error {
	now := s.now().UTC()
	if batch.Disabled {
		return E(http.StatusForbidden, "redemption_batch_disabled", "redemption batch is disabled")
	}
	if batch.ExpiresAt != nil && !batch.ExpiresAt.After(now) {
		return E(http.StatusGone, "redemption_batch_expired", "redemption batch is expired")
	}
	if code.RedeemedAt != nil || code.RedeemedBy != "" {
		return E(http.StatusConflict, "redemption_code_used", "redemption code has already been used")
	}
	if batch.RedeemedCount >= batch.MaxTotalRedemptions {
		return E(http.StatusConflict, "redemption_batch_limit", "redemption batch redemption limit reached")
	}
	if len(batch.AllowedEmails) > 0 && !contains(batch.AllowedEmails, normalizeEmail(user.Email)) {
		return E(http.StatusForbidden, "redemption_email_not_allowed", "redemption code is not valid for this email")
	}
	if len(batch.AllowedDomains) > 0 {
		domain := emailDomain(user.Email)
		if !contains(batch.AllowedDomains, domain) {
			return E(http.StatusForbidden, "redemption_domain_not_allowed", "redemption code is not valid for this email domain")
		}
	}
	userCount := 0
	for _, record := range s.redemptionRecords {
		if record != nil && record.BatchID == batch.ID && record.UserID == user.ID {
			userCount++
		}
	}
	if userCount >= batch.MaxRedemptionsPerUser {
		return E(http.StatusConflict, "redemption_user_limit", "user redemption limit reached")
	}
	return nil
}

func (s *Service) cacheRedemptionBatchLocked(batch RedemptionBatch) *RedemptionBatch {
	cached := cloneRedemptionBatch(batch)
	s.redemptionBatches[cached.ID] = &cached
	return &cached
}

func (s *Service) cacheRedemptionCodeLocked(code RedemptionCode) *RedemptionCode {
	cached := cloneRedemptionCode(code, true)
	s.redemptionCodesByHash[cached.CodeHash] = &cached
	return &cached
}

func (s *Service) cacheRedemptionRecordLocked(record RedemptionRecord) *RedemptionRecord {
	cached := record
	for i, existing := range s.redemptionRecords {
		if existing.ID == cached.ID {
			s.redemptionRecords[i] = &cached
			return &cached
		}
	}
	s.redemptionRecords = append(s.redemptionRecords, &cached)
	return &cached
}

func cloneRedemptionBatch(batch RedemptionBatch) RedemptionBatch {
	batch.AllowedEmails = append([]string(nil), batch.AllowedEmails...)
	batch.AllowedDomains = append([]string(nil), batch.AllowedDomains...)
	return batch
}

func cloneRedemptionCode(code RedemptionCode, includePlain bool) RedemptionCode {
	if !includePlain {
		code.Code = ""
	}
	return code
}

func normalizeEmailList(values []string) []string {
	out := []string{}
	seen := map[string]struct{}{}
	for _, value := range values {
		email := normalizeEmail(value)
		if email == "" || !strings.Contains(email, "@") {
			continue
		}
		if _, ok := seen[email]; ok {
			continue
		}
		seen[email] = struct{}{}
		out = append(out, email)
	}
	sort.Strings(out)
	return out
}

func normalizeDomainList(values []string) []string {
	out := []string{}
	seen := map[string]struct{}{}
	for _, value := range values {
		domain := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(value), "@"))
		if domain == "" || !strings.Contains(domain, ".") {
			continue
		}
		if _, ok := seen[domain]; ok {
			continue
		}
		seen[domain] = struct{}{}
		out = append(out, domain)
	}
	sort.Strings(out)
	return out
}

func emailDomain(email string) string {
	_, domain, ok := strings.Cut(normalizeEmail(email), "@")
	if !ok {
		return ""
	}
	return domain
}
