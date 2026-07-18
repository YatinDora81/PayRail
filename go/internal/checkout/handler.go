package checkout

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/payrail/go/internal/httpx"
	"github.com/payrail/go/internal/middleware"
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
