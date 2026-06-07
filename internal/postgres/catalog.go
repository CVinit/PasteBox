package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"pastebox/internal/plans"
)

type CatalogStore struct {
	pool *pgxpool.Pool
}

func NewCatalogStore(pool *pgxpool.Pool) *CatalogStore {
	return &CatalogStore{pool: pool}
}

func (s *CatalogStore) Catalog(ctx context.Context) (plans.Catalog, error) {
	catalog := plans.Catalog{
		Plans:  []plans.Plan{},
		Prices: []plans.Price{},
	}

	planRows, err := s.pool.Query(ctx, `
SELECT
	id,
	name,
	active_paste_limit,
	active_storage_bytes,
	single_text_bytes,
	single_file_bytes,
	single_paste_bytes,
	attachments_per_paste_limit,
	max_retention_seconds,
	daily_upload_bytes,
	daily_share_download_bytes
FROM plans
ORDER BY
	CASE id WHEN 'free' THEN 0 WHEN 'plus' THEN 1 WHEN 'pro' THEN 2 ELSE 100 END,
	id
`)
	if err != nil {
		return plans.Catalog{}, fmt.Errorf("query plans: %w", err)
	}
	defer planRows.Close()

	for planRows.Next() {
		var plan plans.Plan
		if err := planRows.Scan(
			&plan.ID,
			&plan.Name,
			&plan.ActivePasteLimit,
			&plan.ActiveStorageBytes,
			&plan.SingleTextBytes,
			&plan.SingleFileBytes,
			&plan.SinglePasteBytes,
			&plan.AttachmentsPerPasteLimit,
			&plan.MaxRetentionSeconds,
			&plan.DailyUploadBytes,
			&plan.DailyShareDownloadBytes,
		); err != nil {
			return plans.Catalog{}, fmt.Errorf("scan plan: %w", err)
		}
		catalog.Plans = append(catalog.Plans, plan)
	}
	if err := planRows.Err(); err != nil {
		return plans.Catalog{}, fmt.Errorf("read plans: %w", err)
	}

	priceRows, err := s.pool.Query(ctx, `
SELECT
	id,
	plan_id,
	period,
	amount_cents,
	currency,
	visible,
	purchase_enabled
FROM prices
ORDER BY
	CASE plan_id WHEN 'free' THEN 0 WHEN 'plus' THEN 1 WHEN 'pro' THEN 2 ELSE 100 END,
	CASE period WHEN 'monthly' THEN 0 WHEN 'yearly' THEN 1 ELSE 100 END,
	id
`)
	if err != nil {
		return plans.Catalog{}, fmt.Errorf("query prices: %w", err)
	}
	defer priceRows.Close()

	for priceRows.Next() {
		var price plans.Price
		if err := priceRows.Scan(
			&price.ID,
			&price.PlanID,
			&price.Period,
			&price.AmountCents,
			&price.Currency,
			&price.Visible,
			&price.PurchaseEnabled,
		); err != nil {
			return plans.Catalog{}, fmt.Errorf("scan price: %w", err)
		}
		catalog.Prices = append(catalog.Prices, price)
	}
	if err := priceRows.Err(); err != nil {
		return plans.Catalog{}, fmt.Errorf("read prices: %w", err)
	}

	return catalog, nil
}

func (s *CatalogStore) SaveCatalog(ctx context.Context, catalog plans.Catalog) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin save catalog: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()
	for _, plan := range catalog.Plans {
		if _, err := tx.Exec(ctx, `
INSERT INTO plans (
	id,
	name,
	active_paste_limit,
	active_storage_bytes,
	single_text_bytes,
	single_file_bytes,
	single_paste_bytes,
	attachments_per_paste_limit,
	max_retention_seconds,
	daily_upload_bytes,
	daily_share_download_bytes
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
ON CONFLICT (id) DO UPDATE SET
	name = EXCLUDED.name,
	active_paste_limit = EXCLUDED.active_paste_limit,
	active_storage_bytes = EXCLUDED.active_storage_bytes,
	single_text_bytes = EXCLUDED.single_text_bytes,
	single_file_bytes = EXCLUDED.single_file_bytes,
	single_paste_bytes = EXCLUDED.single_paste_bytes,
	attachments_per_paste_limit = EXCLUDED.attachments_per_paste_limit,
	max_retention_seconds = EXCLUDED.max_retention_seconds,
	daily_upload_bytes = EXCLUDED.daily_upload_bytes,
	daily_share_download_bytes = EXCLUDED.daily_share_download_bytes,
	updated_at = now()
`, plan.ID, plan.Name, plan.ActivePasteLimit, plan.ActiveStorageBytes, plan.SingleTextBytes, plan.SingleFileBytes, plan.SinglePasteBytes, plan.AttachmentsPerPasteLimit, plan.MaxRetentionSeconds, plan.DailyUploadBytes, plan.DailyShareDownloadBytes); err != nil {
			return fmt.Errorf("upsert plan %s: %w", plan.ID, err)
		}
	}
	for _, price := range catalog.Prices {
		if _, err := tx.Exec(ctx, `
INSERT INTO prices (
	id,
	plan_id,
	period,
	amount_cents,
	currency,
	visible,
	purchase_enabled
) VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (id) DO UPDATE SET
	plan_id = EXCLUDED.plan_id,
	period = EXCLUDED.period,
	amount_cents = EXCLUDED.amount_cents,
	currency = EXCLUDED.currency,
	visible = EXCLUDED.visible,
	purchase_enabled = EXCLUDED.purchase_enabled,
	updated_at = now()
`, price.ID, price.PlanID, price.Period, price.AmountCents, price.Currency, price.Visible, price.PurchaseEnabled); err != nil {
			return fmt.Errorf("upsert price %s: %w", price.ID, err)
		}
	}
	priceIDs := make([]string, 0, len(catalog.Prices))
	for _, price := range catalog.Prices {
		priceIDs = append(priceIDs, price.ID)
	}
	if len(priceIDs) == 0 {
		if _, err := tx.Exec(ctx, `DELETE FROM prices`); err != nil {
			return fmt.Errorf("delete removed prices: %w", err)
		}
	} else if _, err := tx.Exec(ctx, `DELETE FROM prices WHERE NOT (id = ANY($1))`, priceIDs); err != nil {
		return fmt.Errorf("delete removed prices: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit save catalog: %w", err)
	}
	return nil
}
