package webhookingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lucsky/cuid"
	"github.com/payrail/go/internal/events"
	"github.com/payrail/go/internal/httpx"
	"github.com/payrail/go/internal/middleware"
	"github.com/payrail/go/internal/telemetry"
)

type verifier interface {
	VerifyWebhook(ctx context.Context, provider string, body []byte, headers http.Header) (bool, error)
}

type Handler struct {
	accepted map[string]bool
	verify   verifier
	store    *Store
	logger   *slog.Logger
}

var sigHeader = map[string]string{
	"RAZORPAY": "X-Razorpay-Signature",
	"STRIPE":   "Stripe-Signature",
	"CASHFREE": "x-webhook-signature",
	"PAYPAL":   "Paypal-Transmission-Sig",
}

func NewHandler(verify verifier, store *Store, logger *slog.Logger) *Handler {
	return &Handler{
		accepted: map[string]bool{"RAZORPAY": true, "STRIPE": true, "CASHFREE": true, "PAYPAL": true},
		verify:   verify,
		store:    store,
		logger:   logger,
	}
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func NewRouter(h *Handler, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.Handle("POST /webhooks/{provider}", capConcurrency(256, http.HandlerFunc(h.Receive)))

	return middleware.Chain(mux,
		middleware.RequestId,
		middleware.Logger(logger),
		middleware.RealIP,
		middleware.Recoverer(logger),
		middleware.Timeout(15*time.Second),
	)
}

func (h *Handler) Receive(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceID(r)
	provider := strings.ToUpper(r.PathValue("provider"))

	if !h.accepted[provider] {
		httpx.WriteError(w, traceID, httpx.BadRequest("unknown provider"))
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 2<<20)) // 2mb
	if err != nil {
		httpx.WriteError(w, traceID, httpx.BadRequest("could not read body"))
		return
	}

	ok, err := h.verify.VerifyWebhook(r.Context(), provider, body, r.Header)
	if err != nil {
		h.logger.Error("verify webhook", "provider", provider, "err", err, "traceId", traceID)
		httpx.WriteError(w, traceID, httpx.NewError(http.StatusBadGateway, "verify_unavailable", "could not verify signature"))
		return
	}
	if !ok {
		httpx.WriteError(w, traceID, httpx.NewError(http.StatusBadRequest, "invalid_signature", "signature verification failed"))
		return
	}

	ev := normalize(provider, body, r.Header)
	if partitionKey(ev) == "" {
		h.logger.Warn("skipping unroutable webhook", "provider", provider, "eventType", ev.EventType, "traceId", traceID)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"received": true, "skipped": true})
		return
	}

	duplicate, err := h.store.Record(r.Context(), ev, body, r.Header.Get(sigHeader[provider]))
	if err != nil {
		h.logger.Error("store webhook", "provider", provider, "err", err, "traceId", traceID)
		httpx.WriteError(w, traceID, httpx.Internal())
		return
	}

	if duplicate {
		telemetry.Counter("payrail_webhook_duplicates_total").Add(r.Context(), 1)
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"received": true, "duplicate": duplicate})

}

func normalize(provider string, body []byte, hdr http.Header) events.PaymentEvent {

	ev := events.PaymentEvent{Gateway: provider, Kind: events.KindPayment, OccurredAt: time.Now().UTC()}

	switch provider {
	case "RAZORPAY":
		var p struct {
			Event   string `json:"event"`
			Payload struct {
				Payment struct {
					Entity struct {
						ID       string `json:"id"`
						OrderID  string `json:"order_id"`
						Amount   int64  `json:"amount"` // already minor units (paise)
						Currency string `json:"currency"`
					} `json:"entity"`
				} `json:"payment"`
				Refund struct {
					Entity struct {
						ID        string `json:"id"`
						PaymentID string `json:"payment_id"`
						Amount    int64  `json:"amount"`
						Currency  string `json:"currency"`
					} `json:"entity"`
				} `json:"refund"`
				Dispute struct {
					Entity struct {
						ID        string `json:"id"`
						PaymentID string `json:"payment_id"`
						Amount    int64  `json:"amount"`
						Status    string `json:"status"`
					} `json:"entity"`
				} `json:"dispute"`
			} `json:"payload"`
		}

		_ = json.Unmarshal(body, &p)
		ev.EventType = p.Event
		ev.EventID = hdr.Get("X-Razorpay-Event-Id")

		if ev.EventID == "" {
			sum := sha256.Sum256(body)
			ev.EventID = "razorpay:sha:" + hex.EncodeToString(sum[:16])
		}

		switch {
		case strings.HasPrefix(p.Event, "refund."):
			ev.Kind = events.KindRefund
			ev.GatewayRefundID = p.Payload.Refund.Entity.ID
			ev.GatewayPaymentID = p.Payload.Refund.Entity.PaymentID
			ev.AmountMinor = p.Payload.Refund.Entity.Amount
			ev.Currency = p.Payload.Refund.Entity.Currency
		case strings.Contains(p.Event, "dispute"):
			ev.Kind = events.KindDispute
			ev.GatewayDisputeID = p.Payload.Dispute.Entity.ID
			ev.GatewayPaymentID = p.Payload.Dispute.Entity.PaymentID
			ev.AmountMinor = p.Payload.Dispute.Entity.Amount
		default:
			ev.GatewayOrderID = p.Payload.Payment.Entity.OrderID
			ev.GatewayPaymentID = p.Payload.Payment.Entity.ID
			ev.AmountMinor = p.Payload.Payment.Entity.Amount
			ev.Currency = p.Payload.Payment.Entity.Currency
		}
	case "STRIPE":
		var p struct {
			ID   string `json:"id"` // evt_… — Stripe's global event id
			Type string `json:"type"`
			Data struct {
				Object struct {
					ID             string `json:"id"`
					PaymentIntent  string `json:"payment_intent"`
					Charge         string `json:"charge"`          // set on refund objects
					Amount         int64  `json:"amount"`          // minor units
					AmountReceived int64  `json:"amount_received"` // intents: what actually landed
					AmountCaptured int64  `json:"amount_captured"` // charges: ditto (§B4)
					Currency       string `json:"currency"`
					Status         string `json:"status"`
				} `json:"object"`
			} `json:"data"`
		}

		_ = json.Unmarshal(body, &p)
		ev.EventType = p.Type
		ev.EventID = p.ID
		ev.Currency = strings.ToUpper(p.Data.Object.Currency)

		switch {
		case strings.HasPrefix(p.Type, "charge.refund") || strings.HasPrefix(p.Type, "refund."):
			ev.Kind = events.KindRefund
			ev.GatewayRefundID = p.Data.Object.ID // re_…
			ev.GatewayPaymentID = p.Data.Object.Charge
			ev.AmountMinor = p.Data.Object.Amount
		case strings.HasPrefix(p.Type, "charge.dispute"):
			ev.Kind = events.KindDispute
			ev.GatewayDisputeID = p.Data.Object.ID // dp_…
			ev.GatewayPaymentID = p.Data.Object.Charge
			ev.AmountMinor = p.Data.Object.Amount
		default:
			// payment_intent.*: the object id IS the intent (our gatewayOrderId);
			// charge.*: the intent is a field and the charge id is the payment id.
			if p.Data.Object.PaymentIntent != "" {
				ev.GatewayOrderID = p.Data.Object.PaymentIntent
				ev.GatewayPaymentID = p.Data.Object.ID
			} else {
				ev.GatewayOrderID = p.Data.Object.ID
			}
			// Prefer what Stripe says LANDED over what was authorized — on a
			// partially captured charge, `amount` is the auth, not the money.
			switch {
			case p.Data.Object.AmountReceived > 0:
				ev.AmountMinor = p.Data.Object.AmountReceived
			case p.Data.Object.AmountCaptured > 0:
				ev.AmountMinor = p.Data.Object.AmountCaptured
			default:
				ev.AmountMinor = p.Data.Object.Amount
			}
		}

	case "CASHFREE":
		{
			var p struct {
				Type string `json:"type"`
				Data struct {
					Order struct {
						OrderID  string `json:"order_id"`
						Currency string `json:"order_currency"`
					} `json:"order"`
					Payment struct {
						ID     json.Number `json:"cf_payment_id"`
						Amount json.Number `json:"payment_amount"` // decimal, e.g. 499.00
					} `json:"payment"`
					Refund struct {
						ID      json.Number `json:"cf_refund_id"`
						OrderID string      `json:"order_id"`
						Amount  json.Number `json:"refund_amount"`
						Status  string      `json:"refund_status"`
					} `json:"refund"`
				} `json:"data"`
			}

			_ = json.Unmarshal(body, &p)
			ev.EventType = p.Type
			ev.Currency = p.Data.Order.Currency
			if strings.HasPrefix(p.Type, "REFUND") {
				ev.Kind = events.KindRefund
				ev.GatewayRefundID = p.Data.Refund.ID.String()
				ev.GatewayOrderID = p.Data.Refund.OrderID
				ev.AmountMinor = decimalToMinor(p.Data.Refund.Amount.String())
				ev.EventID = "cashfree:" + ev.GatewayRefundID + ":" + strings.ToLower(p.Type)
			} else {
				ev.GatewayOrderID = p.Data.Order.OrderID
				ev.GatewayPaymentID = p.Data.Payment.ID.String()
				ev.AmountMinor = decimalToMinor(p.Data.Payment.Amount.String())
				// Cashfree has no global event id; payment id + type is stable per delivery.
				ev.EventID = "cashfree:" + ev.GatewayPaymentID + ":" + strings.ToLower(p.Type)
			}
		}
	case "PAYPAL":
		var p struct {
			ID        string `json:"id"` // WH-… — PayPal's event id
			EventType string `json:"event_type"`
			Resource  struct {
				ID     string `json:"id"`
				Amount struct {
					Value    string `json:"value"` // decimal string
					Currency string `json:"currency_code"`
				} `json:"amount"`
				SupplementaryData struct {
					RelatedIDs struct {
						OrderID   string `json:"order_id"`
						CaptureID string `json:"capture_id"`
					} `json:"related_ids"`
				} `json:"supplementary_data"`
			} `json:"resource"`
		}
		_ = json.Unmarshal(body, &p)
		ev.EventType = p.EventType
		ev.EventID = p.ID
		ev.AmountMinor = decimalToMinor(p.Resource.Amount.Value)
		ev.Currency = p.Resource.Amount.Currency
		switch {
		case strings.HasPrefix(p.EventType, "PAYMENT.CAPTURE.REFUNDED") || strings.HasPrefix(p.EventType, "PAYMENT.REFUND"):
			ev.Kind = events.KindRefund
			ev.GatewayRefundID = p.Resource.ID // the refund resource
			ev.GatewayPaymentID = p.Resource.SupplementaryData.RelatedIDs.CaptureID
		case strings.HasPrefix(p.EventType, "CUSTOMER.DISPUTE"):
			ev.Kind = events.KindDispute
			ev.GatewayDisputeID = p.Resource.ID
		default:
			ev.GatewayPaymentID = p.Resource.ID
			if p.Resource.SupplementaryData.RelatedIDs.OrderID != "" {
				ev.GatewayOrderID = p.Resource.SupplementaryData.RelatedIDs.OrderID
			} else {
				ev.GatewayOrderID = p.Resource.ID
			}
		}
	}

	// Never let a missing provider event id defeat the dedupe key.
	if ev.EventID == "" {
		ev.EventID = strings.ToLower(provider) + ":" + partitionKey(ev) + ":" + strings.ToLower(ev.EventType)
	}
	return ev
}

func (s *Store) Record(ctx context.Context, ev events.PaymentEvent, rawBody []byte, signature string) (duplicate bool, err error) {

	payload, err := json.Marshal(ev)
	if err != nil {
		return false, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)

	sha := sha256.Sum256(rawBody)
	ct, err := tx.Exec(ctx, `
		INSERT INTO "WebhookEvents" ("id","gateway","eventId","eventType","rawBody","bodySha","signature","status")
		VALUES ($1,$2,$3,$4,$5,$6,$7,'RECEIVED')
		ON CONFLICT ("eventId") DO NOTHING`,
		cuid.New(), ev.Gateway, ev.EventID, ev.EventType, rawBody, hex.EncodeToString(sha[:]), signature)

	if err != nil {
		return false, err
	}

	if ct.RowsAffected() == 0 {
		return true, tx.Commit(ctx) // seen this delivery before — ack, do nothing
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO "OutboxEvent" ("id","topic","partitionKey","payload","headers")
		VALUES ($1,$2,$3,$4,$5)`,
		cuid.New(), events.TopicPaymentEvents, partitionKey(ev), payload,
		map[string]string{"eventId": ev.EventID}); err != nil { // eventId rides as a Kafka header — one grep key across every system
		return false, err
	}
	return false, tx.Commit(ctx)
}

func partitionKey(ev events.PaymentEvent) string {
	switch {
	case ev.GatewayOrderID != "":
		return ev.GatewayOrderID
	case ev.GatewayPaymentID != "":
		return ev.GatewayPaymentID
	default:
		return ev.GatewayRefundID
	}
}

// 499.5 -> 49950
func decimalToMinor(v string) int64 {
	v = strings.TrimSpace(v)
	if v == "" || strings.ContainsAny(v, "-+eE ") { // reject signs & exponents outright — money strings are plain decimals
		return 0
	}
	whole, frac, _ := strings.Cut(v, ".")
	if len(frac) > 2 {
		return 0 // 3+ decimals on a 2-decimal platform: don't guess — park it
	}
	frac = (frac + "00")[:2] // right-pad then slice: "5"→"50", ""→"00" — .5 is 50 minor units
	n, err := strconv.ParseInt(whole+frac, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func capConcurrency(n int, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slots := make(chan struct{}, n)
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
			next.ServeHTTP(w, r)
		default:
			w.Header().Set("Retry-After", "2")
			http.Error(w, "busy", http.StatusTooManyRequests)
		}
	})
}
