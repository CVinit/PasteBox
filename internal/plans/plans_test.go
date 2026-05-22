package plans

import "testing"

func TestDefaultCatalogMatchesPRDPlanOrder(t *testing.T) {
	catalog := DefaultCatalog()

	if got := len(catalog.Plans); got != 3 {
		t.Fatalf("expected 3 plans, got %d", got)
	}

	wantOrder := []string{"free", "plus", "pro"}
	for idx, want := range wantOrder {
		if catalog.Plans[idx].ID != want {
			t.Fatalf("plan %d: expected %q, got %q", idx, want, catalog.Plans[idx].ID)
		}
	}
}

func TestFreePlanLimits(t *testing.T) {
	free := DefaultCatalog().Plans[0]

	if free.ActivePasteLimit != 20 {
		t.Fatalf("expected 20 active pastes, got %d", free.ActivePasteLimit)
	}
	if free.SingleTextBytes != 256*kib {
		t.Fatalf("expected 256 KiB text limit, got %d", free.SingleTextBytes)
	}
	if free.MaxRetentionSeconds != 24*60*60 {
		t.Fatalf("expected 24 hour retention, got %d", free.MaxRetentionSeconds)
	}
}
