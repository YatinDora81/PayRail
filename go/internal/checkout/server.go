package checkout

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/payrail/go/internal/middleware"
)

type Pingable interface {
	Ping(ctx context.Context) error
}

func NewRouter(h *Handler, db, rdb Pingable, jwtCfg middleware.UserJWTConfig, allow middleware.AllowFunc, logger *slog.Logger) http.Handler {
	api := http.NewServeMux()

	api.HandleFunc("GET /v1/plans", h.ListPlans)
	api.HandleFunc("GET /v1/bank-offers", h.BankOffers)
	api.HandleFunc("POST /v1/orders/preview", h.PreviewOrder)
	api.HandleFunc("POST /v1/orders", h.CreateOrder)
	api.HandleFunc("GET /v1/orders", h.ListOrders)
	api.HandleFunc("GET /v1/orders/{id}", h.GetOrder)
	api.HandleFunc("GET /v1/me/credits", h.Credits)

	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /readyz", readiness(db, rdb))

	protected := middleware.Chain(api,
		middleware.UserAuth(jwtCfg), // multi-secret rotation · iss/aud/nbf checked
		middleware.RateAllow(allow),
		middleware.BodyLimit(64<<10),
	)

	mux.Handle("/v1/", protected)

	return middleware.Chain(mux,
		middleware.RequestId,
		middleware.Logger(logger),
		middleware.RealIP,
		middleware.Recoverer(logger),
		middleware.Timeout(15*time.Second), // outermost bound — nothing outlives 15s, whatever a handler does
	)
}

func readiness(db, rdb Pingable) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := db.Ping(ctx); err != nil {
			http.Error(w, "db down", http.StatusServiceUnavailable)
			return
		}
		if err := rdb.Ping(ctx); err != nil {
			http.Error(w, "redis down", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	}
}
