package app

import (
	"context"
	"time"
)

type OperationalStores struct {
	Orders        OrderStore
	WebhookEvents WebhookEventStore
	Reports       ReportStore
	Queues        QueueStore
	Mails         MailStore
}

type OrderStore interface {
	CreateOrder(ctx context.Context, order Order) error
	OrderByID(ctx context.Context, id string) (Order, error)
	ListOrders(ctx context.Context) ([]Order, error)
	ListOrdersByUser(ctx context.Context, userID string) ([]Order, error)
	UpdateOrder(ctx context.Context, order Order) error
}

type WebhookEventStore interface {
	CreateWebhookEvent(ctx context.Context, event WebhookEvent) error
	WebhookEventByID(ctx context.Context, id string) (WebhookEvent, error)
	WebhookEventByIdempotencyKey(ctx context.Context, idempotencyKey string) (WebhookEvent, error)
	ListWebhookEvents(ctx context.Context) ([]WebhookEvent, error)
	UpdateWebhookEventProcessed(ctx context.Context, id string, processed bool) error
}

type ReportStore interface {
	CreateReport(ctx context.Context, report Report) error
	ReportByID(ctx context.Context, id string) (Report, error)
	ListReports(ctx context.Context) ([]Report, error)
	UpdateReportStatus(ctx context.Context, id string, status string) error
}

type QueueStore interface {
	CreateQueueItem(ctx context.Context, item QueueItem) error
	ListQueueItemsByKind(ctx context.Context, kind string) ([]QueueItem, error)
	ListQueueItemsByStatus(ctx context.Context, status string, limit int) ([]QueueItem, error)
	DeleteQueueItemsByKindTarget(ctx context.Context, kind string, targetID string) error
}

type MailStore interface {
	QueueMail(ctx context.Context, mail Mail) error
	QueuedMails(ctx context.Context, limit int) ([]Mail, error)
	MailQueueItems(ctx context.Context, status string, limit int) ([]MailQueueItem, error)
}

type PagedOrderStore interface {
	ListOrdersPage(context.Context, int, int) ([]Order, error)
}
type PagedWebhookEventStore interface {
	ListWebhookEventsPage(context.Context, int, int) ([]WebhookEvent, error)
}
type PagedReportStore interface {
	ListReportsPage(context.Context, int, int) ([]Report, error)
}
type OperationalMetricsStore interface {
	OperationalMetrics(context.Context) (OperationalMetrics, error)
}

// Export queries are scoped to the owner and independent of bounded admin caches.
type UserReportStore interface {
	ListReportsByUser(context.Context, string) ([]Report, error)
}
type UserWebhookEventStore interface {
	ListWebhookEventsByUser(context.Context, string) ([]WebhookEvent, error)
}
type ExpiredOrderStore interface {
	ListExpiredPendingOrders(context.Context, time.Time, int) ([]Order, error)
}
