package checkout

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/payrail/go/internal/middleware"
)

type Pingable interface {
	Ping(ctx context.Context) error
}

func NewRouter(h *Handler, db, rdb Pingable, userJwtSecret string, allow middleware.AllowFunc, logger *slog.Logger) http.Handler {
	api := http.NewServeMux()

	api.HandleFunc("GET /v1/plans", h.ListPlans)
	api.HandleFunc("GET /v1/bank-offers" , h.BankOffers)
	api.HandleFunc("POST /v1/orders/preview" , h.PreviewOrder)
	// api.HandleFunc("POST /v1/orders", h.)

	return api
}
