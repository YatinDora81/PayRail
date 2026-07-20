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
