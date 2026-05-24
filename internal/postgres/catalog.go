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
