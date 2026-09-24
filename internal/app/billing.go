package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"pastebox/internal/plans"
	"sort"
	"strings"
	"time"
)

func (s *Service) QuotaWithContext(ctx context.Context, userID string) (QuotaView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, err := s.activeUserLocked(ctx, userID)
	if err != nil {
		return QuotaView{}, err
	}
	plan, _ := s.planForUserLocked(user)
	return s.quotaLocked(ctx, user.ID, plan)
}

func (s *Service) PlanCatalog() plans.Catalog {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneCatalog(s.catalog)
}

func (s *Service) Prices() struct {
	Plans  []plans.Plan   `json:"plans"`
	Prices []BillingPrice `json:"prices"`
} {
	s.mu.Lock()
	defer s.mu.Unlock()

	prices := make([]BillingPrice, 0, len(s.catalog.Prices))
	for _, price := range s.catalog.Prices {
		prices = append(prices, BillingPrice{
			ID:              price.ID,
			PlanID:          price.PlanID,
			Period:          price.Period,
			AmountCents:     price.AmountCents,
			Currency:        price.Currency,
			Visible:         price.Visible,
			PurchaseEnabled: price.PurchaseEnabled,
			StripeEnabled:   s.cfg.StripeEnabled,
			EpusdtEnabled:   s.cfg.EpusdtEnabled,
		})
	}
	return struct {
		Plans  []plans.Plan   `json:"plans"`
		Prices []BillingPrice `json:"prices"`
	}{
		Plans:  cloneCatalog(s.catalog).Plans,
		Prices: prices,
	}
}

func (s *Service) CreateOrderWithContext(ctx context.Context, userID string, provider string, planID string, period string) (Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.activeUserLocked(ctx, userID); err != nil {
		return Order{}, err
	}
	provider = normalizeProvider(provider)
	if provider != "stripe" && provider != "epusdt" {
		return Order{}, E(http.StatusBadRequest, "invalid_provider", "provider must be stripe or epusdt")
	}
	if planID != "plus" && planID != "pro" {
		return Order{}, E(http.StatusBadRequest, "invalid_plan", "paid order requires plus or pro")
	}
	if period == "" {
		period = "monthly"
	}
	price, ok := plans.FindPrice(s.catalog, planID, period)
	if !ok {
		return Order{}, E(http.StatusBadRequest, "invalid_price", "price is not available")
	}
	now := s.now().UTC()
	expiresAt := now.Add(30 * time.Minute)
	orderID := s.newID("ord")
	checkoutURL, address, chain, err := s.paymentDetailsForOrderLocked(provider, orderID, planID, period, price)
	if err != nil {
		return Order{}, err
	}
	order := &Order{
		ID:          orderID,
		UserID:      userID,
		Provider:    provider,
		PlanID:      planID,
		Period:      period,
		AmountCents: price.AmountCents,
		Currency:    price.Currency,
		Status:      "pending",
		CheckoutURL: checkoutURL,
		Address:     address,
		Chain:       chain,
		CreatedAt:   now,
		ExpiresAt:   &expiresAt,
	}
	if err := s.createOrderLocked(ctx, order); err != nil {
		return Order{}, err
	}
	if _, err := s.recordWebhookEventLocked(ctx, provider, "checkout.created", order.ID, "checkout.created:"+order.ID, map[string]any{"planId": planID, "period": period}); err != nil {
		return Order{}, err
	}
	return *order, nil
}

func (s *Service) paymentDetailsForOrderLocked(provider string, orderID string, planID string, period string, price plans.Price) (string, string, string, error) {
	switch provider {
	case "stripe":
		if s.cfg.AppEnv == "production" && !s.cfg.StripeEnabled {
			return "", "", "", E(http.StatusServiceUnavailable, "payment_provider_not_configured", "Stripe is not enabled")
		}
		checkoutURL, err := s.renderPaymentURLLocked(s.cfg.Stripe.CheckoutURLTemplate, provider, orderID, planID, period, price)
		if err != nil {
			return "", "", "", err
		}
		if checkoutURL == "" {
			if s.cfg.AppEnv == "production" {
				return "", "", "", E(http.StatusServiceUnavailable, "payment_provider_not_configured", "Stripe checkout URL template is not configured")
			}
			checkoutURL = fmt.Sprintf("%s/dev/checkout/%s?orderId=%s", strings.TrimRight(s.cfg.PublicURL, "/"), planID, url.QueryEscape(orderID))
		}
		return checkoutURL, "", "", nil
	case "epusdt":
		if s.cfg.AppEnv == "production" && !s.cfg.EpusdtEnabled {
			return "", "", "", E(http.StatusServiceUnavailable, "payment_provider_not_configured", "Epusdt is not enabled")
		}
		checkoutURL, err := s.renderPaymentURLLocked(s.cfg.Epusdt.CheckoutURLTemplate, provider, orderID, planID, period, price)
		if err != nil {
			return "", "", "", err
		}
		address := strings.TrimSpace(s.cfg.Epusdt.Address)
		chain := strings.TrimSpace(s.cfg.Epusdt.Chain)
		if chain == "" {
			chain = "USDT-TRC20"
		}
		if checkoutURL == "" || address == "" {
			if s.cfg.AppEnv == "production" {
				return "", "", "", E(http.StatusServiceUnavailable, "payment_provider_not_configured", "Epusdt checkout URL and payment address are not configured")
			}
			if checkoutURL == "" {
				checkoutURL = fmt.Sprintf("%s/dev/checkout/%s?orderId=%s", strings.TrimRight(s.cfg.PublicURL, "/"), planID, url.QueryEscape(orderID))
			}
			if address == "" {
				address = "TDEVPASTEBOXUSDTTRC20"
			}
		}
		return checkoutURL, address, chain, nil
	default:
		return "", "", "", E(http.StatusBadRequest, "invalid_provider", "provider must be stripe or epusdt")
	}
}

func (s *Service) renderPaymentURLLocked(template string, provider string, orderID string, planID string, period string, price plans.Price) (string, error) {
	template = strings.TrimSpace(template)
	if template == "" {
		return "", nil
	}
	successURL := strings.TrimRight(s.cfg.PublicURL, "/") + "/?view=billing&orderId=" + url.QueryEscape(orderID)
	cancelURL := strings.TrimRight(s.cfg.PublicURL, "/") + "/?view=billing&orderId=" + url.QueryEscape(orderID) + "&status=cancelled"
	replacer := strings.NewReplacer(
		"{order_id}", url.QueryEscape(orderID),
		"{orderId}", url.QueryEscape(orderID),
		"{plan_id}", url.QueryEscape(planID),
		"{planId}", url.QueryEscape(planID),
		"{period}", url.QueryEscape(period),
		"{price_id}", url.QueryEscape(price.ID),
		"{priceId}", url.QueryEscape(price.ID),
		"{amount_cents}", url.QueryEscape(fmt.Sprintf("%d", price.AmountCents)),
		"{amountCents}", url.QueryEscape(fmt.Sprintf("%d", price.AmountCents)),
		"{currency}", url.QueryEscape(price.Currency),
		"{provider}", url.QueryEscape(provider),
		"{success_url}", url.QueryEscape(successURL),
		"{successUrl}", url.QueryEscape(successURL),
		"{cancel_url}", url.QueryEscape(cancelURL),
		"{cancelUrl}", url.QueryEscape(cancelURL),
	)
	rendered := replacer.Replace(template)
	parsed, err := url.Parse(rendered)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", E(http.StatusServiceUnavailable, "payment_provider_not_configured", "payment checkout URL template rendered an invalid URL")
	}
	if s.cfg.AppEnv == "production" && parsed.Scheme != "https" {
		return "", E(http.StatusServiceUnavailable, "payment_provider_not_configured", "production payment checkout URL must use https")
	}
	if s.cfg.AppEnv == "production" && isLocalHost(parsed.Hostname()) {
		return "", E(http.StatusServiceUnavailable, "payment_provider_not_configured", "production payment checkout URL must not point to a local host")
	}
	return rendered, nil
}

func (s *Service) MarkOrderPaidWithContext(ctx context.Context, actorID string, orderID string, txID string, reason string) (Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireAdminLocked(ctx, actorID); err != nil {
		return Order{}, err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Order{}, E(http.StatusBadRequest, "manual_reason_required", "manual payment corrections require a support reason")
	}
	if len(reason) > 500 {
		return Order{}, E(http.StatusBadRequest, "manual_reason_too_long", "manual payment correction reason must be 500 characters or fewer")
	}
	txID = strings.TrimSpace(txID)
	metadata := map[string]any{
		"manual": true,
		"reason": reason,
	}
	return s.markOrderPaidLocked(ctx, actorID, orderID, txID, "manual.payment:"+orderID+":"+txID, metadata)
}

func (s *Service) ProcessBillingWebhookWithContext(ctx context.Context, input BillingWebhookInput) (WebhookEvent, *Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	provider := normalizeProvider(input.Provider)
	eventType := strings.TrimSpace(input.EventType)
	if provider == "" || eventType == "" {
		return WebhookEvent{}, nil, E(http.StatusBadRequest, "invalid_webhook", "provider and event type are required")
	}
	orderID := strings.TrimSpace(input.OrderID)
	txID := strings.TrimSpace(input.TxID)
	idempotencyKey := strings.TrimSpace(input.IdempotencyKey)
	if idempotencyKey == "" {
		idempotencyKey = provider + ":" + eventType + ":" + orderID + ":" + txID
	}
	if event, ok, err := s.webhookEventByKeyLocked(ctx, idempotencyKey); err != nil {
		return WebhookEvent{}, nil, err
	} else if ok {
		var order *Order
		if event.TargetID != "" {
			loaded, err := s.orderByIDLocked(ctx, event.TargetID)
			if err != nil && !isAppStatus(err, http.StatusNotFound) {
				return WebhookEvent{}, nil, err
			}
			order = loaded
		}
		return event, order, nil
	}
	metadata := cloneMetadata(input.Metadata)
	if txID != "" {
		metadata["txId"] = txID
	}

	switch eventType {
	case "payment.succeeded", "checkout.session.completed", "invoice.paid", "epusdt.payment.succeeded":
		order, err := s.markOrderPaidLocked(ctx, "webhook:"+provider, orderID, txID, idempotencyKey, metadata)
		if err != nil {
			return WebhookEvent{}, nil, err
		}
		event, ok, err := s.webhookEventByKeyLocked(ctx, idempotencyKey)
		if err != nil {
			return WebhookEvent{}, nil, err
		}
		if !ok {
			return WebhookEvent{}, nil, E(http.StatusInternalServerError, "webhook_event_missing", "webhook event was not recorded")
		}
		return event, &order, nil
	case "payment.failed", "invoice.payment_failed", "epusdt.payment.failed":
		result, err := s.applyBillingTransactionLocked(ctx, BillingTransactionInput{
			ActorID: "webhook:" + provider, OrderID: orderID, DesiredStatus: "failed", EventID: s.newID("wh"),
			EventProvider: provider, EventType: eventType, IdempotencyKey: idempotencyKey, Metadata: metadata,
			AuditID: s.newID("aud"), OccurredAt: s.now().UTC(),
		})
		if err != nil {
			return WebhookEvent{}, nil, err
		}
		return billingResultEvent(result), result.Order, nil
	case "subscription.deleted", "subscription.canceled", "customer.subscription.deleted", "epusdt.payment.canceled":
		result, err := s.applyBillingTransactionLocked(ctx, BillingTransactionInput{
			ActorID: "webhook:" + provider, OrderID: orderID, DesiredStatus: "canceled", RevokePlan: true,
			EventID: s.newID("wh"), EventProvider: provider, EventType: eventType, IdempotencyKey: idempotencyKey,
			Metadata: metadata, AuditID: s.newID("aud"), OccurredAt: s.now().UTC(),
		})
		if err != nil {
			return WebhookEvent{}, nil, err
		}
		return billingResultEvent(result), result.Order, nil
	case "refund.created", "charge.refunded":
		result, err := s.applyBillingTransactionLocked(ctx, BillingTransactionInput{
			ActorID: "webhook:" + provider, OrderID: orderID, DesiredStatus: "refunded", RevokePlan: true,
			EventID: s.newID("wh"), EventProvider: provider, EventType: eventType, IdempotencyKey: idempotencyKey,
			Metadata: metadata, AuditID: s.newID("aud"), OccurredAt: s.now().UTC(),
		})
		if err != nil {
			return WebhookEvent{}, nil, err
		}
		return billingResultEvent(result), result.Order, nil
	case "payment.expired", "checkout.session.expired", "epusdt.payment.expired":
		result, err := s.applyBillingTransactionLocked(ctx, BillingTransactionInput{
			ActorID: "webhook:" + provider, OrderID: orderID, DesiredStatus: "expired", EventID: s.newID("wh"),
			EventProvider: provider, EventType: eventType, IdempotencyKey: idempotencyKey, Metadata: metadata,
			AuditID: s.newID("aud"), OccurredAt: s.now().UTC(),
		})
		if err != nil {
			return WebhookEvent{}, nil, err
		}
		return billingResultEvent(result), result.Order, nil
	default:
		result, err := s.applyBillingTransactionLocked(ctx, BillingTransactionInput{
			ActorID: "webhook:" + provider, OrderID: orderID, EventID: s.newID("wh"), EventProvider: provider,
			EventType: eventType, IdempotencyKey: idempotencyKey, Metadata: metadata, OccurredAt: s.now().UTC(),
		})
		if err != nil {
			return WebhookEvent{}, nil, err
		}
		return billingResultEvent(result), result.Order, nil
	}
}

func (s *Service) applyLoadedOrderLifecycleStatusLocked(ctx context.Context, actorID string, order *Order, status string, revokePlan bool, metadata map[string]any) error {
	if order == nil {
		return nil
	}
	_, err := s.applyBillingTransactionLocked(ctx, BillingTransactionInput{
		ActorID: actorID, OrderID: order.ID, DesiredStatus: status, RevokePlan: revokePlan,
		Metadata: metadata, AuditID: s.newID("aud"), OccurredAt: s.now().UTC(),
	})
	return err
}

func (s *Service) ReplayWebhookEventWithContext(ctx context.Context, actorID string, eventID string) (WebhookEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireAdminLocked(ctx, actorID); err != nil {
		return WebhookEvent{}, err
	}
	original, err := s.webhookEventByIDLocked(ctx, eventID)
	if err != nil {
		return WebhookEvent{}, err
	}
	if original == nil {
		return WebhookEvent{}, E(http.StatusNotFound, "webhook_event_not_found", "webhook event not found")
	}
	metadata := cloneMetadata(original.Metadata)
	metadata["replayedFrom"] = original.ID
	replayKey := original.IdempotencyKey + ":replay:" + s.newID("rpl")
	var event WebhookEvent
	if original.EventType == "payment.succeeded" && original.TargetID != "" {
		if order, err := s.orderByIDLocked(ctx, original.TargetID); err != nil && !isAppStatus(err, http.StatusNotFound) {
			return WebhookEvent{}, err
		} else if order != nil && order.Status != "paid" {
			_, err := s.markOrderPaidLocked(ctx, actorID, original.TargetID, stringFromMetadata(original.Metadata, "txId"), replayKey, metadata)
			if err != nil {
				return WebhookEvent{}, err
			}
			loaded, ok, err := s.webhookEventByKeyLocked(ctx, replayKey)
			if err != nil {
				return WebhookEvent{}, err
			}
			if ok {
				event = loaded
			}
		}
	}
	if event.ID == "" {
		var err error
		event, err = s.recordWebhookEventLocked(ctx, original.Provider, "webhook.replayed", original.TargetID, replayKey, metadata)
		if err != nil {
			return WebhookEvent{}, err
		}
	}
	if err := s.auditLocked(ctx, actorID, "admin.webhook_replay", original.ID, map[string]any{"replayEventId": event.ID}); err != nil {
		return WebhookEvent{}, err
	}
	return event, nil
}

func (s *Service) markOrderPaidLocked(ctx context.Context, actorID string, orderID string, txID string, eventKey string, metadata map[string]any) (Order, error) {
	input := BillingTransactionInput{
		ActorID: actorID, OrderID: orderID, TxID: strings.TrimSpace(txID), DesiredStatus: "paid",
		Metadata: metadata, AuditID: s.newID("aud"), MailID: s.newID("mail"), OccurredAt: s.now().UTC(),
	}
	if strings.TrimSpace(eventKey) != "" {
		input.EventID = s.newID("wh")
		input.EventType = "payment.succeeded"
		input.IdempotencyKey = strings.TrimSpace(eventKey)
	}
	result, err := s.applyBillingTransactionLocked(ctx, input)
	if err != nil {
		return Order{}, err
	}
	if result.Order == nil {
		return Order{}, E(http.StatusNotFound, "order_not_found", "order not found")
	}
	return *result.Order, nil
}

func (s *Service) applyBillingTransactionLocked(ctx context.Context, input BillingTransactionInput) (BillingTransactionResult, error) {
	if s.transactions != nil {
		s.mu.Unlock()
		result, err := s.transactions.ApplyBilling(ctx, input)
		s.mu.Lock()
		if err != nil {
			return BillingTransactionResult{}, err
		}
		s.cacheBillingTransactionResultLocked(result)
		return result, nil
	}

	var order *Order
	if strings.TrimSpace(input.OrderID) != "" {
		loaded, err := s.orderByIDLocked(ctx, input.OrderID)
		if err != nil {
			if input.DesiredStatus == "paid" || !isAppStatus(err, http.StatusNotFound) {
				return BillingTransactionResult{}, err
			}
		} else {
			cloned := *loaded
			order = &cloned
		}
	}
	var user *User
	if order != nil && (input.DesiredStatus == "paid" || input.RevokePlan) {
		loaded, err := s.userByIDLocked(ctx, order.UserID)
		if err != nil {
			return BillingTransactionResult{}, err
		}
		cloned := *loaded
		user = &cloned
	}
	result, err := BuildBillingTransaction(input, order, user)
	if err != nil {
		return BillingTransactionResult{}, err
	}
	if result.User != nil && s.auth.Users != nil {
		if err := s.auth.Users.UpdateUser(ctx, *result.User); err != nil {
			return BillingTransactionResult{}, err
		}
	}
	if result.Audit != nil && result.Order != nil && s.ops.Orders != nil {
		if err := s.ops.Orders.UpdateOrder(ctx, *result.Order); err != nil {
			return BillingTransactionResult{}, err
		}
	}
	if result.Audit != nil && s.audit != nil {
		if err := s.audit.RecordAuditLog(ctx, *result.Audit); err != nil {
			return BillingTransactionResult{}, err
		}
	}
	if result.Event != nil && s.ops.WebhookEvents != nil {
		if err := s.ops.WebhookEvents.CreateWebhookEvent(ctx, *result.Event); err != nil {
			if !errors.Is(err, ErrStoreConflict) {
				return BillingTransactionResult{}, err
			}
			loaded, loadErr := s.ops.WebhookEvents.WebhookEventByIdempotencyKey(ctx, result.Event.IdempotencyKey)
			if loadErr != nil {
				return BillingTransactionResult{}, loadErr
			}
			result.Event = &loaded
			result.ExistingEvent = true
		}
	}
	if result.Mail != nil && s.ops.Mails != nil {
		if err := s.ops.Mails.QueueMail(ctx, *result.Mail); err != nil {
			return BillingTransactionResult{}, err
		}
	}
	s.cacheBillingTransactionResultLocked(result)
	return result, nil
}

func (s *Service) cacheBillingTransactionResultLocked(result BillingTransactionResult) {
	if result.User != nil {
		s.cacheUserLocked(*result.User)
	}
	if result.Order != nil {
		s.cacheOrderLocked(*result.Order)
	}
	if result.Event != nil {
		s.cacheWebhookEventLocked(*result.Event)
	}
	if result.Audit != nil {
		s.cacheAuditLogLocked(*result.Audit)
	}
	if result.Mail != nil {
		s.cacheMailLocked(*result.Mail)
	}
}

func billingResultEvent(result BillingTransactionResult) WebhookEvent {
	if result.Event == nil {
		return WebhookEvent{}
	}
	return *result.Event
}

func (s *Service) ListOrdersWithContext(ctx context.Context, userID string) ([]Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.activeUserLocked(ctx, userID); err != nil {
		return nil, err
	}
	out, err := s.ordersByUserLocked(ctx, userID)
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (s *Service) RunBillingReconciliationWithContext(ctx context.Context, actorID string) (map[string]int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	actorID = strings.TrimSpace(actorID)
	if actorID != "" {
		if err := s.requireAdminLocked(ctx, actorID); err != nil {
			return nil, err
		}
	}

	if store, ok := s.ops.Orders.(ExpiredOrderStore); ok {
		result := map[string]int{"checkedOrders": 0, "pendingOrders": 0, "expiredOrders": 0}
		actor := defaultString(actorID, "system:billing_reconcile")
		for {
			s.mu.Unlock()
			orders, err := store.ListExpiredPendingOrders(ctx, s.now().UTC(), 100)
			s.mu.Lock()
			if err != nil {
				return nil, err
			}
			for _, order := range orders {
				result["checkedOrders"]++
				result["pendingOrders"]++
				if err := s.applyLoadedOrderLifecycleStatusLocked(ctx, actor, &order, "expired", false, map[string]any{"source": "billing_reconcile"}); err != nil {
					return nil, err
				}
				result["expiredOrders"]++
			}
			if len(orders) < 100 {
				return result, nil
			}
		}
	}
	if s.ops.Orders != nil {
		s.mu.Unlock()
		orders, err := s.ops.Orders.ListOrders(ctx)
		s.mu.Lock()
		if err != nil {
			return nil, err
		}
		s.ordersByID = map[string]*Order{}
		for _, order := range orders {
			s.cacheOrderLocked(order)
		}
	}
	actor := actorID
	if actor == "" {
		actor = "system:billing_reconcile"
	}
	now := s.now().UTC()
	result := map[string]int{"checkedOrders": 0, "pendingOrders": 0, "expiredOrders": 0}
	for _, order := range s.ordersByID {
		result["checkedOrders"]++
		if order.Status != "pending" {
			continue
		}
		result["pendingOrders"]++
		if order.ExpiresAt == nil || order.ExpiresAt.After(now) {
			continue
		}
		if err := s.applyLoadedOrderLifecycleStatusLocked(ctx, actor, order, "expired", false, map[string]any{"source": "billing_reconcile"}); err != nil {
			return nil, err
		}
		result["expiredOrders"]++
	}
	return result, nil
}

func (s *Service) RunBillingReconciliation(actorID string) (map[string]int, error) {
	return s.RunBillingReconciliationWithContext(context.Background(), actorID)
}

func (s *Service) ListOrders(userID string) ([]Order, error) {
	return s.ListOrdersWithContext(context.Background(), userID)
}

func (s *Service) ReplayWebhookEvent(actorID string, eventID string) (WebhookEvent, error) {
	return s.ReplayWebhookEventWithContext(context.Background(), actorID, eventID)
}

func (s *Service) ProcessBillingWebhook(input BillingWebhookInput) (WebhookEvent, *Order, error) {
	return s.ProcessBillingWebhookWithContext(context.Background(), input)
}

func (s *Service) MarkOrderPaid(actorID string, orderID string, txID string, reason string) (Order, error) {
	return s.MarkOrderPaidWithContext(context.Background(), actorID, orderID, txID, reason)
}

func (s *Service) CreateOrder(userID string, provider string, planID string, period string) (Order, error) {
	return s.CreateOrderWithContext(context.Background(), userID, provider, planID, period)
}

func (s *Service) Quota(userID string) (QuotaView, error) {
	return s.QuotaWithContext(context.Background(), userID)
}
