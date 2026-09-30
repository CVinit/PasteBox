package httpserver

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"pastebox/internal/app"
)

func (s *Server) createTransfer(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var req struct {
		IdempotencyKey   string                  `json:"idempotencyKey"`
		ExpiresInSeconds int64                   `json:"expiresInSeconds"`
		Password         string                  `json:"password"`
		LoginRequired    bool                    `json:"loginRequired"`
		ClaimQuota       int                     `json:"claimQuota"`
		Title            string                  `json:"title"`
		Text             string                  `json:"text"`
		Tags             []string                `json:"tags"`
		Items            []app.TransferItemInput `json:"items"`
	}
	if !s.decode(w, r, &req) {
		return
	}
	view, err := s.app.CreateTransferWithContext(r.Context(), user.ID, app.TransferInput{
		IdempotencyKey:   req.IdempotencyKey,
		ExpiresInSeconds: req.ExpiresInSeconds,
		Password:         req.Password,
		LoginRequired:    req.LoginRequired,
		ClaimQuota:       req.ClaimQuota,
		Title:            req.Title,
		Text:             req.Text,
		Tags:             req.Tags,
		Items:            req.Items,
	})
	if s.handleErr(w, err) {
		return
	}
	s.logger.Debug("transfer created", "user_id", user.ID, "transfer_id", view.ID, "item_count", len(view.Items))
	writeJSON(w, http.StatusCreated, map[string]any{"transfer": view})
}

func (s *Server) createGuestTransfer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		GuestToken       string                  `json:"guestToken"`
		TurnstileToken   string                  `json:"turnstileToken"`
		IdempotencyKey   string                  `json:"idempotencyKey"`
		ExpiresInSeconds int64                   `json:"expiresInSeconds"`
		Password         string                  `json:"password"`
		LoginRequired    bool                    `json:"loginRequired"`
		ClaimQuota       int                     `json:"claimQuota"`
		Title            string                  `json:"title"`
		Text             string                  `json:"text"`
		Items            []app.TransferItemInput `json:"items"`
	}
	if !s.decode(w, r, &req) {
		return
	}
	token, view, err := s.app.CreateGuestTransferWithContext(r.Context(), app.GuestCreateTransferInput{
		Token:            firstNonEmpty(req.GuestToken, guestTokenFromRequest(r)),
		TurnstileToken:   firstNonEmpty(req.TurnstileToken, turnstileTokenFromRequest(r)),
		RemoteIP:         s.clientIP(r),
		IdempotencyKey:   req.IdempotencyKey,
		ExpiresInSeconds: req.ExpiresInSeconds,
		Password:         req.Password,
		LoginRequired:    req.LoginRequired,
		ClaimQuota:       req.ClaimQuota,
		Title:            req.Title,
		Text:             req.Text,
		Items:            req.Items,
	})
	if s.handleErr(w, err) {
		return
	}
	s.logger.Debug("guest transfer created", "transfer_id", view.ID, "item_count", len(view.Items))
	writeJSON(w, http.StatusCreated, map[string]any{"guestToken": token, "transfer": view})
}

func (s *Server) getTransfer(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	view, err := s.app.GetTransferWithContext(r.Context(), user.ID, chi.URLParam(r, "transferID"))
	if s.handleErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"transfer": view})
}

func (s *Server) listTransfers(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	transfers, err := s.app.ListTransfersWithContext(r.Context(), user.ID)
	if s.handleErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"transfers": transfers})
}

func (s *Server) uploadTransferItem(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	transferID := chi.URLParam(r, "transferID")
	itemID := chi.URLParam(r, "itemID")
	preflight, err := s.app.PreflightTransferItemUploadWithContext(r.Context(), user.ID, transferID, itemID)
	if s.handleErr(w, err) {
		return
	}
	upload, _, err := readAttachmentMultipart(r, preflight.MaxBytes)
	if err != nil {
		if s.handleErr(w, explainUploadLimit(preflight.LimitExceeded, err)) {
			return
		}
		return
	}
	defer upload.Close()
	view, attachment, err := s.app.AddPreparedTransferItemWithContext(r.Context(), preflight, upload)
	if s.handleErr(w, err) {
		return
	}
	s.logger.Debug("transfer item uploaded", "user_id", user.ID, "transfer_id", view.ID, "item_id", itemID, "attachment_id", attachment.ID, "size", attachment.Size)
	writeJSON(w, http.StatusCreated, map[string]any{"transfer": view, "attachment": attachment})
}

func (s *Server) uploadGuestTransferItem(w http.ResponseWriter, r *http.Request) {
	transferID := chi.URLParam(r, "transferID")
	itemID := chi.URLParam(r, "itemID")
	var preflight app.TransferItemUploadPreflight
	upload, err := guestUploadMultipart(r, func(token string, turnstileToken string) (int64, error) {
		resolved, resolveErr := s.app.PreflightGuestTransferItemUpload(r.Context(), token, transferID, itemID, turnstileToken, s.clientIP(r))
		if resolveErr != nil {
			return 0, resolveErr
		}
		preflight = resolved
		return resolved.MaxBytes, nil
	})
	if err != nil {
		if s.handleErr(w, explainUploadLimit(preflight.LimitExceeded, err)) {
			return
		}
		return
	}
	defer upload.Close()
	view, attachment, err := s.app.AddPreparedTransferItemWithContext(r.Context(), preflight, upload)
	if s.handleErr(w, err) {
		return
	}
	s.logger.Debug("guest transfer item uploaded", "transfer_id", view.ID, "item_id", itemID, "attachment_id", attachment.ID, "size", attachment.Size)
	writeJSON(w, http.StatusCreated, map[string]any{"transfer": view, "attachment": attachment})
}

func (s *Server) publishTransfer(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	view, err := s.app.PublishTransferWithContext(r.Context(), user.ID, chi.URLParam(r, "transferID"))
	if s.handleErr(w, err) {
		return
	}
	s.logger.Debug("transfer published", "user_id", user.ID, "transfer_id", view.ID)
	writeJSON(w, http.StatusOK, map[string]any{"transfer": view})
}

func (s *Server) publishGuestTransfer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		GuestToken string `json:"guestToken"`
	}
	if !s.decodeOptionalLimited(w, r, &req, shareAccessBodyLimitBytes) {
		return
	}
	view, err := s.app.PublishGuestTransferWithContext(r.Context(),
		firstNonEmpty(req.GuestToken, guestTokenFromRequest(r)),
		chi.URLParam(r, "transferID"),
	)
	if s.handleErr(w, err) {
		return
	}
	s.logger.Debug("guest transfer published", "transfer_id", view.ID)
	writeJSON(w, http.StatusOK, map[string]any{"transfer": view})
}

func (s *Server) cancelTransfer(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	view, err := s.app.CancelTransferWithContext(r.Context(), user.ID, chi.URLParam(r, "transferID"))
	if s.handleErr(w, err) {
		return
	}
	s.logger.Debug("transfer canceled", "user_id", user.ID, "transfer_id", view.ID)
	writeJSON(w, http.StatusOK, map[string]any{"transfer": view})
}

func (s *Server) cancelGuestTransfer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		GuestToken string `json:"guestToken"`
	}
	if !s.decodeOptionalLimited(w, r, &req, shareAccessBodyLimitBytes) {
		return
	}
	view, err := s.app.CancelGuestTransferWithContext(r.Context(),
		firstNonEmpty(req.GuestToken, guestTokenFromRequest(r)),
		chi.URLParam(r, "transferID"),
	)
	if s.handleErr(w, err) {
		return
	}
	s.logger.Debug("guest transfer canceled", "transfer_id", view.ID)
	writeJSON(w, http.StatusOK, map[string]any{"transfer": view})
}
