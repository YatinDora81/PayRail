package settlement

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/payrail/go/internal/events"
	"github.com/payrail/go/internal/kafka"
	"github.com/payrail/go/internal/store"
)

type Service struct {
	db     *store.Store
	logger *slog.Logger
}

func NewService(db *store.Store, logger *slog.Logger) *Service {
	return &Service{db: db, logger: logger}
}

func (s *Service) HandleInvoice(ctx context.Context, m kafka.Message) error {
	var ev events.OrderPaid
	if err := json.Unmarshal(m.Value, &ev); err != nil {
		return s.park(ctx, m, "unparseable order.paid for invoicing")
	}

	amount, err := strconv.ParseInt(ev.AmountMinor, 10, 64)
	if err != nil {
		return s.park(ctx, m, "unparseable order.paid amount "+ev.AmountMinor)
	}

	if err := s.db.AssignInvoice(ctx, ev.OrderID, ev.Currency, ev.Currency, amount); err != nil {
		return err
	}
	return nil

}

func (s *Service) park(ctx context.Context, m kafka.Message, reason string) error {
	s.logger.Error("parking payment event", "reason", reason, "key", string(m.Key))
	return s.db.InsertDeadLetter(ctx, "settlement-worker", m.Topic, string(m.Key), m.Value, reason)
}

var captureAllowedFrom = map[string]bool{
	"CREATED": true, "PENDING_PAYMENT": true, "AUTHORIZED": true,
}

func (s *Service) Handle(ctx context.Context, m kafka.Message) error {
	var ev events.PaymentEvent
	if err := json.Unmarshal(m.Value, &ev); err != nil {
		return s.park(ctx, m, "unparseable payment event") // nothing to retry
	}

	switch ev.Kind {
	case events.KindRefund:
		return s.handleRefund(ctx, m, ev)
	case events.KindDispute:
		return s.handleDispute(ctx, m, ev)
	}

	switch classifyCapture(ev.Gateway, ev.EventType) {
	case captureNo:
		return nil
	case captureUnknown:
		return s.park(ctx, m, "unrecognized capture-like event "+ev.EventType)
	}

	target, err := s.db.FindOrderForSettlement(ctx, ev.Gateway, ev.GatewayOrderID)
	if errors.Is(err, store.ErrNotFound) {
		return s.park(ctx, m, "capture for unknown order "+ev.GatewayOrderID)
	}
	if err != nil {
		return err
	}
	if target.Status == "PAID" {
		return nil
	}

	if !captureAllowedFrom[target.Status] {
		return s.park(ctx, m, "stale capture: order is "+target.Status)
	}

	if ev.AmountMinor != target.FinalAmountMinor || !strings.EqualFold(ev.Currency, target.Currency) {
		return s.park(ctx, m, fmt.Sprintf("capture mismatch: event says %d %s, order expects %d %s",
			ev.AmountMinor, ev.Currency, target.FinalAmountMinor, target.Currency))
	}

	receipt, err := json.Marshal(events.OrderPaid{
		OrderID:        target.OrderID,
		UserID:         target.UserID,
		Email:          target.Email,
		CreditsGranted: target.CreditsGranted,
		Currency:       target.Currency,
		AmountMinor:    strconv.FormatInt(target.FinalAmountMinor, 10),
	})
	if err != nil {
		return err
	}
	if err := s.db.SettleOrder(ctx, target, ev.Gateway, ev.GatewayPaymentID, receipt); err != nil {
		if errors.Is(err, store.ErrStaleSettlement) {
			return s.park(ctx, m, "stale capture: lost the race to a terminal state")
		}
		return err // transient — redeliver
	}
	s.logger.Info("order settled", "orderId", target.OrderID,
		"credits", target.CreditsGranted, "eventId", ev.EventID)
	return nil
}

func classifyCapture(gateway, eventType string) captureClass {
	t := strings.ToLower(eventType)
	if captureEvents[gateway][t] {
		return captureYes
	}
	if strings.Contains(t, "captured") || strings.Contains(t, "succeeded") ||
		strings.Contains(t, "completed") || strings.Contains(t, "paid") {
		return captureUnknown
	}
	return captureNo
}

var captureEvents = map[string]map[string]bool{
	"RAZORPAY": {"payment.captured": true, "order.paid": true},
	"STRIPE":   {"payment_intent.succeeded": true, "charge.succeeded": true},
	"CASHFREE": {"payment_success_webhook": true},
	"PAYPAL":   {"payment.capture.completed": true, "checkout.order.completed": true},
}

type captureClass int

const (
	captureNo captureClass = iota
	captureYes
	captureUnknown
)

func (s *Service) handleRefund(ctx context.Context, m kafka.Message, ev events.PaymentEvent) error {
	t, err := s.db.FindRefundForSettlement(ctx, ev.Gateway, ev.GatewayRefundID, ev.GatewayPaymentID, ev.AmountMinor)

	if errors.Is(err, store.ErrNotFound) {
		return s.park(ctx, m, "refund event for unknown refund "+ev.GatewayRefundID)
	}

	if err != nil {
		return err
	}

	switch {
	case isRefundDone(ev.EventType):
		if ev.AmountMinor != 0 && ev.AmountMinor != t.AmountMinor { // 0 = no amount sent → trust our row; anything else — incl. the -1 sentinel — MUST match
			return s.park(ctx, m, fmt.Sprintf("refund amount mismatch: event says %d, row says %d", ev.AmountMinor, t.AmountMinor))
		}
		receipt, err := json.Marshal(events.OrderRefunded{
			OrderID:     t.OrderID,
			RefundID:    t.RefundID,
			UserID:      t.UserID,
			Email:       t.Email,
			Currency:    t.Currency,
			AmountMinor: strconv.FormatInt(t.AmountMinor, 10),
		})
		if err != nil {
			return err
		}
		clawed, err := s.db.SettleRefund(ctx, t, ev.GatewayRefundID, receipt)
		if errors.Is(err, store.ErrStaleRefund) {
			return s.park(ctx, m, "refund done event for refund in state "+t.Status)
		}
		if err != nil {
			return err
		}
		s.logger.Info("refund settled", "refundId", t.RefundID, "orderId", t.OrderID, "creditsClawedBack", clawed, "eventId", ev.EventID)
		return nil
	case isRefundFailed(ev.EventType):
		if err := s.db.MarkRefundFailed(ctx, t.RefundID); err != nil {
			return err
		}
		s.logger.Warn("refund failed at provider", "refundId", t.RefundID, "eventId", ev.EventID)
		return nil
	default:
		return nil // created / speed-changed / pending — nothing to settle yet
	}
}

func isRefundDone(eventType string) bool {
	t := strings.ToLower(eventType)
	return strings.Contains(t, "processed") || strings.Contains(t, "succeeded") ||
		strings.Contains(t, "refunded") || strings.Contains(t, "refund_success")
}

func isRefundFailed(eventType string) bool {
	t := strings.ToLower(eventType)
	return strings.Contains(t, "failed") || strings.Contains(t, "cancelled")
}

func (s *Service) handleDispute(ctx context.Context, m kafka.Message, ev events.PaymentEvent) error {
	t, err := s.db.FindOrderForDispute(ctx, ev.Gateway, ev.GatewayPaymentID)
	if errors.Is(err, store.ErrNotFound) {
		return s.park(ctx, m, "dispute for unknown payment "+ev.GatewayPaymentID)
	}
	if err != nil {
		return err
	}
	if err := s.db.SettleDispute(ctx, t, ev.Gateway, ev.GatewayDisputeID, disputeStatus(ev.EventType), ev.AmountMinor); err != nil {
		return err
	}
	s.logger.Info("dispute applied", "orderId", t.OrderID, "disputeId", ev.GatewayDisputeID, "eventType", ev.EventType)
	return nil
}

func disputeStatus(eventType string) string {
	t := strings.ToLower(eventType)
	switch {
	case strings.Contains(t, "lost"):
		return "LOST"
	case strings.Contains(t, "won"):
		return "WON"
	case strings.Contains(t, "closed"), strings.Contains(t, "resolved"): // ambiguous closures stay UNDER_REVIEW — a human decides, not a substring
		return "UNDER_REVIEW"
	default:
		return "NEEDS_RESPONSE"
	}
}
