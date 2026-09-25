package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"pastebox/internal/app"
)

// OperationalMetrics aggregates durable state without filling the service cache.
func (s *OrderStore) OperationalMetrics(ctx context.Context) (app.OperationalMetrics, error) {
	var result app.OperationalMetrics
	var orders []byte
	err := s.pool.QueryRow(ctx, `
SELECT
 (SELECT count(*) FROM users),
 (SELECT count(*) FROM pastes WHERE status='active' AND expires_at>now()),
 (SELECT COALESCE(sum(octet_length(text_body)),0) FROM pastes WHERE status='active' AND expires_at>now()) +
 (SELECT COALESCE(sum(a.size_bytes),0) FROM attachments a JOIN pastes p ON p.id=a.paste_id WHERE a.status='active' AND p.status='active' AND p.expires_at>now()),
 (SELECT count(*) FROM reports WHERE status='open'),
 (SELECT count(*) FROM jobs WHERE kind='cleanup' AND status='pending'),
 (SELECT count(*) FROM jobs WHERE kind='cleanup_failed'),
 (SELECT count(*) FROM jobs WHERE kind='scan' AND status='pending'),
 (SELECT count(*) FROM jobs WHERE kind='scan_failed'),
 (SELECT count(*) FROM jobs WHERE status='failed'),
 (SELECT count(*) FROM mails WHERE status='queued'),
 (SELECT count(*) FROM mails WHERE status='failed'),
 (SELECT count(*) FROM webhook_events),
 (SELECT COALESCE(jsonb_object_agg(status,n),'{}'::jsonb) FROM (SELECT status,count(*) n FROM orders GROUP BY status) counts)
`).Scan(&result.UserCount, &result.ActivePastes, &result.ActiveStorageBytes, &result.ReportsOpen, &result.CleanupQueueDepth, &result.CleanupFailureDepth, &result.ScanQueueDepth, &result.ScanFailureDepth, &result.FailedJobDepth, &result.MailQueueDepth, &result.MailFailedDepth, &result.WebhookEvents, &orders)
	if err != nil {
		return app.OperationalMetrics{}, fmt.Errorf("read operational metrics: %w", err)
	}
	if err := json.Unmarshal(orders, &result.OrdersByStatus); err != nil {
		return app.OperationalMetrics{}, fmt.Errorf("decode order counts: %w", err)
	}
	return result, nil
}
