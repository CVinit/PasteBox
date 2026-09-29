package httpserver

import (
	"net/http"
)

// resolvePickupCode turns a 6-character pickup code into the share it belongs
// to. It hands back the same share token a link would carry, so password,
// login, expiry and revocation keep being enforced by the one existing share
// access path, and the code itself never grants sender controls.
func (s *Server) resolvePickupCode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code string `json:"code"`
	}
	if !s.decodeOptionalLimited(w, r, &req, shareAccessBodyLimitBytes) {
		return
	}
	resolution, err := s.app.ResolvePickupCodeWithContext(r.Context(), req.Code, s.clientIP(r))
	if s.handleErr(w, err) {
		return
	}
	// The code is a credential: log which share it resolved, never the code.
	s.logger.Debug("pickup code resolved", "share_id", resolution.ShareID)
	// The resolution marshals to exactly the recipient credential: a token and
	// the URL it belongs to, never a share or transfer identifier.
	writeJSON(w, http.StatusOK, resolution)
}
