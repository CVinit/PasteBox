package app

import (
	"context"
	"fmt"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"pastebox/internal/config"
	"pastebox/internal/plans"
	"sync"
	"time"
)

type objectKeyLock struct {
	mu   sync.Mutex
	refs int
}

type Service struct {
	mu            sync.Mutex
	configWriteMu sync.Mutex
	objectLocksMu sync.Mutex
	objectLocks   map[string]*objectKeyLock
	rootConfig    config.Config
	cfg           config.Config
	now           func() time.Time
	catalog       plans.Catalog
	catalogStore  CatalogStore
	auth          AuthStores
	content       ContentStores
	objectStore   ObjectStore
	ops           OperationalStores
	audit         AuditLogStore
	runtime       RuntimeConfigStore
	redemptions   RedemptionStore
	transactions  BusinessTransactionStore
	alerts        AlertEventStore

	usersByID               map[string]*User
	userIDByEmail           map[string]string
	sessionsByID            map[string]*Session
	emailVerifies           map[string]*AuthToken
	passwordResets          map[string]*AuthToken
	loginFailures           map[string]*LoginFailure
	oauthIdentities         map[string]*OAuthIdentity
	pastesByID              map[string]*Paste
	attachmentsByID         map[string]*Attachment
	objects                 map[string][]byte
	objectRefs              map[string]int
	dailyMetrics            DailyMetricStore
	sharesByID              map[string]*Share
	shareIDByToken          map[string]string
	ordersByID              map[string]*Order
	webhookEventKeys        map[string]string
	webhookEvents           []*WebhookEvent
	auditLogs               []*AuditLog
	reports                 []*Report
	cleanupJobs             []*QueueItem
	cleanupFailures         []*QueueItem
	scanJobs                []*QueueItem
	scanFailures            []*QueueItem
	failedJobs              []*QueueItem
	mails                   []*Mail
	runtimeConfig           RuntimeConfig
	managedSecrets          config.ManagedSecrets
	runtimeConfigChange     RuntimeConfigChangeHook
	redemptionBatches       map[string]*RedemptionBatch
	redemptionCodesByHash   map[string]*RedemptionCode
	redemptionRecords       []*RedemptionRecord
	alertEvents             []*AlertEvent
	alertSender             AlertSender
	turnstileVerifier       TurnstileVerifier
	turnstileVerifierCustom bool
	turnstileTokenHashes    map[string]time.Time
	resourceSnapshot        func() RuntimeResourceSnapshot
	nextID                  int64
}

func New(cfg config.Config) *Service {
	return NewWithStores(cfg, AuthStores{}, nil)
}

func NewWithStores(cfg config.Config, authStores AuthStores, dailyMetrics DailyMetricStore) *Service {
	svc, err := NewWithStorage(context.Background(), cfg, Stores{
		Auth:         authStores,
		DailyMetrics: dailyMetrics,
	})
	if err != nil {
		panic(err)
	}
	return svc
}

func NewWithStorage(ctx context.Context, cfg config.Config, stores Stores) (*Service, error) {
	catalog := plans.DefaultCatalog()
	if stores.Catalog != nil {
		loaded, err := stores.Catalog.Catalog(ctx)
		if err != nil {
			return nil, fmt.Errorf("load plan catalog: %w", err)
		}
		catalog = cloneCatalog(loaded)
	}

	managed, managedSecrets := config.ManagedFromConfig(cfg)
	initialRuntimeConfig := defaultRuntimeConfig(cfg)
	initialRuntimeConfig.Managed = managed
	svc := &Service{
		rootConfig:            cfg,
		cfg:                   cfg,
		now:                   time.Now,
		catalog:               catalog,
		catalogStore:          stores.Catalog,
		auth:                  stores.Auth,
		content:               stores.Content,
		objectStore:           stores.Objects,
		ops:                   stores.Operational,
		audit:                 stores.AuditLogs,
		runtime:               stores.RuntimeConfigs,
		redemptions:           stores.Redemptions,
		transactions:          stores.BusinessTransactions,
		alerts:                stores.AlertEvents,
		usersByID:             map[string]*User{},
		userIDByEmail:         map[string]string{},
		sessionsByID:          map[string]*Session{},
		emailVerifies:         map[string]*AuthToken{},
		passwordResets:        map[string]*AuthToken{},
		loginFailures:         map[string]*LoginFailure{},
		oauthIdentities:       map[string]*OAuthIdentity{},
		pastesByID:            map[string]*Paste{},
		attachmentsByID:       map[string]*Attachment{},
		objects:               map[string][]byte{},
		objectRefs:            map[string]int{},
		dailyMetrics:          newMemoryDailyMetricStore(),
		sharesByID:            map[string]*Share{},
		shareIDByToken:        map[string]string{},
		ordersByID:            map[string]*Order{},
		webhookEventKeys:      map[string]string{},
		runtimeConfig:         initialRuntimeConfig,
		managedSecrets:        managedSecrets,
		redemptionBatches:     map[string]*RedemptionBatch{},
		redemptionCodesByHash: map[string]*RedemptionCode{},
		redemptionRecords:     []*RedemptionRecord{},
		alertEvents:           []*AlertEvent{},
		turnstileVerifier:     NewTurnstileVerifier(cfg),
		turnstileTokenHashes:  map[string]time.Time{},
		objectLocks:           map[string]*objectKeyLock{},
		resourceSnapshot:      defaultRuntimeResourceSnapshot,
	}
	if stores.DailyMetrics != nil {
		svc.dailyMetrics = stores.DailyMetrics
	}
	if err := svc.loadRuntimeConfig(ctx); err != nil {
		return nil, err
	}
	if err := svc.loadRedemptionCaches(ctx); err != nil {
		return nil, err
	}
	if err := svc.loadAlertEvents(ctx); err != nil {
		return nil, err
	}
	if err := svc.loadContentCaches(ctx); err != nil {
		return nil, err
	}
	if err := svc.loadOperationalCaches(ctx); err != nil {
		return nil, err
	}
	if cfg.BootstrapAdminEmail != "" && cfg.BootstrapAdminPassword != "" {
		if _, err := svc.SeedAdminWithContext(ctx, cfg.BootstrapAdminEmail, cfg.BootstrapAdminPassword); err != nil {
			return nil, fmt.Errorf("bootstrap admin: %w", err)
		}
	}
	return svc, nil
}

func NewForTest(now func() time.Time) *Service {
	svc := New(config.FromEnv())
	svc.now = now
	return svc
}

func NewWithDailyMetricStore(cfg config.Config, dailyMetrics DailyMetricStore) *Service {
	return NewWithStores(cfg, AuthStores{}, dailyMetrics)
}

type Stores struct {
	Auth                 AuthStores
	Content              ContentStores
	Objects              ObjectStore
	Operational          OperationalStores
	DailyMetrics         DailyMetricStore
	Catalog              CatalogStore
	AuditLogs            AuditLogStore
	RuntimeConfigs       RuntimeConfigStore
	Redemptions          RedemptionStore
	BusinessTransactions BusinessTransactionStore
	AlertEvents          AlertEventStore
}

type CatalogStore interface {
	Catalog(ctx context.Context) (plans.Catalog, error)
}

type AuditLogStore interface {
	RecordAuditLog(ctx context.Context, log AuditLog) error
	AuditLogs(ctx context.Context, limit int) ([]AuditLog, error)
	AuditLogsForActorOrTargets(ctx context.Context, actorID string, targets []string, limit int) ([]AuditLog, error)
}
