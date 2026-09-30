package httpserver

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// transferClaimCookieName holds the credential of one claim session. The cookie
// is scoped to the share it was claimed from, so a browser can hold live claims
// for several shares at once and a claim never authorizes another share.
const transferClaimCookieName = "pastebox_transfer_claim"

func transferClaimCookiePath(token string) string {
	return "/api/v1/shares/" + url.PathEscape(token)
}

// claimTransferShare spends one anonymous claim slot and hands the recipient the
// credential for the claimed session.
func (s *Server) claimTransferShare(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OperationID string `json:"operationId"`
		Password    string `json:"password"`
	}
	if !s.decodeOptionalLimited(w, r, &req, shareAccessBodyLimitBytes) {
		return
	}
	viewerID := s.optionalUserID(r)
	token := chi.URLParam(r, "token")
	result, err := s.app.ClaimTransferWithContext(r.Context(), token, req.Password, viewerID, req.OperationID)
	if s.handleErr(w, err) {
		return
	}
	s.setTransferClaimCookie(w, r, token, result.ClaimToken, result.Claim.ExpiresAt)
	// The claim credential is a bearer token: log which claim it is, never the
	// token itself.
	s.logger.Debug("transfer claimed", "claim_id", result.Claim.ID, "kind", result.Claim.Kind, "viewer_authenticated", viewerID != "")
	writeJSON(w, http.StatusOK, result)
}

// completeTransferClaim ends a file claim session on the recipient's request so
// the remaining files stop being downloadable before the session would expire.
// The credential is left in place on purpose: the server, not the browser, is
// what decides that the session ended, so the recipient gets a precise answer
// instead of appearing to have never claimed.
func (s *Server) completeTransferClaim(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	view, err := s.app.CompleteTransferClaimWithContext(r.Context(), token, chi.URLParam(r, "claimID"), s.transferClaimToken(r), s.optionalUserID(r))
	if s.handleErr(w, err) {
		return
	}
	s.logger.Debug("transfer claim completed", "claim_id", view.ID)
	writeJSON(w, http.StatusOK, map[string]any{"claim": view})
}

func (s *Server) setTransferClaimCookie(w http.ResponseWriter, r *http.Request, token string, claimToken string, expiresAt time.Time) {
	if claimToken == "" {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     transferClaimCookieName,
		Value:    claimToken,
		Path:     transferClaimCookiePath(token),
		Expires:  expiresAt,
		HttpOnly: true,
		Secure:   s.secureSessionCookie(r),
		SameSite: http.SameSiteLaxMode,
	})
}

// transferClaimToken reads the claim credential the recipient's browser stored
// when it claimed. The cookie is scoped to the share, so a request for one share
// never carries another share's claim.
func (s *Server) transferClaimToken(r *http.Request) string {
	cookie, err := r.Cookie(transferClaimCookieName)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(cookie.Value)
}
