package webhookingest

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
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

	

	return mux
}

func (h *Handler)Receive(w http.ResponseWriter, r *http.Request){

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
