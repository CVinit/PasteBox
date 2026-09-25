package app

import (
	"context"
	"net/http"
	"pastebox/internal/plans"
)

type CatalogEntryWriter interface {
	SaveCatalogEntries(context.Context, plans.Catalog, AuditLog) error
}

// AdminUpdateCatalogEntries preserves unrelated catalog entries and drafts.
func (s *Service) AdminUpdateCatalogEntries(ctx context.Context, actorID string, update AdminPlanUpdate) (plans.Catalog, error) {
	s.configWriteMu.Lock()
	defer s.configWriteMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireAdminLocked(ctx, actorID); err != nil {
		return plans.Catalog{}, err
	}
	if len(update.Plans)+len(update.Prices) == 0 {
		return plans.Catalog{}, E(http.StatusBadRequest, "catalog_entries_required", "at least one catalog entry is required")
	}
	current := cloneCatalog(s.catalog)
	if s.catalogStore != nil {
		s.mu.Unlock()
		loaded, err := s.catalogStore.Catalog(ctx)
		s.mu.Lock()
		if err != nil {
			return plans.Catalog{}, err
		}
		current = loaded
	}
	selectedPlans := map[string]bool{}
	selectedPrices := map[string]bool{}
	for _, entry := range update.Plans {
		if selectedPlans[entry.ID] {
			return plans.Catalog{}, E(http.StatusBadRequest, "duplicate_plan", "plan IDs must be unique")
		}
		found := false
		for i := range current.Plans {
			if current.Plans[i].ID == entry.ID {
				current.Plans[i] = entry
				found = true
				break
			}
		}
		if !found {
			return plans.Catalog{}, E(http.StatusNotFound, "plan_not_found", "plan not found")
		}
		selectedPlans[entry.ID] = true
	}
	for _, entry := range update.Prices {
		if selectedPrices[entry.ID] {
			return plans.Catalog{}, E(http.StatusBadRequest, "duplicate_price", "price IDs must be unique")
		}
		found := false
		for i := range current.Prices {
			if current.Prices[i].ID == entry.ID {
				current.Prices[i] = entry
				found = true
				break
			}
		}
		if !found {
			return plans.Catalog{}, E(http.StatusNotFound, "price_not_found", "price not found")
		}
		selectedPrices[entry.ID] = true
	}
	validated, err := validateCatalogUpdate(AdminPlanUpdate{Plans: current.Plans, Prices: current.Prices})
	if err != nil {
		return plans.Catalog{}, err
	}
	entries := plans.Catalog{}
	for _, plan := range validated.Plans {
		if selectedPlans[plan.ID] {
			entries.Plans = append(entries.Plans, plan)
		}
	}
	for _, price := range validated.Prices {
		if selectedPrices[price.ID] {
			entries.Prices = append(entries.Prices, price)
		}
	}
	audit := AuditLog{ID: s.newID("aud"), ActorID: actorID, Action: "admin.catalog_entries_update", Target: "plans", Metadata: map[string]any{"plans": len(entries.Plans), "prices": len(entries.Prices)}, CreatedAt: s.now().UTC()}
	if writer, ok := s.catalogStore.(CatalogEntryWriter); ok {
		s.mu.Unlock()
		err = writer.SaveCatalogEntries(ctx, entries, audit)
		s.mu.Lock()
		if err != nil {
			return plans.Catalog{}, err
		}
		s.catalog = cloneCatalog(validated)
		s.cacheAuditLogLocked(audit)
		return cloneCatalog(s.catalog), nil
	}
	// Existing test/local stores retain their complete-catalog writer contract.
	if _, ok := s.catalogWriter(); ok {
		return plans.Catalog{}, E(http.StatusInternalServerError, "catalog_partial_update_unavailable", "catalog store does not support entry updates")
	}
	if err := s.auditLocked(ctx, actorID, audit.Action, audit.Target, audit.Metadata); err != nil {
		return plans.Catalog{}, err
	}
	s.catalog = cloneCatalog(validated)
	return cloneCatalog(s.catalog), nil
}
