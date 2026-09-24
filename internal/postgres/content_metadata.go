package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"pastebox/internal/app"
)

var (
	ErrPasteNotFound      = errors.Join(errors.New("postgres paste not found"), app.ErrStoreNotFound)
	ErrAttachmentNotFound = errors.Join(errors.New("postgres attachment not found"), app.ErrStoreNotFound)
	ErrObjectRefNotFound  = errors.Join(errors.New("postgres object ref not found"), app.ErrStoreNotFound)
	ErrShareNotFound      = errors.Join(errors.New("postgres share not found"), app.ErrStoreNotFound)
	ErrShareTokenExists   = errors.Join(errors.New("postgres share token exists"), app.ErrStoreConflict)
)

type ObjectRef = app.ObjectRef

type PasteStore struct {
	pool *pgxpool.Pool
}

type objectRefQueryer interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type lockedObjectRefStore struct {
	store *AttachmentStore
	conn  *pgxpool.Conn
}

func (s *lockedObjectRefStore) IncrementObjectRef(ctx context.Context, ref app.ObjectRef) (app.ObjectRef, error) {
	return s.store.incrementObjectRefOnQueryer(ctx, s.conn, ref)
}

func (s *lockedObjectRefStore) DecrementObjectRef(ctx context.Context, objectKey string) (app.ObjectRef, bool, error) {
	return s.store.decrementObjectRefOnConn(ctx, s.conn, objectKey)
}

func NewPasteStore(pool *pgxpool.Pool) *PasteStore {
	return &PasteStore{pool: pool}
}

func (s *PasteStore) CreatePaste(ctx context.Context, paste app.Paste) error {
	return createPasteRecord(ctx, s.pool, paste)
}

func createPasteRecord(ctx context.Context, executor execQuerier, paste app.Paste) error {
	tags, err := json.Marshal(nonNilStrings(paste.Tags))
	if err != nil {
		return fmt.Errorf("encode paste tags: %w", err)
	}
	if _, err := executor.Exec(ctx, `
INSERT INTO pastes (
	id,
	user_id,
	title,
	text_body,
	tags,
	pinned,
	favorite,
	status,
	scan_status,
	expires_at,
	created_at,
	updated_at
) VALUES (
	$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12
)
`, paste.ID, paste.UserID, paste.Title, paste.Text, string(tags), paste.Pinned, paste.Favorite, paste.Status, paste.ScanStatus, paste.ExpiresAt, paste.CreatedAt, paste.UpdatedAt); err != nil {
		return fmt.Errorf("create paste: %w", err)
	}
	return nil
}

func (s *PasteStore) PasteByID(ctx context.Context, id string) (app.Paste, error) {
	return s.queryPaste(ctx, `
SELECT id, user_id, title, text_body, tags, pinned, favorite, status, scan_status, expires_at, created_at, updated_at
FROM pastes
WHERE id = $1
`, id)
}

func (s *PasteStore) ListPastesByUser(ctx context.Context, userID string) ([]app.Paste, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, user_id, title, text_body, tags, pinned, favorite, status, scan_status, expires_at, created_at, updated_at
FROM pastes
WHERE user_id = $1
ORDER BY pinned DESC, created_at DESC, id DESC
`, userID)
	if err != nil {
		return nil, fmt.Errorf("query pastes by user: %w", err)
	}
	defer rows.Close()
	return scanPastes(rows)
}

func (s *PasteStore) UserContentMetrics(ctx context.Context, userID string) (app.UserContentMetrics, error) {
	var metrics app.UserContentMetrics
	err := s.pool.QueryRow(ctx, `
SELECT
	COUNT(*)::int,
	COALESCE(SUM(length(p.text_body)), 0) + COALESCE((
		SELECT SUM(a.size_bytes)
		FROM attachments a
		JOIN pastes ap ON ap.id = a.paste_id
		WHERE a.user_id = $1
		  AND a.status = 'active'
		  AND ap.status = 'active'
		  AND ap.expires_at > CURRENT_TIMESTAMP
	), 0)
FROM pastes p
WHERE p.user_id = $1
	AND p.status = 'active'
	AND p.expires_at > CURRENT_TIMESTAMP
`, userID).Scan(&metrics.ActivePasteCount, &metrics.ActiveStorageBytes)
	if err != nil {
		return app.UserContentMetrics{}, fmt.Errorf("query user content metrics: %w", err)
	}
	return metrics, nil
}

func (s *PasteStore) ListPastesByUserPage(ctx context.Context, userID string, limit int, offset int) ([]app.Paste, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, user_id, title, text_body, tags, pinned, favorite, status, scan_status, expires_at, created_at, updated_at
FROM pastes
WHERE user_id = $1 AND status = 'active' AND expires_at > CURRENT_TIMESTAMP
ORDER BY pinned DESC, created_at DESC, id DESC
LIMIT $2 OFFSET $3
`, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("query paged pastes by user: %w", err)
	}
	defer rows.Close()
	return scanPastes(rows)
}

func (s *PasteStore) ListPastesPage(ctx context.Context, limit int, offset int) ([]app.Paste, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, user_id, title, text_body, tags, pinned, favorite, status, scan_status, expires_at, created_at, updated_at
FROM pastes
ORDER BY created_at DESC, id DESC
LIMIT $1 OFFSET $2
`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("query paged pastes: %w", err)
	}
	defer rows.Close()
	return scanPastes(rows)
}

func (s *PasteStore) ListPastesForCleanup(ctx context.Context, limit int) ([]app.Paste, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, user_id, title, text_body, tags, pinned, favorite, status, scan_status, expires_at, created_at, updated_at
FROM pastes
WHERE status = 'pending_delete'
   OR (status = 'active' AND expires_at <= CURRENT_TIMESTAMP)
ORDER BY updated_at ASC, created_at ASC, id ASC
LIMIT $1
`, limit)
	if err != nil {
		return nil, fmt.Errorf("query cleanup pastes: %w", err)
	}
	defer rows.Close()
	return scanPastes(rows)
}

func (s *PasteStore) ListPastesByUserPageWithOptions(ctx context.Context, userID string, opts app.ListOptions, limit int, offset int) ([]app.Paste, error) {
	args := []any{userID}
	conditions := []string{
		"p.user_id = $1",
		"p.status = 'active'",
		"p.expires_at > CURRENT_TIMESTAMP",
	}
	addArg := func(value any) string {
		args = append(args, value)
		return fmt.Sprintf("$%d", len(args))
	}

	query := strings.ToLower(strings.TrimSpace(opts.Query))
	if query != "" {
		placeholder := addArg(query)
		conditions = append(conditions, fmt.Sprintf(`(
			position(lower(%s) in lower(p.title)) > 0 OR
			position(lower(%s) in lower(p.text_body)) > 0 OR
			position(lower(%s) in lower(p.tags::text)) > 0 OR
			EXISTS (
				SELECT 1 FROM attachments a
				WHERE a.paste_id = p.id AND a.status <> 'deleted'
				  AND position(lower(%s) in lower(a.file_name)) > 0
			)
		)`, placeholder, placeholder, placeholder, placeholder))
	}
	if tag := strings.ToLower(strings.TrimSpace(opts.Tag)); tag != "" {
		tagJSON, err := json.Marshal([]string{tag})
		if err != nil {
			return nil, fmt.Errorf("encode paste tag filter: %w", err)
		}
		placeholder := addArg(string(tagJSON))
		conditions = append(conditions, fmt.Sprintf("p.tags @> %s::jsonb", placeholder))
	}

	switch strings.ToLower(strings.TrimSpace(opts.Filter)) {
	case "", "all":
	case "text":
		conditions = append(conditions, "btrim(p.text_body) <> ''")
	case "image":
		conditions = append(conditions, "EXISTS (SELECT 1 FROM attachments a WHERE a.paste_id = p.id AND a.status <> 'deleted' AND a.content_type LIKE 'image/%')")
	case "file":
		conditions = append(conditions, "EXISTS (SELECT 1 FROM attachments a WHERE a.paste_id = p.id AND a.status <> 'deleted')")
	case "expiring":
		conditions = append(conditions, "p.expires_at <= CURRENT_TIMESTAMP + INTERVAL '24 hours'")
	case "shared":
		conditions = append(conditions, "EXISTS (SELECT 1 FROM shares sh WHERE sh.paste_id = p.id AND sh.revoked_at IS NULL)")
	case "favorite":
		conditions = append(conditions, "p.favorite")
	case "pinned":
		conditions = append(conditions, "p.pinned")
	}
	limitPlaceholder := addArg(limit)
	offsetPlaceholder := addArg(offset)
	querySQL := fmt.Sprintf(`
SELECT p.id, p.user_id, p.title, p.text_body, p.tags, p.pinned, p.favorite, p.status, p.scan_status, p.expires_at, p.created_at, p.updated_at
FROM pastes p
WHERE %s
ORDER BY p.pinned DESC, p.created_at DESC, p.id DESC
LIMIT %s OFFSET %s
`, strings.Join(conditions, " AND "), limitPlaceholder, offsetPlaceholder)
	rows, err := s.pool.Query(ctx, querySQL, args...)
	if err != nil {
		return nil, fmt.Errorf("query filtered paged pastes by user: %w", err)
	}
	defer rows.Close()
	return scanPastes(rows)
}

func (s *PasteStore) ListPastes(ctx context.Context) ([]app.Paste, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, user_id, title, text_body, tags, pinned, favorite, status, scan_status, expires_at, created_at, updated_at
FROM pastes
ORDER BY created_at DESC, id DESC
`)
	if err != nil {
		return nil, fmt.Errorf("query pastes: %w", err)
	}
	defer rows.Close()
	return scanPastes(rows)
}

func (s *PasteStore) UpdatePaste(ctx context.Context, paste app.Paste) error {
	return updatePasteRecord(ctx, s.pool, paste)
}

func updatePasteRecord(ctx context.Context, executor execQuerier, paste app.Paste) error {
	tags, err := json.Marshal(nonNilStrings(paste.Tags))
	if err != nil {
		return fmt.Errorf("encode paste tags: %w", err)
	}
	tag, err := executor.Exec(ctx, `
UPDATE pastes
SET
	title = $2,
	text_body = $3,
	tags = $4,
	pinned = $5,
	favorite = $6,
	status = $7,
	scan_status = $8,
	expires_at = $9,
	updated_at = $10
WHERE id = $1
`, paste.ID, paste.Title, paste.Text, string(tags), paste.Pinned, paste.Favorite, paste.Status, paste.ScanStatus, paste.ExpiresAt, paste.UpdatedAt)
	if err != nil {
		return fmt.Errorf("update paste: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrPasteNotFound
	}
	return nil
}

func (s *PasteStore) queryPaste(ctx context.Context, sql string, args ...any) (app.Paste, error) {
	paste, err := scanPaste(s.pool.QueryRow(ctx, sql, args...))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return app.Paste{}, ErrPasteNotFound
		}
		return app.Paste{}, err
	}
	return paste, nil
}

type AttachmentStore struct {
	pool *pgxpool.Pool
}

func NewAttachmentStore(pool *pgxpool.Pool) *AttachmentStore {
	return &AttachmentStore{pool: pool}
}

func (s *AttachmentStore) CreateAttachment(ctx context.Context, attachment app.Attachment) error {
	if _, err := s.pool.Exec(ctx, `
INSERT INTO attachments (
	id,
	user_id,
	paste_id,
	file_name,
	content_type,
	size_bytes,
	sha256,
	object_key,
	status,
	scan_status,
	risk,
	image_width,
	image_height,
	download_count,
	created_at
) VALUES (
	$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15
)
`, attachment.ID, attachment.UserID, attachment.PasteID, attachment.FileName, attachment.ContentType, attachment.Size, attachment.SHA256, attachment.ObjectKey, attachment.Status, attachment.ScanStatus, attachment.Risk, attachment.ImageWidth, attachment.ImageHeight, attachment.DownloadN, attachment.CreatedAt); err != nil {
		return fmt.Errorf("create attachment: %w", err)
	}
	return nil
}

func (s *AttachmentStore) AttachmentByID(ctx context.Context, id string) (app.Attachment, error) {
	return s.queryAttachment(ctx, `
SELECT id, user_id, paste_id, file_name, content_type, size_bytes, sha256, object_key, status, scan_status, risk, image_width, image_height, download_count, created_at
FROM attachments
WHERE id = $1
`, id)
}

func (s *AttachmentStore) ListAttachmentsByPaste(ctx context.Context, pasteID string) ([]app.Attachment, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, user_id, paste_id, file_name, content_type, size_bytes, sha256, object_key, status, scan_status, risk, image_width, image_height, download_count, created_at
FROM attachments
WHERE paste_id = $1
ORDER BY created_at ASC, id ASC
`, pasteID)
	if err != nil {
		return nil, fmt.Errorf("query attachments by paste: %w", err)
	}
	defer rows.Close()
	attachments := []app.Attachment{}
	for rows.Next() {
		attachment, err := scanAttachment(rows)
		if err != nil {
			return nil, err
		}
		attachments = append(attachments, attachment)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read attachments: %w", err)
	}
	return attachments, nil
}

func (s *AttachmentStore) ListAttachments(ctx context.Context) ([]app.Attachment, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, user_id, paste_id, file_name, content_type, size_bytes, sha256, object_key, status, scan_status, risk, image_width, image_height, download_count, created_at
FROM attachments
ORDER BY created_at ASC, id ASC
`)
	if err != nil {
		return nil, fmt.Errorf("query attachments: %w", err)
	}
	defer rows.Close()
	attachments := []app.Attachment{}
	for rows.Next() {
		attachment, err := scanAttachment(rows)
		if err != nil {
			return nil, err
		}
		attachments = append(attachments, attachment)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read attachments: %w", err)
	}
	return attachments, nil
}

func (s *AttachmentStore) ListAttachmentsPage(ctx context.Context, query string, limit int, offset int) ([]app.Attachment, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	rows, err := s.pool.Query(ctx, `
SELECT id, user_id, paste_id, file_name, content_type, size_bytes, sha256, object_key, status, scan_status, risk, image_width, image_height, download_count, created_at
FROM attachments
WHERE $1 = '' OR position($1 in lower(concat_ws(E'\n', user_id, file_name, sha256, status, scan_status))) > 0
ORDER BY created_at DESC, id DESC
LIMIT $2 OFFSET $3
`, query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("query paged attachments: %w", err)
	}
	defer rows.Close()
	attachments := []app.Attachment{}
	for rows.Next() {
		attachment, err := scanAttachment(rows)
		if err != nil {
			return nil, err
		}
		attachments = append(attachments, attachment)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read paged attachments: %w", err)
	}
	return attachments, nil
}

func (s *AttachmentStore) UpdateAttachment(ctx context.Context, attachment app.Attachment) error {
	tag, err := s.pool.Exec(ctx, `
UPDATE attachments
SET
	file_name = $2,
	content_type = $3,
	size_bytes = $4,
	sha256 = $5,
	object_key = $6,
	status = $7,
	scan_status = $8,
	risk = $9,
	image_width = $10,
	image_height = $11,
	download_count = GREATEST(download_count, $12)
WHERE id = $1
`, attachment.ID, attachment.FileName, attachment.ContentType, attachment.Size, attachment.SHA256, attachment.ObjectKey, attachment.Status, attachment.ScanStatus, attachment.Risk, attachment.ImageWidth, attachment.ImageHeight, attachment.DownloadN)
	if err != nil {
		return fmt.Errorf("update attachment: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrAttachmentNotFound
	}
	return nil
}

func (s *AttachmentStore) IncrementAttachmentDownload(ctx context.Context, id string) (app.Attachment, error) {
	attachment, err := scanAttachment(s.pool.QueryRow(ctx, `
UPDATE attachments
SET download_count = download_count + 1
WHERE id = $1
RETURNING id, user_id, paste_id, file_name, content_type, size_bytes, sha256, object_key, status, scan_status, risk, image_width, image_height, download_count, created_at
`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return app.Attachment{}, ErrAttachmentNotFound
		}
		return app.Attachment{}, fmt.Errorf("increment attachment download: %w", err)
	}
	return attachment, nil
}

func (s *AttachmentStore) DeleteAttachment(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM attachments WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete attachment: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrAttachmentNotFound
	}
	return nil
}

func (s *AttachmentStore) UpsertObjectRef(ctx context.Context, ref app.ObjectRef) error {
	if ref.CreatedAt.IsZero() {
		ref.CreatedAt = time.Now().UTC()
	}
	if ref.UpdatedAt.IsZero() {
		ref.UpdatedAt = ref.CreatedAt
	}
	if _, err := s.pool.Exec(ctx, `
INSERT INTO object_refs (object_key, ref_count, size_bytes, sha256, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (object_key) DO UPDATE SET
	ref_count = EXCLUDED.ref_count,
	size_bytes = EXCLUDED.size_bytes,
	sha256 = EXCLUDED.sha256,
	updated_at = EXCLUDED.updated_at
`, ref.ObjectKey, ref.RefCount, ref.Size, ref.SHA256, ref.CreatedAt, ref.UpdatedAt); err != nil {
		return fmt.Errorf("upsert object ref: %w", err)
	}
	return nil
}

func (s *AttachmentStore) DeleteObjectRef(ctx context.Context, objectKey string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM object_refs WHERE object_key = $1`, objectKey)
	if err != nil {
		return fmt.Errorf("delete object ref: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrObjectRefNotFound
	}
	return nil
}

func (s *AttachmentStore) IncrementObjectRef(ctx context.Context, ref app.ObjectRef) (app.ObjectRef, error) {
	var loaded app.ObjectRef
	err := s.withObjectRefSessionLock(ctx, ref.ObjectKey, func(lockCtx context.Context, queryer objectRefQueryer) error {
		var err error
		loaded, err = s.incrementObjectRefOnQueryer(lockCtx, queryer, ref)
		return err
	})
	if err != nil {
		return app.ObjectRef{}, err
	}
	return loaded, nil
}

func (s *AttachmentStore) incrementObjectRefOnQueryer(ctx context.Context, queryer objectRefQueryer, ref app.ObjectRef) (app.ObjectRef, error) {
	if ref.CreatedAt.IsZero() {
		ref.CreatedAt = time.Now().UTC()
	}
	if ref.UpdatedAt.IsZero() {
		ref.UpdatedAt = ref.CreatedAt
	}
	var loaded app.ObjectRef
	err := queryer.QueryRow(ctx, `
INSERT INTO object_refs (object_key, ref_count, size_bytes, sha256, created_at, updated_at)
VALUES ($1, 1, $2, $3, $4, $5)
ON CONFLICT (object_key) DO UPDATE SET
	ref_count = object_refs.ref_count + 1,
	size_bytes = CASE WHEN object_refs.size_bytes = 0 THEN EXCLUDED.size_bytes ELSE object_refs.size_bytes END,
	sha256 = CASE WHEN object_refs.sha256 = '' THEN EXCLUDED.sha256 ELSE object_refs.sha256 END,
	updated_at = EXCLUDED.updated_at
RETURNING object_key, ref_count, size_bytes, sha256, created_at, updated_at
`, ref.ObjectKey, ref.Size, ref.SHA256, ref.CreatedAt, ref.UpdatedAt).Scan(
		&loaded.ObjectKey, &loaded.RefCount, &loaded.Size, &loaded.SHA256, &loaded.CreatedAt, &loaded.UpdatedAt,
	)
	if err != nil {
		return app.ObjectRef{}, fmt.Errorf("increment object ref: %w", err)
	}
	return loaded, nil
}

func (s *AttachmentStore) DecrementObjectRef(ctx context.Context, objectKey string) (app.ObjectRef, bool, error) {
	var remaining app.ObjectRef
	var removed bool
	err := s.withObjectRefSessionLock(ctx, objectKey, func(lockCtx context.Context, queryer objectRefQueryer) error {
		conn, ok := queryer.(*pgxpool.Conn)
		if !ok {
			return fmt.Errorf("object ref queryer is not a PostgreSQL connection")
		}
		var err error
		remaining, removed, err = s.decrementObjectRefOnConn(lockCtx, conn, objectKey)
		return err
	})
	if err != nil {
		return app.ObjectRef{}, false, err
	}
	return remaining, removed, nil
}

func (s *AttachmentStore) decrementObjectRefOnConn(ctx context.Context, conn *pgxpool.Conn, objectKey string) (app.ObjectRef, bool, error) {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return app.ObjectRef{}, false, fmt.Errorf("begin object ref decrement: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var remaining app.ObjectRef
	err = tx.QueryRow(ctx, `
UPDATE object_refs
SET ref_count = ref_count - 1, updated_at = now()
WHERE object_key = $1 AND ref_count > 1
RETURNING object_key, ref_count, size_bytes, sha256, created_at, updated_at
`, objectKey).Scan(&remaining.ObjectKey, &remaining.RefCount, &remaining.Size, &remaining.SHA256, &remaining.CreatedAt, &remaining.UpdatedAt)
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return app.ObjectRef{}, false, fmt.Errorf("commit object ref decrement: %w", err)
		}
		return remaining, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return app.ObjectRef{}, false, fmt.Errorf("decrement object ref: %w", err)
	}

	var removed app.ObjectRef
	err = tx.QueryRow(ctx, `
DELETE FROM object_refs
WHERE object_key = $1 AND ref_count = 1
RETURNING object_key, ref_count, size_bytes, sha256, created_at, updated_at
`, objectKey).Scan(&removed.ObjectKey, &removed.RefCount, &removed.Size, &removed.SHA256, &removed.CreatedAt, &removed.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return app.ObjectRef{}, false, ErrObjectRefNotFound
		}
		return app.ObjectRef{}, false, fmt.Errorf("remove object ref: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return app.ObjectRef{}, false, fmt.Errorf("commit object ref removal: %w", err)
	}
	return removed, true, nil
}

func (s *AttachmentStore) WithObjectRefLock(ctx context.Context, objectKey string, fn func(context.Context, app.AtomicObjectRefStore) error) error {
	conn, lockCtx, err := s.acquireObjectRefLock(ctx, objectKey)
	if err != nil {
		return err
	}
	callbackErr := fn(lockCtx, &lockedObjectRefStore{store: s, conn: conn})
	unlockErr := s.releaseObjectRefLock(conn, objectKey)
	conn.Release()
	return errors.Join(callbackErr, unlockErr)
}

func (s *AttachmentStore) withObjectRefSessionLock(ctx context.Context, objectKey string, fn func(context.Context, objectRefQueryer) error) error {
	conn, lockCtx, err := s.acquireObjectRefLock(ctx, objectKey)
	if err != nil {
		return err
	}
	callbackErr := fn(lockCtx, conn)
	unlockErr := s.releaseObjectRefLock(conn, objectKey)
	conn.Release()
	return errors.Join(callbackErr, unlockErr)
}

func (s *AttachmentStore) acquireObjectRefLock(ctx context.Context, objectKey string) (*pgxpool.Conn, context.Context, error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("acquire object ref lock connection: %w", err)
	}
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended($1, 0))`, objectKey); err != nil {
		conn.Release()
		return nil, nil, fmt.Errorf("acquire object ref lock: %w", err)
	}
	return conn, ctx, nil
}

func (s *AttachmentStore) releaseObjectRefLock(conn *pgxpool.Conn, objectKey string) error {
	unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := conn.Exec(unlockCtx, `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, objectKey); err != nil {
		return fmt.Errorf("release object ref lock: %w", err)
	}
	return nil
}

func (s *AttachmentStore) ObjectRef(ctx context.Context, objectKey string) (ObjectRef, error) {
	var ref ObjectRef
	err := s.pool.QueryRow(ctx, `
SELECT object_key, ref_count, size_bytes, sha256, created_at, updated_at
FROM object_refs
WHERE object_key = $1
`, objectKey).Scan(&ref.ObjectKey, &ref.RefCount, &ref.Size, &ref.SHA256, &ref.CreatedAt, &ref.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ObjectRef{}, ErrObjectRefNotFound
		}
		return ObjectRef{}, fmt.Errorf("read object ref: %w", err)
	}
	return ref, nil
}

func (s *AttachmentStore) queryAttachment(ctx context.Context, sql string, args ...any) (app.Attachment, error) {
	attachment, err := scanAttachment(s.pool.QueryRow(ctx, sql, args...))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return app.Attachment{}, ErrAttachmentNotFound
		}
		return app.Attachment{}, err
	}
	return attachment, nil
}

type ShareStore struct {
	pool *pgxpool.Pool
}

func NewShareStore(pool *pgxpool.Pool) *ShareStore {
	return &ShareStore{pool: pool}
}

func (s *ShareStore) CreateShare(ctx context.Context, share app.Share) error {
	if _, err := s.pool.Exec(ctx, `
INSERT INTO shares (
	id,
	paste_id,
	user_id,
	token_hash,
	token_ciphertext,
	password_hash,
	login_required,
	max_visits,
	max_downloads,
	visit_count,
	download_count,
	expires_at,
	revoked_at,
	created_at,
	last_visited_at,
	last_downloaded_at,
	last_access_failure
) VALUES (
	$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17
)
`, share.ID, share.PasteID, share.UserID, share.TokenHash, share.Token, share.PasswordHash, share.LoginRequired, share.MaxVisits, share.MaxDownloads, share.VisitCount, share.DownloadCount, share.ExpiresAt, share.RevokedAt, share.CreatedAt, share.LastVisitedAt, share.LastDownloadedAt, share.LastAccessFailure); err != nil {
		if isUniqueViolation(err, "shares_token_hash_key") {
			return ErrShareTokenExists
		}
		return fmt.Errorf("create share: %w", err)
	}
	return nil
}

func (s *ShareStore) ShareByID(ctx context.Context, id string) (app.Share, error) {
	return s.queryShare(ctx, `
SELECT id, paste_id, user_id, token_hash, token_ciphertext, password_hash, login_required, max_visits, max_downloads, visit_count, download_count, expires_at, revoked_at, created_at, last_visited_at, last_downloaded_at, last_access_failure
FROM shares
WHERE id = $1
`, id)
}

func (s *ShareStore) ShareByTokenHash(ctx context.Context, tokenHash string) (app.Share, error) {
	return s.queryShare(ctx, `
SELECT id, paste_id, user_id, token_hash, token_ciphertext, password_hash, login_required, max_visits, max_downloads, visit_count, download_count, expires_at, revoked_at, created_at, last_visited_at, last_downloaded_at, last_access_failure
FROM shares
WHERE token_hash = $1
`, tokenHash)
}

func (s *ShareStore) ListSharesByUser(ctx context.Context, userID string) ([]app.Share, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, paste_id, user_id, token_hash, token_ciphertext, password_hash, login_required, max_visits, max_downloads, visit_count, download_count, expires_at, revoked_at, created_at, last_visited_at, last_downloaded_at, last_access_failure
FROM shares
WHERE user_id = $1
ORDER BY created_at DESC, id DESC
`, userID)
	if err != nil {
		return nil, fmt.Errorf("query shares by user: %w", err)
	}
	defer rows.Close()
	shares := []app.Share{}
	for rows.Next() {
		share, err := scanShare(rows)
		if err != nil {
			return nil, err
		}
		shares = append(shares, share)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read shares: %w", err)
	}
	return shares, nil
}

func (s *ShareStore) ListSharesByPaste(ctx context.Context, pasteID string) ([]app.Share, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, paste_id, user_id, token_hash, token_ciphertext, password_hash, login_required, max_visits, max_downloads, visit_count, download_count, expires_at, revoked_at, created_at, last_visited_at, last_downloaded_at, last_access_failure
FROM shares
WHERE paste_id = $1
ORDER BY created_at DESC, id DESC
`, pasteID)
	if err != nil {
		return nil, fmt.Errorf("query shares by paste: %w", err)
	}
	defer rows.Close()
	shares := []app.Share{}
	for rows.Next() {
		share, err := scanShare(rows)
		if err != nil {
			return nil, err
		}
		shares = append(shares, share)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read shares by paste: %w", err)
	}
	return shares, nil
}

func (s *ShareStore) ListShares(ctx context.Context) ([]app.Share, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, paste_id, user_id, token_hash, token_ciphertext, password_hash, login_required, max_visits, max_downloads, visit_count, download_count, expires_at, revoked_at, created_at, last_visited_at, last_downloaded_at, last_access_failure
FROM shares
ORDER BY created_at DESC, id DESC
`)
	if err != nil {
		return nil, fmt.Errorf("query shares: %w", err)
	}
	defer rows.Close()
	shares := []app.Share{}
	for rows.Next() {
		share, err := scanShare(rows)
		if err != nil {
			return nil, err
		}
		shares = append(shares, share)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read shares: %w", err)
	}
	return shares, nil
}

func (s *ShareStore) ListSharesPage(ctx context.Context, limit int, offset int) ([]app.Share, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, paste_id, user_id, token_hash, token_ciphertext, password_hash, login_required, max_visits, max_downloads, visit_count, download_count, expires_at, revoked_at, created_at, last_visited_at, last_downloaded_at, last_access_failure
FROM shares
ORDER BY created_at DESC, id DESC
LIMIT $1 OFFSET $2
`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("query paged shares: %w", err)
	}
	defer rows.Close()
	shares := []app.Share{}
	for rows.Next() {
		share, err := scanShare(rows)
		if err != nil {
			return nil, err
		}
		shares = append(shares, share)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read paged shares: %w", err)
	}
	return shares, nil
}

func (s *ShareStore) UpdateShare(ctx context.Context, share app.Share) error {
	tag, err := s.pool.Exec(ctx, `
UPDATE shares
SET
	token_ciphertext = $2,
	password_hash = $3,
	login_required = $4,
	max_visits = $5,
	max_downloads = $6,
	visit_count = GREATEST(visit_count, $7),
	download_count = GREATEST(download_count, $8),
	expires_at = $9,
	revoked_at = COALESCE(revoked_at, $10),
	last_visited_at = GREATEST(last_visited_at, $11),
	last_downloaded_at = GREATEST(last_downloaded_at, $12),
	last_access_failure = $13
WHERE id = $1
`, share.ID, share.Token, share.PasswordHash, share.LoginRequired, share.MaxVisits, share.MaxDownloads, share.VisitCount, share.DownloadCount, share.ExpiresAt, share.RevokedAt, share.LastVisitedAt, share.LastDownloadedAt, share.LastAccessFailure)
	if err != nil {
		return fmt.Errorf("update share: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrShareNotFound
	}
	return nil
}

func (s *ShareStore) ConsumeShareVisit(ctx context.Context, shareID string, now time.Time) (app.Share, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return app.Share{}, fmt.Errorf("begin share visit: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	share, err := scanShare(tx.QueryRow(ctx, `
SELECT id, paste_id, user_id, token_hash, token_ciphertext, password_hash, login_required, max_visits, max_downloads, visit_count, download_count, expires_at, revoked_at, created_at, last_visited_at, last_downloaded_at, last_access_failure
FROM shares
WHERE id = $1
FOR UPDATE
`, shareID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return app.Share{}, ErrShareNotFound
		}
		return app.Share{}, fmt.Errorf("load share for visit: %w", err)
	}
	if share.RevokedAt != nil || !share.ExpiresAt.After(now) {
		return app.Share{}, app.E(http.StatusGone, "share_expired", "share is expired or revoked")
	}
	if share.MaxVisits > 0 && share.VisitCount >= share.MaxVisits {
		return app.Share{}, app.E(http.StatusGone, "visit_limit_reached", "share visit limit reached")
	}
	share.VisitCount++
	share.LastVisitedAt = &now
	if _, err := tx.Exec(ctx, `
UPDATE shares SET visit_count = $2, last_visited_at = $3 WHERE id = $1
`, share.ID, share.VisitCount, now); err != nil {
		return app.Share{}, fmt.Errorf("update share visit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return app.Share{}, fmt.Errorf("commit share visit: %w", err)
	}
	return share, nil
}

func (s *ShareStore) ConsumeShareDownload(ctx context.Context, shareID string, attachmentID string, userID string, dailyLimit int64, now time.Time) (app.Share, app.Attachment, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return app.Share{}, app.Attachment{}, fmt.Errorf("begin share download: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	share, err := scanShare(tx.QueryRow(ctx, `
SELECT id, paste_id, user_id, token_hash, token_ciphertext, password_hash, login_required, max_visits, max_downloads, visit_count, download_count, expires_at, revoked_at, created_at, last_visited_at, last_downloaded_at, last_access_failure
FROM shares
WHERE id = $1
FOR UPDATE
`, shareID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return app.Share{}, app.Attachment{}, ErrShareNotFound
		}
		return app.Share{}, app.Attachment{}, fmt.Errorf("load share for download: %w", err)
	}
	if share.UserID != userID {
		return app.Share{}, app.Attachment{}, app.E(http.StatusForbidden, "share_owner_mismatch", "share owner does not match")
	}
	if share.RevokedAt != nil || !share.ExpiresAt.After(now) {
		return app.Share{}, app.Attachment{}, app.E(http.StatusGone, "share_expired", "share is expired or revoked")
	}
	if share.MaxDownloads > 0 && share.DownloadCount >= share.MaxDownloads {
		return app.Share{}, app.Attachment{}, app.E(http.StatusGone, "download_limit_reached", "share download limit reached")
	}

	attachment, err := scanAttachment(tx.QueryRow(ctx, `
SELECT id, user_id, paste_id, file_name, content_type, size_bytes, sha256, object_key, status, scan_status, risk, image_width, image_height, download_count, created_at
FROM attachments
WHERE id = $1 AND paste_id = $2 AND status = 'active' AND scan_status = 'clean'
`, attachmentID, share.PasteID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return app.Share{}, app.Attachment{}, ErrAttachmentNotFound
		}
		return app.Share{}, app.Attachment{}, fmt.Errorf("load shared attachment: %w", err)
	}
	if dailyLimit < 0 {
		dailyLimit = 0
	}
	var dailyBytes int64
	err = tx.QueryRow(ctx, `
INSERT INTO daily_metrics (user_id, metric_kind, metric_day, bytes)
SELECT $1, $2, $3, $4::bigint
WHERE $4::bigint <= $5::bigint
ON CONFLICT (user_id, metric_kind, metric_day)
DO UPDATE SET bytes = daily_metrics.bytes + EXCLUDED.bytes
WHERE daily_metrics.bytes + EXCLUDED.bytes <= $5::bigint
RETURNING bytes
`, share.UserID, "share_download", metricDay(now), attachment.Size, dailyLimit).Scan(&dailyBytes)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return app.Share{}, app.Attachment{}, app.E(http.StatusForbidden, "daily_download_limit", "daily share download traffic exceeds plan limit")
		}
		return app.Share{}, app.Attachment{}, fmt.Errorf("record shared download metric: %w", err)
	}
	_ = dailyBytes

	attachment, err = scanAttachment(tx.QueryRow(ctx, `
UPDATE attachments
SET download_count = download_count + 1
WHERE id = $1 AND paste_id = $2 AND status = 'active' AND scan_status = 'clean'
RETURNING id, user_id, paste_id, file_name, content_type, size_bytes, sha256, object_key, status, scan_status, risk, image_width, image_height, download_count, created_at
`, attachmentID, share.PasteID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return app.Share{}, app.Attachment{}, ErrAttachmentNotFound
		}
		return app.Share{}, app.Attachment{}, fmt.Errorf("increment shared attachment download: %w", err)
	}
	share.DownloadCount++
	share.LastDownloadedAt = &now
	if _, err := tx.Exec(ctx, `
UPDATE shares SET download_count = $2, last_downloaded_at = $3 WHERE id = $1
`, share.ID, share.DownloadCount, now); err != nil {
		return app.Share{}, app.Attachment{}, fmt.Errorf("update share download: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return app.Share{}, app.Attachment{}, fmt.Errorf("commit share download: %w", err)
	}
	return share, attachment, nil
}

func (s *ShareStore) queryShare(ctx context.Context, sql string, args ...any) (app.Share, error) {
	share, err := scanShare(s.pool.QueryRow(ctx, sql, args...))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return app.Share{}, ErrShareNotFound
		}
		return app.Share{}, err
	}
	return share, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

type rowsScanner interface {
	rowScanner
	Next() bool
	Err() error
}

func scanPastes(rows rowsScanner) ([]app.Paste, error) {
	pastes := []app.Paste{}
	for rows.Next() {
		paste, err := scanPaste(rows)
		if err != nil {
			return nil, err
		}
		pastes = append(pastes, paste)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read pastes: %w", err)
	}
	return pastes, nil
}

func scanPaste(row rowScanner) (app.Paste, error) {
	var paste app.Paste
	var tagsBytes []byte
	if err := row.Scan(&paste.ID, &paste.UserID, &paste.Title, &paste.Text, &tagsBytes, &paste.Pinned, &paste.Favorite, &paste.Status, &paste.ScanStatus, &paste.ExpiresAt, &paste.CreatedAt, &paste.UpdatedAt); err != nil {
		return app.Paste{}, fmt.Errorf("scan paste: %w", err)
	}
	if len(tagsBytes) > 0 {
		if err := json.Unmarshal(tagsBytes, &paste.Tags); err != nil {
			return app.Paste{}, fmt.Errorf("decode paste tags: %w", err)
		}
	}
	paste.Tags = nonNilStrings(paste.Tags)
	return paste, nil
}

func scanAttachment(row rowScanner) (app.Attachment, error) {
	var attachment app.Attachment
	if err := row.Scan(
		&attachment.ID,
		&attachment.UserID,
		&attachment.PasteID,
		&attachment.FileName,
		&attachment.ContentType,
		&attachment.Size,
		&attachment.SHA256,
		&attachment.ObjectKey,
		&attachment.Status,
		&attachment.ScanStatus,
		&attachment.Risk,
		&attachment.ImageWidth,
		&attachment.ImageHeight,
		&attachment.DownloadN,
		&attachment.CreatedAt,
	); err != nil {
		return app.Attachment{}, fmt.Errorf("scan attachment: %w", err)
	}
	return attachment, nil
}

func scanShare(row rowScanner) (app.Share, error) {
	var share app.Share
	var revokedAt pgtype.Timestamptz
	var lastVisitedAt pgtype.Timestamptz
	var lastDownloadedAt pgtype.Timestamptz
	var lastAccessFailure pgtype.Timestamptz
	if err := row.Scan(
		&share.ID,
		&share.PasteID,
		&share.UserID,
		&share.TokenHash,
		&share.Token,
		&share.PasswordHash,
		&share.LoginRequired,
		&share.MaxVisits,
		&share.MaxDownloads,
		&share.VisitCount,
		&share.DownloadCount,
		&share.ExpiresAt,
		&revokedAt,
		&share.CreatedAt,
		&lastVisitedAt,
		&lastDownloadedAt,
		&lastAccessFailure,
	); err != nil {
		return app.Share{}, fmt.Errorf("scan share: %w", err)
	}
	share.RevokedAt = optionalTime(revokedAt)
	share.LastVisitedAt = optionalTime(lastVisitedAt)
	share.LastDownloadedAt = optionalTime(lastDownloadedAt)
	share.LastAccessFailure = optionalTime(lastAccessFailure)
	return share, nil
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// Multiple cleanup jobs can scan the same expired paste. Serialize sweepers so
// they cannot decrement one attachment's object reference twice.
func (s *PasteStore) WithCleanupLock(ctx context.Context, fn func(context.Context) error) error {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire cleanup connection: %w", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(773012, 1)`); err != nil {
		return fmt.Errorf("acquire cleanup lock: %w", err)
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if _, err := conn.Exec(cleanupCtx, `SELECT pg_advisory_unlock(773012, 1)`); err != nil {
			_ = conn.Conn().Close(cleanupCtx)
		}
	}()
	return fn(ctx)
}
