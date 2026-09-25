package app

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"pastebox/internal/plans"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func normalizeProvider(provider string) string {
	return strings.ToLower(strings.TrimSpace(provider))
}

func isLocalHost(host string) bool {
	normalized := strings.ToLower(strings.Trim(host, "[]"))
	switch normalized {
	case "", "localhost", "host.docker.internal", "minio":
		return true
	}
	ip := net.ParseIP(normalized)
	return ip != nil && ip.IsLoopback()
}

func oauthIdentityKey(provider string, subject string) string {
	return normalizeProvider(provider) + "\x00" + strings.TrimSpace(subject)
}

func hasOAuthProvider(identities []OAuthIdentity, provider string) bool {
	provider = normalizeProvider(provider)
	for _, identity := range identities {
		if normalizeProvider(identity.Provider) == provider {
			return true
		}
	}
	return false
}

func oauthProviderNames(identities []OAuthIdentity) []string {
	providers := []string{}
	seen := map[string]struct{}{}
	for _, identity := range identities {
		provider := normalizeProvider(identity.Provider)
		if provider == "" {
			continue
		}
		if _, ok := seen[provider]; ok {
			continue
		}
		seen[provider] = struct{}{}
		providers = append(providers, provider)
	}
	sort.Strings(providers)
	return providers
}

func cloneMetadata(metadata map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range metadata {
		out[key] = value
	}
	return out
}

func cloneWebhookEvent(event WebhookEvent) WebhookEvent {
	event.Metadata = cloneMetadata(event.Metadata)
	return event
}

func cloneAuditLog(log AuditLog) AuditLog {
	log.Metadata = cloneMetadata(log.Metadata)
	return log
}

func stringFromMetadata(metadata map[string]any, key string) string {
	value, ok := metadata[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return fmt.Sprint(typed)
	}
}

func normalizeTags(tags []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, raw := range tags {
		for _, part := range strings.Split(raw, ",") {
			tag := strings.ToLower(strings.TrimSpace(part))
			if tag != "" && !seen[tag] {
				seen[tag] = true
				out = append(out, tag)
			}
		}
	}
	sort.Strings(out)
	return out
}

func resolveExpiresAt(now time.Time, expiresInSeconds int64, plan plans.Plan) time.Time {
	if expiresInSeconds <= 0 || expiresInSeconds > plan.MaxRetentionSeconds {
		expiresInSeconds = plan.MaxRetentionSeconds
	}
	return now.Add(time.Duration(expiresInSeconds) * time.Second)
}

func matchesPaste(paste PasteView, query string, filter string, tag string) bool {
	if tag != "" && !contains(paste.Tags, tag) {
		return false
	}
	if query != "" {
		haystack := strings.ToLower(paste.Title + "\n" + paste.Text + "\n" + strings.Join(paste.Tags, " "))
		for _, attachment := range paste.Attachments {
			haystack += "\n" + strings.ToLower(attachment.FileName)
		}
		if !strings.Contains(haystack, query) {
			return false
		}
	}
	switch filter {
	case "", "all":
		return true
	case "text":
		return strings.TrimSpace(paste.Text) != ""
	case "image":
		for _, att := range paste.Attachments {
			if strings.HasPrefix(att.ContentType, "image/") {
				return true
			}
		}
		return false
	case "file":
		return len(paste.Attachments) > 0
	case "expiring":
		return paste.SecondsToLive <= int64(24*time.Hour.Seconds())
	case "shared":
		return paste.ShareCount > 0
	case "favorite":
		return paste.Favorite
	case "pinned":
		return paste.Pinned
	default:
		return true
	}
}

func preview(text string) string {
	trimmed := strings.TrimSpace(text)
	if len([]rune(trimmed)) <= 160 {
		return trimmed
	}
	runes := []rune(trimmed)
	return string(runes[:160]) + "..."
}

func classifyAttachmentRisk(fileName string, contentType string) string {
	ext := strings.ToLower(filepath.Ext(fileName))
	if ext == ".exe" || ext == ".bat" || ext == ".cmd" || ext == ".scr" || ext == ".msi" {
		return "executable_file"
	}
	if strings.Contains(strings.ToLower(contentType), "html") || strings.Contains(strings.ToLower(contentType), "svg") {
		return "render_as_download_only"
	}
	return ""
}

func countReports(reports []*Report, status string) int {
	count := 0
	for _, report := range reports {
		if report.Status == status {
			count++
		}
	}
	return count
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func tagsEqual(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func removeString(values []string, target string) []string {
	out := values[:0]
	for _, value := range values {
		if value != target {
			out = append(out, value)
		}
	}
	return out
}

func NormalizeUserLanguage(language string) string {
	for _, candidate := range strings.Split(language, ",") {
		normalized := strings.TrimSpace(strings.Split(candidate, ";")[0])
		normalized = strings.ToLower(normalized)
		switch {
		case normalized == "zh-tw", normalized == "zh-hk", normalized == "zh-mo", strings.Contains(normalized, "hant"):
			return "zh-TW"
		case normalized == "zh-cn", normalized == "zh-sg", strings.HasPrefix(normalized, "zh"):
			return "zh-CN"
		case strings.HasPrefix(normalized, "es"):
			return "es"
		case strings.HasPrefix(normalized, "en"):
			return "en"
		}
	}
	return "en"
}

func defaultString(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func max64(a int64, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func max(a int, b int) int {
	if a > b {
		return a
	}
	return b
}

func ReadAllLimited(buf *bytes.Buffer, limit int64) ([]byte, error) {
	if int64(buf.Len()) > limit {
		return nil, E(http.StatusRequestEntityTooLarge, "file_too_large", "file exceeds request limit")
	}
	return buf.Bytes(), nil
}
