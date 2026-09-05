package events

import "time"

const (
	KindPayment = "PAYMENT"
	KindRefund  = "REFUND"
	KindDispute = "DISPUTE"
)

const (
	TopicPaymentEvents           = "payment.events"            // webhook-ingest -> settlement-worker (payments, refunds, disputes)
	TopicOrderPaid               = "order.paid"                // settlement-worker -> email-worker + invoice assigner
	TopicOrderRefunded           = "order.refunded"            // settlement-worker -> email-worker (REFUND_DONE)
	TopicPromotionBudgetUpserted = "promotion.budget.upserted" // admin-api -> reconciler
	TopicPromotionActivated      = "promotion.activated"       // admin-api -> reconciler
)

type PaymentEvent struct {
	Kind             string    `json:"kind"` // PAYMENT | REFUND | DISPUTE
	Gateway          string    `json:"gateway"`
	GatewayOrderID   string    `json:"gatewayOrderId"`
	GatewayPaymentID string    `json:"gatewayPaymentId"`
	GatewayRefundID  string    `json:"gatewayRefundId,omitempty"`  // set on Kind=REFUND
	GatewayDisputeID string    `json:"gatewayDisputeId,omitempty"` // set on Kind=DISPUTE
	EventID          string    `json:"eventId"`
	EventType        string    `json:"eventType"`
	AmountMinor      int64     `json:"amountMinor"` // 0 = the edge could not extract one (fail closed)
	Currency         string    `json:"currency"`
	OccurredAt       time.Time `json:"occurredAt"`
}

type OrderPaid struct {
	OrderID        string `json:"orderId"`
	UserID         string `json:"userId"`
	Email          string `json:"email"`
	CreditsGranted int    `json:"creditsGranted"`
	Currency       string `json:"currency"`
	AmountMinor    string `json:"amountMinor"` // money as string
}


type OrderRefunded struct {
	OrderID           string `json:"orderId"`
	RefundID          string `json:"refundId"`
	UserID            string `json:"userId"`
	Email             string `json:"email"`
	CreditsClawedBack int    `json:"creditsClawedBack"`
	Currency          string `json:"currency"`
	AmountMinor       string `json:"amountMinor"`
}

type PromotionBudgetUpserted struct {
	PromotionID string `json:"promotionId"`
	Currency    string `json:"currency"`
	CapMinor    string `json:"capMinor"` 
}

type PromotionActivated struct {
	PromotionID string `json:"promotionId"`
}