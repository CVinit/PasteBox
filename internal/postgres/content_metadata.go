package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
ORDER BY created_at DESC, id DESC
`, userID)
	if err != nil {
		return nil, fmt.Errorf("query pastes by user: %w", err)
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
	download_count = $12
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

func (s *ShareStore) UpdateShare(ctx context.Context, share app.Share) error {
	tag, err := s.pool.Exec(ctx, `
UPDATE shares
SET
	token_ciphertext = $2,
	password_hash = $3,
	login_required = $4,
	max_visits = $5,
	max_downloads = $6,
	visit_count = $7,
	download_count = $8,
	expires_at = $9,
	revoked_at = $10,
	last_visited_at = $11,
	last_downloaded_at = $12,
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
