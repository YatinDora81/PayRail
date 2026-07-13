package checkout

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/payrail/go/internal/httpx"
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

	httpx.WriteJSON(w , http.StatusOK , out)
}

type planResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Credits     int    `json:"credits"`
	Currency    string `json:"currency"`
	Amount      string `json:"amount"`
}
