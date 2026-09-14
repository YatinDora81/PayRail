package gatewayrecon

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"time"

	"github.com/payrail/go/internal/events"
	gw "github.com/payrail/go/internal/gatewayclient"
	"github.com/payrail/go/internal/store"
	"github.com/payrail/go/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type Gateway interface {
	FetchPayment(ctx context.Context, gateway, gatewayOrderID string) (gw.FetchResult, error)
	FetchRefund(ctx context.Context, gateway, gatewayRefundID, idempotencyKey, gatewayOrderID string) (gw.RefundFetchResult, error)
	CreateRefund(ctx context.Context, gateway, gatewayPaymentID, gatewayOrderID string, amountMinor int64, currency, idempotencyKey string) (gw.CreateRefundResult, error)
	FindOrderByReference(ctx context.Context, gateway, merchantReference string) (gw.OrderLookupResult, error)
}

type Store interface {
	OrdersUnstamped(ctx context.Context, olderThan time.Duration, limit int) ([]store.UnstampedOrder, error)
	StampGateway(ctx context.Context, orderID, gateway, gatewayOrderID string) error
	OrdersAwaitingSettlement(ctx context.Context, olderThan time.Duration, limit int) ([]store.StuckOrder, error)
	FailStuckOrder(ctx context.Context, orderID string) error
	RefundsAwaitingResolution(ctx context.Context, olderThan time.Duration, limit int) ([]store.StuckRefund, error)
	MarkRefundFailed(ctx context.Context, refundID string) error
	ParkedCapturesOnTerminalOrders(ctx context.Context, limit int) ([]store.OrphanCapture, error)
	MarkNeedsReview(ctx context.Context, deadLetterID string) error
	CreateOrphanRefund(ctx context.Context, orderID, gatewayPaymentID, gateway string, amountMinor int64, currency, idempotencyKey string) (string, error)
	LogReconciliation(ctx context.Context, c store.Correction) error
	EnqueueOutbox(ctx context.Context, topic, partitionKey string, payload []byte) error
}

type Options struct {
	StuckOrderGrace      time.Duration
	OrphanOrderGrace     time.Duration
	StuckRefundGrace     time.Duration
	AutoRefundLimitMinor int64
	Batch                int
}

func (o Options) withDefaults() Options {
	if o.StuckOrderGrace <= 0 {
		o.StuckOrderGrace = 15 * time.Minute
	}
	if o.OrphanOrderGrace <= 0 {
		o.OrphanOrderGrace = 5 * time.Minute
	}
	if o.StuckRefundGrace <= 0 {
		o.StuckRefundGrace = 30 * time.Minute
	}
	if o.Batch <= 0 {
		o.Batch = 200
	}
	return o
}

type Service struct {
	db      Store
	gateway Gateway
	opts    Options
	logger  *slog.Logger
}

func NewService(db Store, g Gateway, opts Options, logger *slog.Logger) *Service {
	return &Service{db: db, gateway: g, opts: opts.withDefaults(), logger: logger}
}

const (
	sweepOrphanOrders   = "orphan_orders"
	sweepStuckOrders    = "stuck_orders"
	sweepStuckRefunds   = "stuck_refunds"
	sweepOrphanCaptures = "orphan_captures"
)

func (s *Service) Pass(ctx context.Context) error {
	ctx, span := telemetry.Tracer("payrail/gateway-reconciler").Start(ctx, "gwrecon.pass")
	defer span.End()

	corrections := 0

	for _, sw := range []struct {
		name string
		run  func(context.Context) (int, error)
	}{
		{sweepOrphanOrders, s.sweepOrphanOrders},
		{sweepStuckOrders, s.sweepStuckOrders},
		{sweepStuckRefunds, s.sweepStuckRefunds},
		{sweepOrphanCaptures, s.sweepOrphanCaptures},
	} {
		n, err := sw.run(ctx)
		if err != nil {
			s.logger.Error("sweep failed", "sweep", sw.name, "err", err)
			return err
		}
		corrections += n
	}
	if corrections > 0 {
		s.logger.Warn("pass corrected provider drift", "corrections", corrections)
	}
	return nil

}

func (s *Service) backlog(ctx context.Context, sweep string, n int) {
	telemetry.Gauge("payrail_gwrecon_backlog").Record(ctx, int64(n),
		metric.WithAttributes(attribute.String("sweep", sweep)))
}

func (s *Service) providerError(ctx context.Context, sweep string) {
	telemetry.Counter("payrail_gwrecon_provider_errors_total").Add(ctx, 1,
		metric.WithAttributes(attribute.String("sweep", sweep)))
}

func (s *Service) correction(ctx context.Context, sweep, outcome string) {
	telemetry.Counter("payrail_gwrecon_corrections_total").Add(ctx, 1,
		metric.WithAttributes(attribute.String("sweep", sweep), attribute.String("outcome", outcome)))
}

func (s *Service) record(ctx context.Context, c store.Correction) {
	if err := s.db.LogReconciliation(ctx, c); err != nil {
		s.logger.Error("reconciliation log failed", "kind", c.Kind, "deadLetterId", c.DeadLetterID, "note", c.Note, "err", err)
	}
}

const (
	outcomeRecovered   = "recovered"
	outcomeReemitted   = "reemitted"
	outcomeFailed      = "failed"
	outcomeAutoRefund  = "auto_refunded"
	outcomeNeedsReview = "needs_review"
)

func (s *Service) sweepOrphanOrders(ctx context.Context) (int, error) {
	orphans, err := s.db.OrdersUnstamped(ctx, s.opts.OrphanOrderGrace, s.opts.Batch)
	if err != nil {
		return 0, err
	}

	s.backlog(ctx, sweepOrphanOrders, len(orphans))

	recovered := 0
	for _, o := range orphans {
		found, err := s.gateway.FindOrderByReference(ctx, o.Gateway, o.ID)
		if err != nil {
			s.logger.Warn("orphan order lookup failed", "order", o.ID, "gateway", o.Gateway, "err", err)
			s.providerError(ctx, sweepOrphanOrders)
			continue
		}
		if !found.Found {
			continue
		}
		if err := s.db.StampGateway(ctx, o.ID, o.Gateway, found.GatewayOrderID); err != nil {
			s.logger.Error("stamp recovered order", "order", o.ID, "err", err)
			continue
		}
		s.logger.Info("orphan order recovered from provider truth",
			"order", o.ID, "gatewayOrderId", found.GatewayOrderID, "providerStatus", found.Status)
		s.correction(ctx, sweepOrphanOrders, outcomeRecovered)
		s.record(ctx, store.Correction{
			Kind:      store.CorrectionOrphanOrderRecovered,
			Note:      "order " + o.ID + ": gatewayOrderId " + found.GatewayOrderID + " recovered by reference at " + o.Gateway + " (provider status " + found.Status + ")",
			Corrected: true,
		})
		recovered++

	}
	return recovered, nil

}

func (s *Service) sweepStuckOrders(ctx context.Context) (int, error) {

	stuck, err := s.db.OrdersAwaitingSettlement(ctx, s.opts.StuckOrderGrace, s.opts.Batch)
	if err != nil {
		return 0, err
	}
	s.backlog(ctx, sweepStuckOrders, len(stuck))

	corrected := 0
	for _, o := range stuck {
		pay, err := s.gateway.FetchPayment(ctx, o.Gateway, o.GatewayOrderID)
		if err != nil {
			s.logger.Warn("fetch payment failed", "order", o.ID, "gateway", o.Gateway, "err", err)
			s.providerError(ctx, sweepStuckOrders)
			continue // transient — next tick retries
		}
		switch pay.Status {
		case gw.Captured:
			if s.healCapture(ctx, o, pay) {
				corrected++
			}
		case gw.Failed, gw.Expired:
			if s.failOrder(ctx, o, pay) {
				corrected++
			}
		default:
		}
	}
	return corrected, nil

}

func (s *Service) healCapture(ctx context.Context, o store.StuckOrder, pay gw.FetchResult) bool {
	if !s.emitCaptured(ctx, o, pay) {
		return false
	}
	s.correction(ctx, sweepStuckOrders, outcomeReemitted)
	s.record(ctx, store.Correction{
		Kind:      store.CorrectionOrderCaptureHealed,
		Note:      "order " + o.ID + ": provider says CAPTURED (" + pay.GatewayPaymentID + "), no webhook arrived — " + events.TypeCapturedReconciled + " re-emitted",
		Corrected: true,
	})
	return true
}

const eventIDPrefix = "gwrecon:"

func syntheticEventID(id string) string {
	return eventIDPrefix + id
}

func (s *Service) emitCaptured(ctx context.Context, o store.StuckOrder, pay gw.FetchResult) bool {
	ev := events.PaymentEvent{
		Kind:             events.KindPayment,
		Gateway:          o.Gateway,
		GatewayOrderID:   o.GatewayOrderID,
		GatewayPaymentID: pay.GatewayPaymentID,
		EventID:          syntheticEventID(pay.GatewayPaymentID),
		EventType:        events.TypeCapturedReconciled,
		AmountMinor:      pay.AmountMinor,
		Currency:         pay.Currency,
		OccurredAt:       time.Now().UTC(),
	}
	return s.enqueue(ctx, o.GatewayOrderID, ev, "order", o.ID)
}

func (s *Service) enqueue(ctx context.Context, key string, ev events.PaymentEvent, what, id string) bool {
	payload, err := json.Marshal(ev)
	if err != nil {
		s.logger.Error("marshal reconcile event", what, id, "err", err)
		return false
	}
	if err := s.db.EnqueueOutbox(ctx, events.TopicPaymentEvents, key, payload); err != nil {
		s.logger.Error("enqueue reconcile event", what, id, "err", err) // next tick retries
		return false
	}
	s.logger.Info("re-emitted from provider truth", what, id, "kind", ev.Kind, "eventId", ev.EventID)
	return true
}

func (s *Service) failOrder(ctx context.Context, o store.StuckOrder, pay gw.FetchResult) bool {
	if err := s.db.FailStuckOrder(ctx, o.ID); err != nil {
		s.logger.Error("fail stuck order", "order", o.ID, "providerStatus", pay.Status, "err", err)
		return false
	}
	s.logger.Info("order failed from provider truth", "order", o.ID, "providerStatus", pay.Status)
	s.correction(ctx, sweepStuckOrders, outcomeFailed)
	s.record(ctx, store.Correction{
		Kind:      store.CorrectionOrderFailedFromProvider,
		Note:      "order " + o.ID + ": provider says " + pay.Status + " — FAILED, reservations released",
		Corrected: true,
	})
	return true
}

func (s *Service) sweepStuckRefunds(ctx context.Context) (int, error) {

	stuck, err := s.db.RefundsAwaitingResolution(ctx, s.opts.StuckRefundGrace, s.opts.Batch)
	if err != nil {
		return 0, err
	}
	s.backlog(ctx, sweepStuckRefunds, len(stuck))

	corrected := 0
	for _, r := range stuck {
		ref, err := s.gateway.FetchRefund(ctx, r.Gateway, r.GatewayRefundID, r.IdempotencyKey, r.GatewayOrderID)
		if err != nil {
			s.logger.Warn("fetch refund failed", "refund", r.ID, "gateway", r.Gateway, "err", err)
			s.providerError(ctx, sweepStuckRefunds)
			continue
		}
		switch ref.Status {
		case gw.Processed:
			if s.healRefund(ctx, r, ref) {
				corrected++
			}
		case gw.RefundFailed:
			if s.failRefund(ctx, r) {
				corrected++
			}
		default:
		}
	}
	return corrected, nil
}

func (s *Service) healRefund(ctx context.Context, r store.StuckRefund, ref gw.RefundFetchResult) bool {
	if !s.emitRefundProcessed(ctx, r, ref) {
		return false
	}
	s.correction(ctx, sweepStuckRefunds, outcomeReemitted)
	s.record(ctx, store.Correction{
		Kind:      store.CorrectionRefundHealed,
		Note:      "refund " + r.ID + ": provider says PROCESSED, no webhook arrived — " + events.TypeRefundProcessedReconciled + " re-emitted",
		Corrected: true,
	})
	return true
}

func (s *Service) emitRefundProcessed(ctx context.Context, r store.StuckRefund, ref gw.RefundFetchResult) bool {
	refundID := ref.GatewayRefundID
	if refundID == "" {
		refundID = r.GatewayRefundID
	}
	ev := events.PaymentEvent{
		Kind:             events.KindRefund,
		Gateway:          r.Gateway,
		GatewayOrderID:   r.GatewayOrderID,
		GatewayPaymentID: r.GatewayPaymentID,
		GatewayRefundID:  refundID,
		EventID:          syntheticEventID(r.ID),
		EventType:        events.TypeRefundProcessedReconciled,
		AmountMinor:      ref.AmountMinor,
		Currency:         r.Currency,
		OccurredAt:       time.Now().UTC(),
	}
	return s.enqueue(ctx, r.GatewayOrderID, ev, "refund", r.ID)
}

func (s *Service) failRefund(ctx context.Context, r store.StuckRefund) bool {
	if err := s.db.MarkRefundFailed(ctx, r.ID); err != nil {
		s.logger.Error("mark refund failed", "refund", r.ID, "err", err)
		return false
	}
	s.logger.Warn("refund failed from provider truth", "refund", r.ID, "order", r.OrderID)
	s.correction(ctx, sweepStuckRefunds, outcomeFailed)
	s.record(ctx, store.Correction{
		Kind:      store.CorrectionRefundFailedFromProvider,
		Note:      "refund " + r.ID + ": provider says FAILED — marked FAILED, refundable ceiling re-opened",
		Corrected: true,
	})
	return true
}

func (s *Service) sweepOrphanCaptures(ctx context.Context) (int, error) {
	orphans, err := s.db.ParkedCapturesOnTerminalOrders(ctx, s.opts.Batch)
	if err != nil {
		return 0, err
	}
	s.backlog(ctx, sweepOrphanCaptures, len(orphans))

	resolved := 0
	for _, o := range orphans {
		if o.AmountMinor > s.opts.AutoRefundLimitMinor {
			if s.handOver(ctx, o) {
				resolved++
			}
			continue
		}
		if s.autoRefund(ctx, o) {
			resolved++
		}
	}
	return resolved, nil
}

func (s *Service) handOver(ctx context.Context, o store.OrphanCapture) bool {
	if err := s.db.MarkNeedsReview(ctx, o.DeadLetterID); err != nil {
		s.logger.Error("mark needs review failed", "deadLetterId", o.DeadLetterID, "eventId", o.EventID, "err", err)
		return false
	}
	s.logger.Warn("orphan capture above auto-refund ceiling — handed to finance",
		"deadLetterId", o.DeadLetterID, "order", o.OrderID, "amountMinor", o.AmountMinor, "currency", o.Currency)
	s.correction(ctx, sweepOrphanCaptures, outcomeNeedsReview)
	s.record(ctx, store.Correction{
		Kind:         store.CorrectionOrphanCaptureNeedsReview,
		DeadLetterID: o.DeadLetterID,
		Note:         "capture " + o.GatewayPaymentID + " on terminal order " + o.OrderID + ": " + strconv.FormatInt(o.AmountMinor, 10) + " " + o.Currency + " exceeds the auto-refund ceiling — NEEDS_REVIEW",
		Corrected:    false, // nothing moved; a human owns it now
	})
	return true
}

func orphanRefundKey(eventID string) string {
	return "orphan:" + eventID
}

func (s *Service) autoRefund(ctx context.Context, o store.OrphanCapture) bool {
	key := orphanRefundKey(o.EventID)

	refundID, err := s.db.CreateOrphanRefund(ctx, o.OrderID, o.GatewayPaymentID, o.Gateway, o.AmountMinor, o.Currency, key)
	if err != nil {
		s.logger.Error("orphan refund row failed", "eventId", o.EventID, "err", err)
		return false
	}

	if _, err := s.gateway.CreateRefund(ctx, o.Gateway, o.GatewayPaymentID, o.GatewayOrderID, o.AmountMinor, "", key); err != nil {
		s.logger.Error("orphan auto-refund failed", "eventId", o.EventID, "refund", refundID, "err", err)
		s.providerError(ctx, sweepOrphanCaptures)
		return false
	}
	s.logger.Warn("orphan capture auto-refunded from provider truth",
		"deadLetterId", o.DeadLetterID, "order", o.OrderID, "refund", refundID, "amountMinor", o.AmountMinor, "currency", o.Currency)

	s.correction(ctx, sweepOrphanCaptures, outcomeAutoRefund)
	s.record(ctx, store.Correction{
		Kind:         store.CorrectionOrphanCaptureAutoRefunded,
		DeadLetterID: o.DeadLetterID,
		Note:         "capture " + o.GatewayPaymentID + " on terminal order " + o.OrderID + ": " + strconv.FormatInt(o.AmountMinor, 10) + " " + o.Currency + " refunded at " + o.Gateway + " as " + refundID + " (key " + key + ")",
		Corrected:    true,
	})

	return true
}

func (s *Service) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if err := s.Pass(ctx); err != nil {
			s.logger.Error("pass failed", "err", err) 
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
