package checkout

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/payrail/go/internal/httpx"
	"github.com/payrail/go/internal/middleware"
	"github.com/payrail/go/internal/store"
)

type Handler struct {
	svc    *Service
	logger *slog.Logger
}

func NewHandler(svc *Service, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, logger: logger}
}

func (h *Handler) ListPlans(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceID(r)
	country := r.URL.Query().Get("country")
	if country == "" {
		country = "US"
	}

	plans, err := h.svc.ListPlans(r.Context(), country)
	if err != nil {
		h.logger.Error("list plans", "err", err, "traceId", traceID)
		httpx.WriteError(w, traceID, httpx.Internal())
		return
	}

	out := make([]planResponse, 0, len(plans))
	for _, p := range plans {
		out = append(out, planResponse{
			ID: p.ID, Name: p.Name, Description: p.Description, Credits: p.Credits,
			Currency: p.Currency, Amount: strconv.FormatInt(p.AmountMinor, 10),
		})
	}

	httpx.WriteJSON(w, http.StatusOK, out)
}

type planResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Credits     int    `json:"credits"`
	Currency    string `json:"currency"`
	Amount      string `json:"amount"`
}

func (h *Handler) BankOffers(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceID(r)
	country := r.URL.Query().Get("country")
	if country == "" {
		country = "US"
	}
	offers, err := h.svc.BankOffers(r.Context(), country)
	if err != nil {
		h.logger.Error("list bank offers", "err", err, "traceId", traceID)
		httpx.WriteError(w, traceID, httpx.Internal())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, offers)
}

type previewRequest struct {
	PlanID       string   `json:"planId"`
	Country      string   `json:"country"`
	City         string   `json:"city"`
	PromotionIDs []string `json:"promotionIds"`
	CouponCode   string   `json:"couponCode"`
}

func (h *Handler) PreviewOrder(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceID(r)
	var req previewRequest

	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.WriteError(w, traceID, err)
		return
	}

	if req.PlanID == "" || req.Country == "" {
		httpx.WriteError(w, traceID, httpx.BadRequest("planId and country are required"))
		return
	}

	promoIDs, perr := normalizePromoIDs(req.PromotionIDs)
	if perr != nil {
		httpx.WriteError(w, traceID, perr)
		return
	}

	q, err := h.svc.PreviewOrder(r.Context(), CreateOrderInput{
		UserID:       userFromRequest(r),
		PlanID:       req.PlanID,
		Country:      req.Country,
		City:         req.City,
		PromotionIDs: promoIDs,
		CouponCode:   strings.ToUpper(strings.TrimSpace(req.CouponCode)),
		TraceID:      traceID,
	})

	if err != nil {
		httpx.WriteError(w, traceID, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"currency":       q.Currency,
		"baseAmount":     strconv.FormatInt(q.BaseMinor, 10),
		"discountAmount": strconv.FormatInt(q.DiscountMinor, 10),
		"taxIncluded":    strconv.FormatInt(q.TaxIncludedMinor, 10),
		"finalAmount":    strconv.FormatInt(q.FinalMinor, 10),
		"credits":        q.Credits,
	})
}

func normalizePromoIDs(ids []string) ([]string, error) {
	seen := map[string]bool{}
	out := ids[:0]
	for _, id := range ids {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	if len(out) > 3 {
		return nil, httpx.BadRequest("at most 3 promotions per order")
	}
	return out, nil
}

func userFromRequest(r *http.Request) string {
	return middleware.UserID(r.Context())
}

type createOrderRequest struct {
	PlanID       string   `json:"planId"`
	Country      string   `json:"country"`
	City         string   `json:"city"`
	Gateway      string   `json:"gateway"`
	PromotionIDs []string `json:"promotionIds"`
	CouponCode   string   `json:"couponCode"`
}

func (h *Handler) CreateOrder(w http.ResponseWriter, r *http.Request) {

	traceID := httpx.TraceID(r)
	var req createOrderRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.WriteError(w, traceID, err)
		return
	}
	if err := req.validate(); err != nil {
		httpx.WriteError(w, traceID, err)
		return
	}

	userID := userFromRequest(r)
	if userID == "" {
		httpx.WriteError(w, traceID, httpx.NewError(http.StatusUnauthorized, "unauthorized", "missing user"))
		return
	}

	idemKey := r.Header.Get("Idempotency-Key")
	if idemKey == "" {
		httpx.WriteError(w, traceID, httpx.BadRequest("Idempotency-Key header is required"))
		return
	}

	promoIDs, perr := normalizePromoIDs(req.PromotionIDs)
	if perr != nil {
		httpx.WriteError(w, traceID, perr)
		return
	}

	result, err := h.svc.CreateOrder(r.Context(), CreateOrderInput{
		UserID:         userID,
		IdempotencyKey: idemKey,
		PlanID:         req.PlanID,
		Country:        req.Country,
		City:           req.City,
		Gateway:        req.Gateway,
		PromotionIDs:   promoIDs,
		CouponCode:     strings.ToUpper(strings.TrimSpace(req.CouponCode)), // canonical form at the edge — the DB stores uppercase
		TraceID:        traceID,
	})
	if err != nil {
		httpx.WriteError(w, traceID, err)
		return
	}

	resp := toOrderResponse(result.Order)
	resp.Gateway = result.Gateway
	resp.GatewayOrderID = result.GatewayOrderID
	resp.ClientParams = result.ClientParams
	resp.Replayed = result.Replayed

	status := http.StatusCreated
	if result.Replayed {
		status = http.StatusOK
	}

	httpx.WriteJSON(w, status, resp)
}

func (req createOrderRequest) validate() error {
	switch {
	case req.PlanID == "":
		return httpx.BadRequest("planId is required")
	case req.Country == "":
		return httpx.BadRequest("country is required")
	case req.Gateway == "":
		return httpx.BadRequest("gateway is required")
	}
	return nil
}

type orderResponse struct {
	OrderID        string         `json:"orderId"`
	Status         string         `json:"status"`
	Currency       string         `json:"currency"`
	BaseAmount     string         `json:"baseAmount"`
	DiscountAmount string         `json:"discountAmount"`
	FinalAmount    string         `json:"finalAmount"`
	CreditsGranted int            `json:"creditsGranted"`
	ExpiresAt      string         `json:"expiresAt"`
	Gateway        string         `json:"gateway,omitempty"`
	GatewayOrderID string         `json:"gatewayOrderId,omitempty"`
	ClientParams   map[string]any `json:"clientParams,omitempty"`
	Replayed       bool           `json:"replayed,omitempty"`
}

func toOrderResponse(o store.Order) orderResponse {
	resp := orderResponse{
		OrderID:        o.ID,
		Status:         o.Status,
		Currency:       o.Currency,
		BaseAmount:     strconv.FormatInt(o.BaseAmountMinor, 10),
		DiscountAmount: strconv.FormatInt(o.DiscountAmountMinor, 10),
		FinalAmount:    strconv.FormatInt(o.FinalAmountMinor, 10),
		CreditsGranted: o.CreditsGranted,
		ExpiresAt:      o.ExpiresAt.UTC().Format(time.RFC3339),
	}
	if o.Gateway != nil {
		resp.Gateway = *o.Gateway
	}
	if o.GatewayOrderID != nil {
		resp.GatewayOrderID = *o.GatewayOrderID
	}
	return resp
}
