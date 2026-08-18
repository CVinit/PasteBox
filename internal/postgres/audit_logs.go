package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"pastebox/internal/app"
)

type AuditLogStore struct {
	pool *pgxpool.Pool
}

func NewAuditLogStore(pool *pgxpool.Pool) *AuditLogStore {
	return &AuditLogStore{pool: pool}
}

func (s *AuditLogStore) RecordAuditLog(ctx context.Context, log app.AuditLog) error {
	return insertAuditLog(ctx, s.pool, log)
}

func insertAuditLog(ctx context.Context, executor execQuerier, log app.AuditLog) error {
	createdAt := log.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	metadata, err := json.Marshal(normalizeMetadata(log.Metadata))
	if err != nil {
		return fmt.Errorf("encode audit metadata: %w", err)
	}
	if _, err := executor.Exec(ctx, `
INSERT INTO audit_logs (id, actor_id, action, target, metadata, created_at)
VALUES ($1, $2, $3, $4, $5, $6)
`, log.ID, log.ActorID, log.Action, log.Target, string(metadata), createdAt); err != nil {
		return fmt.Errorf("record audit log: %w", err)
	}
	return nil
}

func (s *AuditLogStore) AuditLogs(ctx context.Context, limit int) ([]app.AuditLog, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, actor_id, action, target, metadata, created_at
FROM audit_logs
ORDER BY created_at DESC, id DESC
LIMIT $1
`, limit)
	if err != nil {
		return nil, fmt.Errorf("query audit logs: %w", err)
	}
	defer rows.Close()

	logs := []app.AuditLog{}
	for rows.Next() {
		var log app.AuditLog
		var metadataBytes []byte
		if err := rows.Scan(&log.ID, &log.ActorID, &log.Action, &log.Target, &metadataBytes, &log.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan audit log: %w", err)
		}
		metadata := map[string]any{}
		if len(metadataBytes) > 0 {
			if err := json.Unmarshal(metadataBytes, &metadata); err != nil {
				return nil, fmt.Errorf("decode audit metadata: %w", err)
			}
		}
		log.Metadata = metadata
		logs = append(logs, log)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read audit logs: %w", err)
	}
	return logs, nil
}

func (s *AuditLogStore) AuditLogsForActorOrTargets(ctx context.Context, actorID string, targets []string, limit int) ([]app.AuditLog, error) {
	if limit <= 0 {
		limit = 1000
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, actor_id, action, target, metadata, created_at
FROM audit_logs
WHERE actor_id = $1 OR target = ANY($2::text[])
ORDER BY created_at DESC, id DESC
LIMIT $3
`, actorID, targets, limit)
	if err != nil {
		return nil, fmt.Errorf("query scoped audit logs: %w", err)
	}
	defer rows.Close()

	logs := []app.AuditLog{}
	for rows.Next() {
		var log app.AuditLog
		var metadataBytes []byte
		if err := rows.Scan(&log.ID, &log.ActorID, &log.Action, &log.Target, &metadataBytes, &log.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan scoped audit log: %w", err)
		}
		metadata := map[string]any{}
		if len(metadataBytes) > 0 {
			if err := json.Unmarshal(metadataBytes, &metadata); err != nil {
				return nil, fmt.Errorf("decode scoped audit metadata: %w", err)
			}
		}
		log.Metadata = metadata
		logs = append(logs, log)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read scoped audit logs: %w", err)
	}
	return logs, nil
}

func normalizeMetadata(metadata map[string]any) map[string]any {
	if metadata == nil {
		return map[string]any{}
	}
	return metadata
}
