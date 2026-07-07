package plans

type Catalog struct {
	Plans  []Plan  `json:"plans"`
	Prices []Price `json:"prices"`
}

type Plan struct {
	ID                       string `json:"id"`
	Name                     string `json:"name"`
	ActivePasteLimit         int    `json:"activePasteLimit"`
	ActiveStorageBytes       int64  `json:"activeStorageBytes"`
	SingleTextBytes          int64  `json:"singleTextBytes"`
	SingleFileBytes          int64  `json:"singleFileBytes"`
	SinglePasteBytes         int64  `json:"singlePasteBytes"`
	AttachmentsPerPasteLimit int    `json:"attachmentsPerPasteLimit"`
	TagsPerPasteLimit        int    `json:"tagsPerPasteLimit"`
	MaxRetentionSeconds      int64  `json:"maxRetentionSeconds"`
	DailyUploadBytes         int64  `json:"dailyUploadBytes"`
	DailyShareDownloadBytes  int64  `json:"dailyShareDownloadBytes"`
}

type Price struct {
	ID              string `json:"id"`
	PlanID          string `json:"planId"`
	Period          string `json:"period"`
	AmountCents     int64  `json:"amountCents"`
	Currency        string `json:"currency"`
	Visible         bool   `json:"visible"`
	PurchaseEnabled bool   `json:"purchaseEnabled"`
}

const (
	kib = int64(1024)
	mib = 1024 * kib
	gib = 1024 * mib
	tib = 1024 * gib
)

func DefaultCatalog() Catalog {
	return Catalog{
		Plans: []Plan{
			{
				ID:                       "free",
				Name:                     "Free",
				ActivePasteLimit:         20,
				ActiveStorageBytes:       500 * mib,
				SingleTextBytes:          256 * kib,
				SingleFileBytes:          25 * mib,
				SinglePasteBytes:         50 * mib,
				AttachmentsPerPasteLimit: 5,
				TagsPerPasteLimit:        0,
				MaxRetentionSeconds:      24 * 60 * 60,
				DailyUploadBytes:         1 * gib,
				DailyShareDownloadBytes:  2 * gib,
			},
			{
				ID:                       "plus",
				Name:                     "Plus",
				ActivePasteLimit:         500,
				ActiveStorageBytes:       50 * gib,
				SingleTextBytes:          2 * mib,
				SingleFileBytes:          250 * mib,
				SinglePasteBytes:         1 * gib,
				AttachmentsPerPasteLimit: 20,
				TagsPerPasteLimit:        5,
				MaxRetentionSeconds:      30 * 24 * 60 * 60,
				DailyUploadBytes:         20 * gib,
				DailyShareDownloadBytes:  100 * gib,
			},
			{
				ID:                       "pro",
				Name:                     "Pro",
				ActivePasteLimit:         5000,
				ActiveStorageBytes:       500 * gib,
				SingleTextBytes:          10 * mib,
				SingleFileBytes:          2 * gib,
				SinglePasteBytes:         5 * gib,
				AttachmentsPerPasteLimit: 100,
				TagsPerPasteLimit:        20,
				MaxRetentionSeconds:      180 * 24 * 60 * 60,
				DailyUploadBytes:         200 * gib,
				DailyShareDownloadBytes:  1 * tib,
			},
		},
		Prices: []Price{
			{ID: "price_plus_monthly", PlanID: "plus", Period: "monthly", AmountCents: 900, Currency: "USD", Visible: true, PurchaseEnabled: true},
			{ID: "price_plus_yearly", PlanID: "plus", Period: "yearly", AmountCents: 9000, Currency: "USD", Visible: true, PurchaseEnabled: true},
			{ID: "price_pro_monthly", PlanID: "pro", Period: "monthly", AmountCents: 2900, Currency: "USD", Visible: true, PurchaseEnabled: true},
			{ID: "price_pro_yearly", PlanID: "pro", Period: "yearly", AmountCents: 29000, Currency: "USD", Visible: true, PurchaseEnabled: true},
		},
	}
}

func Find(catalog Catalog, id string) (Plan, bool) {
	for _, plan := range catalog.Plans {
		if plan.ID == id {
			return plan, true
		}
	}
	return Plan{}, false
}

func FindPrice(catalog Catalog, planID string, period string) (Price, bool) {
	for _, price := range catalog.Prices {
		if price.PlanID == planID && price.Period == period && price.Visible && price.PurchaseEnabled {
			return price, true
		}
	}
	return Price{}, false
}
