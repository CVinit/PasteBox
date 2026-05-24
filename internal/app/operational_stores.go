package app

import "context"

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
}
